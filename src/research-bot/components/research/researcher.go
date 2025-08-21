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

type VerificationStatus string

const (
	StatusVerified   VerificationStatus = "Verified"
	StatusUnverified VerificationStatus = "Not Verified"
	StatusFailed     VerificationStatus = "Failed to Verify"
)

type VerificationResult struct {
	Claim    string
	Status   VerificationStatus
	Evidence string
	URL      string
}

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
			Timeout: 300 * time.Second,
		},
	}
}

func (r *Researcher) VerifyClaims(network string, refID uint32) ([]VerificationResult, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, err
	}

	claims, err := r.extractClaims(content)
	if err != nil {
		return nil, err
	}

	if len(claims) == 0 {
		return []VerificationResult{}, nil
	}

	return r.verifyClaims(claims)
}

func (r *Researcher) ResearchProponent(network string, refID uint32) ([]VerificationResult, error) {
	content, err := r.getProposalContent(network, refID)
	if err != nil {
		return nil, err
	}

	proponentInfo, err := r.extractProponentInfo(content)
	if err != nil {
		return nil, err
	}

	if len(proponentInfo) == 0 {
		return []VerificationResult{}, nil
	}

	return r.verifyProponentInfo(proponentInfo)
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

type Claim struct {
	Claim string `json:"claim"`
	URL   string `json:"verification_url"`
}

func (r *Researcher) extractClaims(content string) ([]Claim, error) {
	prompt := `Analyze the following proposal and extract all verifiable claims. Focus on:
1. Numerical claims (views, comments, engagement metrics)
2. Platform statistics (YouTube, Twitter, etc.)
3. Past performance claims
4. Deliverable claims
5. Timeline claims
6. Budget/cost claims

For each claim, provide the URL where it can be verified if mentioned.

Respond with a JSON array of objects with "claim" and "verification_url" fields.
Only include claims that can potentially be verified through online sources.`

	systemPrompt := "You are a claim extraction expert. Extract only factual, verifiable claims from proposals."

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content),
			},
		},
		"temperature":           0.1,
		"max_completion_tokens": 2000,
		"response_format":       map[string]string{"type": "json_object"},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	var claimsResponse struct {
		Claims []Claim `json:"claims"`
	}

	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &claimsResponse); err != nil {
		return nil, err
	}

	return claimsResponse.Claims, nil
}

func (r *Researcher) verifyClaims(claims []Claim) ([]VerificationResult, error) {
	var wg sync.WaitGroup
	results := make([]VerificationResult, len(claims))

	semaphore := make(chan struct{}, 3)

	for i, claim := range claims {
		wg.Add(1)
		go func(index int, c Claim) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := r.verifySingleClaim(c)
			results[index] = result
		}(i, claim)
	}

	wg.Wait()
	return results, nil
}

func (r *Researcher) verifySingleClaim(claim Claim) VerificationResult {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Verify the following claim: "%s"
URL to check (if provided): %s

Instructions:
1. If a URL is provided, verify the claim against that specific source
2. If no URL provided, search for reliable sources to verify
3. Check for exact numbers, dates, and facts
4. Provide specific evidence for or against the claim

Respond with:
- Status: "Verified", "Not Verified", or "Failed to Verify"
- Evidence: Brief explanation with specific details found`, claim.Claim, claim.URL)

	reqBody := map[string]interface{}{
		"model": "o3-deep-research",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature":           0.1,
		"max_completion_tokens": 1000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Error marshaling request for claim '%s': %v", claim.Claim, err)
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "Failed to create verification request",
			URL:      claim.URL,
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Error creating request for claim '%s': %v", claim.Claim, err)
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "Failed to create verification request",
			URL:      claim.URL,
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		log.Printf("Error verifying claim '%s': %v", claim.Claim, err)
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "Verification request failed",
			URL:      claim.URL,
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading response for claim '%s': %v", claim.Claim, err)
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "Failed to read verification response",
			URL:      claim.URL,
		}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("Error parsing response for claim '%s': %v", claim.Claim, err)
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "Failed to parse verification response",
			URL:      claim.URL,
		}
	}

	if len(result.Choices) == 0 {
		return VerificationResult{
			Claim:    claim.Claim,
			Status:   StatusFailed,
			Evidence: "No verification response received",
			URL:      claim.URL,
		}
	}

	response := result.Choices[0].Message.Content
	status, evidence := r.parseVerificationResponse(response)

	return VerificationResult{
		Claim:    claim.Claim,
		Status:   status,
		Evidence: evidence,
		URL:      claim.URL,
	}
}

func (r *Researcher) parseVerificationResponse(response string) (VerificationStatus, string) {
	lines := strings.Split(response, "\n")
	var status VerificationStatus = StatusFailed
	var evidence string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Status:") {
			statusStr := strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
			statusStr = strings.Trim(statusStr, `"`)

			switch strings.ToLower(statusStr) {
			case "verified":
				status = StatusVerified
			case "not verified":
				status = StatusUnverified
			default:
				status = StatusFailed
			}
		} else if strings.HasPrefix(line, "Evidence:") {
			evidence = strings.TrimSpace(strings.TrimPrefix(line, "Evidence:"))
		}
	}

	if evidence == "" {
		evidence = strings.TrimSpace(response)
		if len(evidence) > 200 {
			evidence = evidence[:197] + "..."
		}
	}

	return status, evidence
}

type ProponentInfo struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Info string `json:"info"`
}

func (r *Researcher) extractProponentInfo(content string) ([]ProponentInfo, error) {
	prompt := `Extract all proponent/team member professional information from this proposal. Look for:
1. GitHub profiles and repositories
2. LinkedIn profiles
3. Previous work/projects mentioned
4. Team member backgrounds
5. Company/organization information
6. Professional experience claims

Respond with a JSON array of objects with "type" (github/linkedin/experience/etc), "url", and "info" fields.`

	systemPrompt := "You are an expert at extracting professional information for verification."

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": fmt.Sprintf("%s\n\nProposal:\n%s", prompt, content),
			},
		},
		"temperature":           0.1,
		"max_completion_tokens": 2000,
		"response_format":       map[string]string{"type": "json_object"},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	var infoResponse struct {
		ProponentInfo []ProponentInfo `json:"proponent_info"`
	}

	if err := json.Unmarshal([]byte(result.Choices[0].Message.Content), &infoResponse); err != nil {
		return nil, err
	}

	return infoResponse.ProponentInfo, nil
}

func (r *Researcher) verifyProponentInfo(infos []ProponentInfo) ([]VerificationResult, error) {
	var wg sync.WaitGroup
	results := make([]VerificationResult, len(infos))

	semaphore := make(chan struct{}, 3)

	for i, info := range infos {
		wg.Add(1)
		go func(index int, pi ProponentInfo) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result := r.verifyProponentSingle(pi)
			results[index] = result
		}(i, info)
	}

	wg.Wait()
	return results, nil
}

func (r *Researcher) verifyProponentSingle(info ProponentInfo) VerificationResult {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(`Verify the following professional information:
Type: %s
URL: %s
Information: %s

Instructions:
1. If GitHub: Check repositories, contributions, code quality, activity
2. If LinkedIn: Verify professional experience, skills, connections
3. If experience claim: Verify through available sources
4. Look for red flags or inconsistencies
5. Assess credibility and relevance to the proposal

Respond with:
- Status: "Verified", "Not Verified", or "Failed to Verify"
- Evidence: Specific findings about the proponent's credentials`, info.Type, info.URL, info.Info)

	reqBody := map[string]interface{}{
		"model": "o3-deep-research",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature":           0.1,
		"max_completion_tokens": 1000,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "Failed to create verification request",
			URL:      info.URL,
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "Failed to create verification request",
			URL:      info.URL,
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "Verification request failed",
			URL:      info.URL,
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "Failed to read verification response",
			URL:      info.URL,
		}
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "Failed to parse verification response",
			URL:      info.URL,
		}
	}

	if len(result.Choices) == 0 {
		return VerificationResult{
			Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
			Status:   StatusFailed,
			Evidence: "No verification response received",
			URL:      info.URL,
		}
	}

	response := result.Choices[0].Message.Content
	status, evidence := r.parseVerificationResponse(response)

	return VerificationResult{
		Claim:    fmt.Sprintf("%s: %s", info.Type, info.Info),
		Status:   status,
		Evidence: evidence,
		URL:      info.URL,
	}
}
