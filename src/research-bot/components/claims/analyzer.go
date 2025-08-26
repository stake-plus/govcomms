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

	prompt := `Analyze this proposal and identify ALL verifiable claims about deliverables, metrics, and achievements.

First, count the TOTAL number of verifiable claims in the proposal.

Then select the 10 MOST IMPORTANT claims to verify based on:
- Financial impact (budget items, costs, payments)
- Deliverable claims (what was actually produced)
- Performance metrics (views, engagement, participation)
- Team credentials and experience
- Previous work or grants

Respond with JSON:
{
  "total_claims": 25,
  "top_claims": [
    {"claim": "Requested 1,625 DOT total funding", "category": "financial"},
    {"claim": "Delivered 41 live broadcasts totaling 57 hours", "category": "deliverables"},
    {"claim": "Reached 11,950 cumulative views across platforms", "category": "metrics"}
  ]
}`

	request := openai.ChatRequest{
		Model: "gpt-5-mini",
		Messages: []openai.Message{
			{Role: "system", Content: "Extract and prioritize verifiable claims. Output valid JSON only."},
			{Role: "user", Content: fmt.Sprintf("%s\n\nProposal:\n%s", prompt, proposalContent)},
		},
		Temperature:         1,
		MaxCompletionTokens: 25000,
	}

	log.Printf("Extracting top claims from proposal (content length: %d chars)", len(proposalContent))

	response, err := a.client.CreateChatCompletion(ctx, request)
	if err != nil {
		return nil, 0, err
	}

	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return []Claim{}, 0, nil
	}

	var claimsResponse ClaimsResponse
	responseContent := strings.TrimSpace(response.Choices[0].Message.Content)

	if err := json.Unmarshal([]byte(responseContent), &claimsResponse); err != nil {
		// Try to extract JSON if embedded
		startIdx := strings.Index(responseContent, "{")
		endIdx := strings.LastIndex(responseContent, "}")
		if startIdx >= 0 && endIdx > startIdx {
			jsonStr := responseContent[startIdx : endIdx+1]
			if err := json.Unmarshal([]byte(jsonStr), &claimsResponse); err != nil {
				log.Printf("Failed to parse claims response: %v", err)
				return []Claim{}, 0, nil
			}
		} else {
			return []Claim{}, 0, nil
		}
	}

	log.Printf("Found %d total claims, returning top %d for verification",
		claimsResponse.TotalClaims, len(claimsResponse.TopClaims))

	return claimsResponse.TopClaims, claimsResponse.TotalClaims, nil
}

func (a *Analyzer) VerifySingleClaim(ctx context.Context, claim Claim) VerificationResult {
	prompt := fmt.Sprintf(`You are a verification detective. Use web search to verify this specific claim:

Claim: "%s"
Category: %s

Instructions:
1. Search the web for evidence supporting or refuting this claim
2. Look for official sources, GitHub repos, social media profiles, documentation
3. Verify specific numbers, dates, and facts
4. Be skeptical and thorough

Respond with EXACTLY this format:
STATUS: [Valid/Rejected/Unknown]
EVIDENCE: [One sentence explanation with specific details found]`, claim.Claim, claim.Category)

	request := openai.ChatRequest{
		Model: "gpt-5-mini",
		Messages: []openai.Message{
			{Role: "user", Content: prompt},
		},
		Temperature:         1,
		MaxCompletionTokens: 25000,
	}

	response, err := a.client.CreateChatCompletionWithWebSearch(ctx, request)
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to verify",
		}
	}

	if len(response.Choices) == 0 {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "No response",
		}
	}

	status, evidence := a.parseVerificationResponse(response.Choices[0].Message.Content)
	return VerificationResult{
		Claim:    claim.Claim,
		Status:   status,
		Evidence: evidence,
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

func (a *Analyzer) parseVerificationResponse(response string) (VerificationStatus, string) {
	lines := strings.Split(response, "\n")
	var status VerificationStatus = StatusUnknown
	var evidence string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(line), "STATUS:") {
			statusStr := strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(line), "STATUS:"))
			switch statusStr {
			case "VALID":
				status = StatusValid
			case "REJECTED":
				status = StatusRejected
			default:
				status = StatusUnknown
			}
		} else if strings.HasPrefix(strings.ToUpper(line), "EVIDENCE:") {
			evidence = strings.TrimSpace(strings.TrimPrefix(line, "EVIDENCE:"))
			if evidence == "" {
				evidence = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(line), "EVIDENCE:"))
			}
		}
	}

	if evidence == "" {
		evidence = "Unable to determine"
	}

	return status, evidence
}
