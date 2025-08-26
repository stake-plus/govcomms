package research

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Researcher struct {
	apiKey     string
	tempDir    string
	httpClient *http.Client
}

func NewResearcher(apiKey, tempDir string) *Researcher {
	return &Researcher{
		apiKey:  apiKey,
		tempDir: tempDir,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

func (r *Researcher) ExtractTopClaims(ctx context.Context, network string, refID uint32) ([]Claim, int, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, 0, err
	}

	// Truncate content if too long to leave room for response
	maxContentLength := 10000
	if len(content) > maxContentLength {
		content = content[:maxContentLength] + "\n\n[Content truncated for analysis]"
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

	// Enable web search by including tools
	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "Extract and prioritize verifiable claims. Output valid JSON only."},
			{"role": "user", "content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content)},
		},
		"temperature":           1,
		"max_completion_tokens": 4000,
		"tools": []map[string]interface{}{
			{
				"type": "web_search",
			},
		},
		"tool_choice": "auto",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	log.Printf("Extracting top claims from proposal (content length: %d chars)", len(content))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI API error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return nil, 0, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, 0, err
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return []Claim{}, 0, nil
	}

	var claimsResponse struct {
		TotalClaims int     `json:"total_claims"`
		TopClaims   []Claim `json:"top_claims"`
	}

	responseContent := strings.TrimSpace(result.Choices[0].Message.Content)
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

func (r *Researcher) VerifySingleClaimWithContext(ctx context.Context, claim Claim) VerificationResult {
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

	// Enable web search with tools parameter
	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature":           1,
		"max_completion_tokens": 500,
		"tools": []map[string]interface{}{
			{
				"type": "web_search",
			},
		},
		"tool_choice": "auto",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to create verification request",
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to create verification request",
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Verification request failed",
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to read response",
		}
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI verification error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "API error",
		}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Failed to parse response",
		}
	}

	response := result.Choices[0].Message.Content
	status, evidence := r.parseVerificationResponse(response)

	return VerificationResult{
		Claim:    claim.Claim,
		Status:   status,
		Evidence: evidence,
	}
}

func (r *Researcher) ExtractClaims(ctx context.Context, network string, refID uint32) ([]Claim, error) {
	claims, _, err := r.ExtractTopClaims(ctx, network, refID)
	return claims, err
}

func (r *Researcher) VerifyClaims(ctx context.Context, claims []Claim) ([]VerificationResult, error) {
	var wg sync.WaitGroup
	results := make([]VerificationResult, len(claims))
	semaphore := make(chan struct{}, 3)

	log.Printf("Starting verification of %d claims", len(claims))

	for i, claim := range claims {
		select {
		case <-ctx.Done():
			log.Printf("Context cancelled during claim verification")
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
			result := r.VerifySingleClaimWithContext(ctx, c)
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
		log.Printf("All claims verified successfully")
		return results, nil
	case <-ctx.Done():
		log.Printf("Context timeout during claim verification")
		return results, ctx.Err()
	}
}

func (r *Researcher) ExtractTeamMembers(ctx context.Context, network string, refID uint32) ([]TeamMember, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, err
	}

	maxContentLength := 10000
	if len(content) > maxContentLength {
		content = content[:maxContentLength] + "\n\n[Content truncated]"
	}

	prompt := `Extract all team members mentioned in this proposal with their roles and social profiles.

Look for:
- Names of people working on the project
- Their roles or responsibilities
- GitHub profiles
- Twitter/X profiles
- LinkedIn profiles

Respond with JSON array only:
[
  {"name": "John Doe", "role": "Lead Developer", "github": "https://github.com/johndoe", "twitter": "", "linkedin": ""}
]`

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "Extract team member information. Output valid JSON array only."},
			{"role": "user", "content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content)},
		},
		"temperature":           1,
		"max_completion_tokens": 2000,
		"tools": []map[string]interface{}{
			{
				"type": "web_search",
			},
		},
		"tool_choice": "auto",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	log.Printf("Making request to OpenAI for team extraction")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI API error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return []TeamMember{}, nil
	}

	var members []TeamMember
	responseContent := strings.TrimSpace(result.Choices[0].Message.Content)

	if err := json.Unmarshal([]byte(responseContent), &members); err != nil {
		// Try to extract JSON array if embedded
		startIdx := strings.Index(responseContent, "[")
		endIdx := strings.LastIndex(responseContent, "]")
		if startIdx >= 0 && endIdx > startIdx {
			jsonStr := responseContent[startIdx : endIdx+1]
			if err := json.Unmarshal([]byte(jsonStr), &members); err != nil {
				return []TeamMember{}, nil
			}
		} else {
			return []TeamMember{}, nil
		}
	}

	log.Printf("Successfully extracted %d team members", len(members))
	return members, nil
}

func (r *Researcher) AnalyzeTeamMembers(ctx context.Context, members []TeamMember) ([]TeamAnalysisResult, error) {
	var wg sync.WaitGroup
	results := make([]TeamAnalysisResult, len(members))
	semaphore := make(chan struct{}, 3)

	log.Printf("Starting analysis of %d team members", len(members))

	for i, member := range members {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(index int, m TeamMember) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[index] = TeamAnalysisResult{
					Name:            m.Name,
					Role:            m.Role,
					IsReal:          false,
					HasStatedSkills: false,
					Capability:      "Analysis cancelled due to timeout",
				}
				return
			}

			log.Printf("Analyzing team member %d: %s", index+1, m.Name)
			result := r.analyzeSingleMember(ctx, m)
			results[index] = result
		}(i, member)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("All team members analyzed successfully")
		return results, nil
	case <-ctx.Done():
		log.Printf("Context timeout during team analysis")
		return results, ctx.Err()
	}
}

func (r *Researcher) analyzeSingleMember(ctx context.Context, member TeamMember) TeamAnalysisResult {
	profileInfo := ""
	if member.GitHub != "" {
		profileInfo += fmt.Sprintf("\nGitHub: %s", member.GitHub)
	}
	if member.Twitter != "" {
		profileInfo += fmt.Sprintf("\nTwitter: %s", member.Twitter)
	}
	if member.LinkedIn != "" {
		profileInfo += fmt.Sprintf("\nLinkedIn: %s", member.LinkedIn)
	}

	prompt := fmt.Sprintf(`You are analyzing a team member for a blockchain project proposal. Use web search to verify:

Name: %s
Role: %s%s

Tasks:
1. Verify if this is a real person (check profiles, activity, history)
2. Verify if they have the skills for their stated role
3. Assess their capability for blockchain/Web3 development

Respond with EXACTLY this format:
IS_REAL: [true/false]
HAS_SKILLS: [true/false]
CAPABILITY: [One sentence assessment of their capability for this project]`, member.Name, member.Role, profileInfo)

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature":           1,
		"max_completion_tokens": 500,
		"tools": []map[string]interface{}{
			{
				"type": "web_search",
			},
		},
		"tool_choice": "auto",
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze",
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze",
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Request failed",
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to read response",
		}
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI team analysis error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "API error",
		}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil || len(result.Choices) == 0 {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to parse response",
		}
	}

	response := result.Choices[0].Message.Content
	return r.parseTeamAnalysisResponse(member, response)
}

func (r *Researcher) parseVerificationResponse(response string) (VerificationStatus, string) {
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

func (r *Researcher) parseTeamAnalysisResponse(member TeamMember, response string) TeamAnalysisResult {
	lines := strings.Split(response, "\n")
	result := TeamAnalysisResult{
		Name: member.Name,
		Role: member.Role,
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)

		if strings.HasPrefix(upper, "IS_REAL:") {
			value := strings.TrimSpace(strings.TrimPrefix(upper, "IS_REAL:"))
			result.IsReal = value == "TRUE"
		} else if strings.HasPrefix(upper, "HAS_SKILLS:") {
			value := strings.TrimSpace(strings.TrimPrefix(upper, "HAS_SKILLS:"))
			result.HasStatedSkills = value == "TRUE"
		} else if strings.HasPrefix(upper, "CAPABILITY:") {
			result.Capability = strings.TrimSpace(strings.TrimPrefix(line, "CAPABILITY:"))
			if result.Capability == "" {
				result.Capability = strings.TrimSpace(strings.TrimPrefix(upper, "CAPABILITY:"))
			}
		}
	}

	if result.Capability == "" {
		result.Capability = "Unable to assess"
	}

	return result
}

func (r *Researcher) getProposalContent(network string, refID uint32) (string, error) {
	cacheFile := r.getCacheFilePath(network, refID)
	content, err := os.ReadFile(cacheFile)
	if err != nil {
		return "", fmt.Errorf("proposal content not found")
	}
	return string(content), nil
}

func (r *Researcher) getCacheFilePath(network string, refID uint32) string {
	hash := md5.Sum([]byte(fmt.Sprintf("%s-%d", network, refID)))
	filename := fmt.Sprintf("%s-%d-%s.txt", network, refID, hex.EncodeToString(hash[:8]))
	return filepath.Join(r.tempDir, filename)
}
