package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/stake-plus/govcomms/src/research-bot/components/openai"
)

type Analyzer struct {
	client *openai.Client
}

func NewAnalyzer(apiKey string) *Analyzer {
	return &Analyzer{
		client: openai.NewClient(apiKey),
	}
}

func (a *Analyzer) ExtractTopClaims(ctx context.Context, proposalContent string) ([]Claim, int, error) {
	// Truncate content if too long
	maxContentLength := 10000
	if len(proposalContent) > maxContentLength {
		proposalContent = proposalContent[:maxContentLength] + "\n\n[Content truncated for analysis]"
	}

	prompt := fmt.Sprintf(`Analyze this blockchain governance proposal and extract HISTORICAL/BACKGROUND claims that can be verified online.

IMPORTANT: DO NOT extract obvious current facts about this proposal such as:
- The amount of funding being requested
- The proposer's on-chain address
- The submission date of this proposal
- The proposal ID or referendum number
- Current voting status

INSTEAD, focus on PAST ACTIVITIES and ACHIEVEMENTS that the team claims to have done:
- Previous deliverables completed (videos published, code written, events organized)
- Team member backgrounds and experience
- Past project metrics (user counts, engagement, views)
- Previous funding received and how it was used
- Historical partnerships or collaborations
- Prior work in the ecosystem

For each claim, extract ALL URLs mentioned in the proposal that could help verify it (can be multiple).

Count total verifiable HISTORICAL claims, then select the 10 MOST IMPORTANT based on:
1. Past deliverables with public proof (videos, code, documents)
2. Team member credentials and past experience
3. Historical metrics that can be independently checked
4. Previous achievements in the ecosystem

Respond with JSON:
{
  "total_claims": 25,
  "top_claims": [
    {
      "claim": "Published 41 educational videos on YouTube channel",
      "category": "deliverables",
      "urls": ["https://youtube.com/@polkadotamericas", "https://youtube.com/playlist?list=xyz"],
      "context": "Past content creation work"
    },
    {
      "claim": "César Escobedo has 5 years experience in blockchain development",
      "category": "team",
      "urls": ["https://github.com/cesarescobedo", "https://linkedin.com/in/cesarescobedo"],
      "context": "Team lead background"
    },
    {
      "claim": "Previously organized 12 community meetups with 500+ total attendees",
      "category": "deliverables",
      "urls": ["https://meetup.com/polkadot-mexico"],
      "context": "Past community building activities"
    }
  ]
}

Proposal:
%s`, proposalContent)

	response, err := a.client.CreateResponseNoSearch(ctx, prompt)
	if err != nil {
		return nil, 0, err
	}

	responseText := response.GetText()
	if responseText == "" {
		return []Claim{}, 0, nil
	}

	var claimsResponse ClaimsResponse
	if err := json.Unmarshal([]byte(responseText), &claimsResponse); err != nil {
		// Try to extract JSON if embedded
		startIdx := strings.Index(responseText, "{")
		endIdx := strings.LastIndex(responseText, "}")
		if startIdx >= 0 && endIdx > startIdx {
			jsonStr := responseText[startIdx : endIdx+1]
			if err := json.Unmarshal([]byte(jsonStr), &claimsResponse); err != nil {
				log.Printf("Failed to parse claims response: %v", err)
				return []Claim{}, 0, nil
			}
		} else {
			return []Claim{}, 0, nil
		}
	}

	log.Printf("Found %d total verifiable claims, returning top %d for verification",
		claimsResponse.TotalClaims, len(claimsResponse.TopClaims))

	return claimsResponse.TopClaims, claimsResponse.TotalClaims, nil
}

func (a *Analyzer) VerifySingleClaim(ctx context.Context, claim Claim) VerificationResult {
	prompt := fmt.Sprintf(`You are a blockchain governance proposal detective. Verify this specific HISTORICAL claim using web search.

Claim: "%s"
Category: %s`, claim.Claim, claim.Category)

	if len(claim.URLs) > 0 {
		prompt += fmt.Sprintf("\nProposal provided URLs to check: %s", strings.Join(claim.URLs, ", "))
	}

	if claim.Context != "" {
		prompt += fmt.Sprintf("\nContext: %s", claim.Context)
	}

	prompt += `

Instructions:
1. If URLs were provided, search for and verify information at those specific locations
2. For GitHub claims: Check repositories, commit history, contributor activity
3. For LinkedIn/Twitter: Verify the person exists and their stated credentials
4. For metrics (views, followers): Get current numbers and verify claims
5. For YouTube claims: Check all videos/playlists mentioned, sum up total views if needed
6. For financial claims: Look for on-chain data or official announcements
7. Be skeptical - look for evidence that confirms OR refutes the claim

Provide your verdict as:
- VALID: Clear evidence supports the claim
- REJECTED: Evidence contradicts the claim
- UNKNOWN: Cannot find sufficient evidence online

Format your response EXACTLY as:
STATUS: [Valid/Rejected/Unknown]
EVIDENCE: [One sentence with specific details found]
SOURCES: [Comma-separated list of primary URLs where you found evidence, or "No sources found"]`

	response, err := a.client.CreateResponseWithWebSearchRetry(ctx, prompt)
	if err != nil {
		return VerificationResult{
			Claim:      claim.Claim,
			Status:     StatusUnknown,
			Evidence:   "Failed to verify",
			SourceURLs: []string{},
		}
	}

	responseText := response.GetText()
	citations := response.GetCitations()

	status, evidence, sourceURLs := a.parseVerificationResponse(responseText)

	// Add citations if no source URLs were parsed
	if len(sourceURLs) == 0 && len(citations) > 0 {
		sourceURLs = citations
	}

	return VerificationResult{
		Claim:      claim.Claim,
		Status:     status,
		Evidence:   evidence,
		SourceURLs: sourceURLs,
	}
}

func (a *Analyzer) VerifyClaims(ctx context.Context, claims []Claim) ([]VerificationResult, error) {
	results := make([]VerificationResult, len(claims))
	semaphore := make(chan struct{}, 1) // 1 concurrent operation

	// Initial delay to let rate limits reset
	log.Printf("Waiting 120 seconds before starting verification...")
	select {
	case <-ctx.Done():
		return results, ctx.Err()
	case <-time.After(120 * time.Second):
	}

	// Process claims one at a time
	for i := 0; i < len(claims); i++ {
		// Create a new context with timeout for this claim
		claimCtx, claimCancel := context.WithCancel(context.Background())

		var wg sync.WaitGroup

		select {
		case <-ctx.Done(): // Check parent context
			claimCancel()
			return results, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(index int, c Claim) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-claimCtx.Done():
				results[index] = VerificationResult{
					Claim:      c.Claim,
					Status:     StatusUnknown,
					Evidence:   "Verification timeout",
					SourceURLs: []string{},
				}
				return
			}

			log.Printf("Verifying claim %d of %d: %s", index+1, len(claims), c.Claim)
			result := a.VerifySingleClaim(claimCtx, c)
			results[index] = result
			log.Printf("Claim %d verification result: %s", index+1, result.Status)
		}(i, claims[i])

		// Wait for this claim to complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Claim completed successfully
		case <-claimCtx.Done():
			// Claim timeout
			log.Printf("Claim %d timed out", i+1)
		}

		claimCancel()

		// Wait 120 seconds between each claim to avoid rate limiting
		if i < len(claims)-1 {
			log.Printf("Waiting 120 seconds before next claim...")
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(120 * time.Second):
			}
		}
	}

	return results, nil
}

func (a *Analyzer) parseVerificationResponse(response string) (VerificationStatus, string, []string) {
	lines := strings.Split(response, "\n")
	var status VerificationStatus = StatusUnknown
	var evidence string
	var sourceURLs []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "STATUS:") {
			statusStr := strings.TrimSpace(strings.TrimPrefix(upper, "STATUS:"))
			switch statusStr {
			case "VALID":
				status = StatusValid
			case "REJECTED":
				status = StatusRejected
			default:
				status = StatusUnknown
			}
		} else if strings.HasPrefix(upper, "EVIDENCE:") {
			evidence = strings.TrimSpace(strings.TrimPrefix(line, "EVIDENCE:"))
			if evidence == "" {
				evidence = strings.TrimSpace(strings.TrimPrefix(upper, "EVIDENCE:"))
			}
		} else if strings.HasPrefix(upper, "SOURCES:") || strings.HasPrefix(upper, "SOURCE:") {
			sourcesStr := strings.TrimSpace(strings.TrimPrefix(line, "SOURCES:"))
			if sourcesStr == "" {
				sourcesStr = strings.TrimSpace(strings.TrimPrefix(line, "SOURCE:"))
			}
			if sourcesStr != "" && !strings.EqualFold(sourcesStr, "No sources found") {
				// Split by comma and clean up
				parts := strings.Split(sourcesStr, ",")
				for _, url := range parts {
					url = strings.TrimSpace(url)
					if url != "" && strings.HasPrefix(url, "http") {
						sourceURLs = append(sourceURLs, url)
					}
				}
			}
		}
	}

	if evidence == "" {
		evidence = "Unable to determine"
	}

	return status, evidence, sourceURLs
}
