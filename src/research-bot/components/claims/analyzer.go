package claims

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

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

	prompt := fmt.Sprintf(`Analyze this blockchain governance proposal and extract verifiable claims that can be checked online.

Focus ONLY on claims that can be verified through:
- GitHub repositories and activity
- LinkedIn profiles
- Official project websites
- Twitter/X accounts with metrics
- YouTube channels with view counts
- Published documentation or reports
- Previous blockchain transactions or proposals

SKIP claims about:
- Private Telegram groups or Discord servers
- Unverifiable meetup attendance
- General statements without specific metrics
- Internal team activities without public proof

For each claim, extract any URLs mentioned in the proposal that could help verify it.

Count total verifiable claims, then select the 10 MOST IMPORTANT based on:
1. Financial amounts requested or spent
2. Deliverables with public proof (videos, code, documents)
3. Team member credentials that can be verified online
4. Metrics that can be independently checked

Respond with JSON:
{
  "total_claims": 25,
  "top_claims": [
    {
      "claim": "Requested 1,625 DOT total funding",
      "category": "financial",
      "url": "",
      "context": "Main funding request"
    },
    {
      "claim": "César Escobedo founder of Polkadot México with GitHub profile",
      "category": "team",
      "url": "https://github.com/cesarescobedo",
      "context": "Team lead credentials"
    },
    {
      "claim": "Published 41 videos on YouTube channel",
      "category": "deliverables",
      "url": "https://youtube.com/@polkadotamericas",
      "context": "Content creation metrics"
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
	prompt := fmt.Sprintf(`You are a blockchain governance proposal detective. Verify this specific claim using web search.

Claim: "%s"
Category: %s`, claim.Claim, claim.Category)

	if claim.URL != "" {
		prompt += fmt.Sprintf("\nProposal provided URL: %s", claim.URL)
	}
	if claim.Context != "" {
		prompt += fmt.Sprintf("\nContext: %s", claim.Context)
	}

	prompt += `

Instructions:
1. If a URL was provided, search for and verify information at that specific location
2. For GitHub claims: Check repositories, commit history, contributor activity
3. For LinkedIn/Twitter: Verify the person exists and their stated credentials
4. For metrics (views, followers): Get current numbers if possible
5. For financial claims: Look for on-chain data or official announcements
6. Be skeptical - look for evidence that confirms OR refutes the claim

Provide your verdict as:
- VALID: Clear evidence supports the claim
- REJECTED: Evidence contradicts the claim
- UNKNOWN: Cannot find sufficient evidence online

Format your response EXACTLY as:
STATUS: [Valid/Rejected/Unknown]
EVIDENCE: [One sentence with specific details found]
SOURCE: [Primary URL where you found the evidence, or "No source found"]`

	response, err := a.client.CreateResponseWithWebSearch(ctx, prompt)
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to verify",
		}
	}

	responseText := response.GetText()
	citations := response.GetCitations()

	status, evidence, sourceURL := a.parseVerificationResponse(responseText)

	// Use first citation if no source URL was parsed
	if sourceURL == "" && len(citations) > 0 {
		sourceURL = citations[0]
	}

	return VerificationResult{
		Claim:     claim.Claim,
		Status:    status,
		Evidence:  evidence,
		SourceURL: sourceURL,
	}
}

func (a *Analyzer) VerifyClaims(ctx context.Context, claims []Claim) ([]VerificationResult, error) {
	var wg sync.WaitGroup
	results := make([]VerificationResult, len(claims))
	semaphore := make(chan struct{}, 3)

	log.Printf("Starting verification of %d claims", len(claims))

	for i, claim := range claims {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(index int, c Claim) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = VerificationResult{
					Claim:    c.Claim,
					Status:   StatusUnknown,
					Evidence: "Verification cancelled due to timeout",
				}
				return
			}

			log.Printf("Verifying claim %d: %s", index+1, c.Claim)
			result := a.VerifySingleClaim(ctx, c)
			results[index] = result
			log.Printf("Claim %d verification result: %s", index+1, result.Status)
		}(i, claim)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return results, nil
	case <-ctx.Done():
		return results, ctx.Err()
	}
}

func (a *Analyzer) parseVerificationResponse(response string) (VerificationStatus, string, string) {
	lines := strings.Split(response, "\n")
	var status VerificationStatus = StatusUnknown
	var evidence string
	var sourceURL string

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
		} else if strings.HasPrefix(upper, "SOURCE:") {
			sourceURL = strings.TrimSpace(strings.TrimPrefix(line, "SOURCE:"))
			if sourceURL == "" || sourceURL == "NO SOURCE FOUND" {
				sourceURL = ""
			}
		}
	}

	if evidence == "" {
		evidence = "Unable to determine"
	}

	return status, evidence, sourceURL
}
