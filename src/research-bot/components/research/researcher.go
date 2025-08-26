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

func (r *Researcher) ExtractClaims(ctx context.Context, network string, refID uint32) ([]Claim, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, err
	}

	prompt := `Analyze this proposal and extract ALL verifiable claims about historical deliverables, performance metrics, and factual statements that can be verified through web search.

Focus on:
- Past project completions and deliverables
- GitHub activity, commits, repositories
- Social media metrics (followers, views, engagement)
- Previous grants or funding received
- Specific numerical claims
- Timeline claims about past events
- Published work or documentation

Respond with JSON array of claims:
[
  {"claim": "Delivered 5 educational videos with 100k+ views", "category": "deliverables"},
  {"claim": "GitHub repository has 500+ commits", "category": "development"}
]`

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "Extract verifiable claims from proposals. Output valid JSON only."},
			{"role": "user", "content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content)},
		},
		"temperature":           1,
		"max_completion_tokens": 20000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal request body: %v", err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	log.Printf("Making request to OpenAI for claim extraction (proposal length: %d chars)", len(content))

	resp, err := r.httpClient.Do(req)
	if err != nil {
		log.Printf("HTTP request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v", err)
		return nil, err
	}

	log.Printf("OpenAI response status: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI API error - Status: %d, Body: %s", resp.StatusCode, string(body))

		// Try to parse error response
		var errorResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}

		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI API error: %s (type: %s, code: %s)",
				errorResp.Error.Message, errorResp.Error.Type, errorResp.Error.Code)
		}

		return nil, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("Failed to unmarshal response: %v, body: %s", err, string(body))
		return nil, err
	}

	if len(result.Choices) == 0 {
		log.Printf("No choices in OpenAI response. Full response: %s", string(body))
		return nil, fmt.Errorf("no response from OpenAI")
	}

	log.Printf("OpenAI tokens used - Prompt: %d, Completion: %d, Total: %d",
		result.Usage.PromptTokens, result.Usage.CompletionTokens, result.Usage.TotalTokens)

	responseContent := result.Choices[0].Message.Content
	log.Printf("OpenAI response content length: %d chars", len(responseContent))

	var claims []Claim
	if err := json.Unmarshal([]byte(responseContent), &claims); err != nil {
		log.Printf("Failed to parse claims JSON: %v\nContent: %s", err, responseContent)
		return nil, fmt.Errorf("failed to parse claims: %w", err)
	}

	log.Printf("Successfully extracted %d claims", len(claims))
	return claims, nil
}

func (r *Researcher) VerifyClaims(ctx context.Context, claims []Claim) ([]VerificationResult, error) {
	var wg sync.WaitGroup
	results := make([]VerificationResult, len(claims))
	semaphore := make(chan struct{}, 3) // Max 3 concurrent verifications

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
			result := r.verifySingleClaim(ctx, c)
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

func (r *Researcher) verifySingleClaim(ctx context.Context, claim Claim) VerificationResult {
	prompt := fmt.Sprintf(`You are a verification detective. Your task is to verify this specific claim using web search:

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

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature":           1,
		"max_completion_tokens": 500,
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
			Evidence: "Failed to read verification response",
		}
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI verification error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusUnknown,
			Evidence: "Verification API error",
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
			Evidence: "Failed to parse verification response",
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

func (r *Researcher) ExtractTeamMembers(ctx context.Context, network string, refID uint32) ([]TeamMember, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, err
	}

	prompt := `Extract all team members mentioned in this proposal with their roles and social profiles.

Look for:
- Names of people working on the project
- Their roles or responsibilities
- GitHub profiles
- Twitter/X profiles
- LinkedIn profiles
- Any other professional links

Respond with JSON array:
[
  {"name": "John Doe", "role": "Lead Developer", "github": "https://github.com/johndoe", "twitter": "", "linkedin": ""}
]`

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{"role": "system", "content": "Extract team member information. Output valid JSON only."},
			{"role": "user", "content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content)},
		},
		"temperature":           1,
		"max_completion_tokens": 20000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal team extraction request: %v", err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Failed to create team extraction request: %v", err)
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	log.Printf("Making request to OpenAI for team extraction")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		log.Printf("Team extraction HTTP request failed: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read team extraction response: %v", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI team extraction error - Status: %d, Body: %s", resp.StatusCode, string(body))

		var errorResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}

		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error.Message != "" {
			return nil, fmt.Errorf("OpenAI API error: %s", errorResp.Error.Message)
		}

		return nil, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("Failed to unmarshal team extraction response: %v", err)
		return nil, err
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	var members []TeamMember
	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &members); err != nil {
		log.Printf("Failed to parse team members JSON: %s", result.Choices[0].Message.Content)
		return nil, fmt.Errorf("failed to parse team members: %w", err)
	}

	log.Printf("Successfully extracted %d team members", len(members))
	return members, nil
}

func (r *Researcher) AnalyzeTeamMembers(ctx context.Context, members []TeamMember) ([]TeamAnalysisResult, error) {
	var wg sync.WaitGroup
	results := make([]TeamAnalysisResult, len(members))
	semaphore := make(chan struct{}, 3) // Max 3 concurrent analyses

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
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze team member",
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze team member",
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
			Capability:      "Analysis request failed",
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
			Capability:      "Failed to read analysis response",
		}
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("OpenAI team analysis error - Status: %d, Body: %s", resp.StatusCode, string(body))
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Analysis API error",
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
			Capability:      "Failed to parse analysis response",
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
		evidence = "Unable to determine verification details"
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
		result.Capability = "Unable to assess capability"
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
