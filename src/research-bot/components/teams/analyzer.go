package teams

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

func (a *Analyzer) ExtractTeamMembers(ctx context.Context, proposalContent string) ([]TeamMember, error) {
	maxContentLength := 10000
	if len(proposalContent) > maxContentLength {
		proposalContent = proposalContent[:maxContentLength] + "\n\n[Content truncated]"
	}

	prompt := fmt.Sprintf(`Extract team members from this proposal. Focus on finding ALL their verifiable online profiles.

Look for:
- Full names of team members
- Their roles in the project
- ALL GitHub usernames or profile URLs (can be multiple per person)
- ALL LinkedIn profile URLs
- ALL Twitter/X handles or profile URLs
- Any other professional links mentioned (personal sites, etc)

Extract URLs exactly as they appear. If only usernames are mentioned, construct likely URLs.
A person might have multiple profiles (e.g., personal and org GitHub accounts).

Respond with JSON array:
[
  {
    "name": "César Escobedo",
    "role": "Founder/Lead",
    "github": ["https://github.com/cesarescobedo", "https://github.com/cesare-dev"],
    "twitter": ["https://twitter.com/cesarescobedo"],
    "linkedin": ["https://linkedin.com/in/cesarescobedo"],
    "other": ["https://cesarescobedo.com"]
  }
]

Include empty arrays for missing profile types. Only include team members with at least a name and role.

Proposal:
%s`, proposalContent)

	response, err := a.client.CreateResponseNoSearch(ctx, prompt)
	if err != nil {
		return nil, err
	}

	responseText := response.GetText()
	if responseText == "" {
		return []TeamMember{}, nil
	}

	var members []TeamMember
	if err := json.Unmarshal([]byte(responseText), &members); err != nil {
		// Try to extract JSON array if embedded
		startIdx := strings.Index(responseText, "[")
		endIdx := strings.LastIndex(responseText, "]")
		if startIdx >= 0 && endIdx > startIdx {
			jsonStr := responseText[startIdx : endIdx+1]
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

func (a *Analyzer) AnalyzeTeamMembers(ctx context.Context, members []TeamMember) ([]TeamAnalysisResult, error) {
	results := make([]TeamAnalysisResult, len(members))
	semaphore := make(chan struct{}, 1) // 1 concurrent operation

	// Initial delay to let rate limits reset
	log.Printf("Waiting 5 seconds before starting team analysis...")
	select {
	case <-ctx.Done():
		return results, ctx.Err()
	case <-time.After(5 * time.Second):
	}

	// Process members one at a time
	for i := 0; i < len(members); i++ {
		memberCtx, memberCancel := context.WithTimeout(ctx, 5*time.Minute)

		var wg sync.WaitGroup

		select {
		case <-ctx.Done(): // Check parent context
			memberCancel()
			return results, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(index int, m TeamMember) {
			defer wg.Done()

			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-memberCtx.Done():
				results[index] = TeamAnalysisResult{
					Name:            m.Name,
					Role:            m.Role,
					IsReal:          false,
					HasStatedSkills: false,
					Capability:      "Analysis timeout",
					VerifiedURLs:    []string{},
				}
				return
			}

			log.Printf("Analyzing team member %d of %d: %s", index+1, len(members), m.Name)
			result := a.analyzeSingleMember(memberCtx, m)
			results[index] = result
		}(i, members[i])

		// Wait for this member to complete
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Member completed successfully
		case <-memberCtx.Done():
			// Member timeout
			log.Printf("Team member %d analysis timed out", i+1)
		}

		memberCancel()

		// Wait 10 seconds between each member to avoid rate limiting
		if i < len(members)-1 {
			log.Printf("Waiting 10 seconds before next team member...")
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(10 * time.Second):
			}
		}
	}

	return results, nil
}

func (a *Analyzer) analyzeSingleMember(ctx context.Context, member TeamMember) TeamAnalysisResult {
	prompt := fmt.Sprintf(`Verify this team member for a blockchain/Polkadot project using web search.

Name: %s
Role: %s`, member.Name, member.Role)

	// Add all profile URLs
	var allURLs []string

	if len(member.GitHub) > 0 {
		prompt += fmt.Sprintf("\nGitHub profiles: %s", strings.Join(member.GitHub, ", "))
		allURLs = append(allURLs, member.GitHub...)
	}
	if len(member.Twitter) > 0 {
		prompt += fmt.Sprintf("\nTwitter profiles: %s", strings.Join(member.Twitter, ", "))
		allURLs = append(allURLs, member.Twitter...)
	}
	if len(member.LinkedIn) > 0 {
		prompt += fmt.Sprintf("\nLinkedIn profiles: %s", strings.Join(member.LinkedIn, ", "))
		allURLs = append(allURLs, member.LinkedIn...)
	}
	if len(member.Other) > 0 {
		prompt += fmt.Sprintf("\nOther links: %s", strings.Join(member.Other, ", "))
		allURLs = append(allURLs, member.Other...)
	}

	prompt += `

Tasks:
1. Verify if this is a real person:
   - Check ALL provided profile URLs are valid and active
   - Verify the profiles belong to the named person
   - Check for consistent identity across profiles

2. Verify their skills for the stated role:
   - For developers: Check ALL GitHub accounts for contributions, repositories, commit history
   - For designers: Look for portfolio or design work
   - For community managers: Check social media activity and engagement
   - Look for blockchain/Web3/Polkadot experience specifically

3. List which URLs you successfully verified

Respond with EXACTLY this format:
IS_REAL: [true/false]
HAS_SKILLS: [true/false]
CAPABILITY: [One detailed sentence about their verified experience and suitability]
VERIFIED_URLS: [Comma-separated list of URLs that were successfully verified, or "None"]`

	response, err := a.client.CreateResponseWithWebSearchRetry(ctx, prompt)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze",
			VerifiedURLs:    []string{},
		}
	}

	responseText := response.GetText()
	if responseText == "" {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "No response",
			VerifiedURLs:    []string{},
		}
	}

	return a.parseTeamAnalysisResponse(member, responseText)
}

func (a *Analyzer) parseTeamAnalysisResponse(member TeamMember, response string) TeamAnalysisResult {
	lines := strings.Split(response, "\n")
	result := TeamAnalysisResult{
		Name:         member.Name,
		Role:         member.Role,
		VerifiedURLs: []string{},
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
		} else if strings.HasPrefix(upper, "VERIFIED_URLS:") {
			urlsStr := strings.TrimSpace(strings.TrimPrefix(line, "VERIFIED_URLS:"))
			if urlsStr != "" && !strings.EqualFold(urlsStr, "None") {
				// Split by comma and clean up
				parts := strings.Split(urlsStr, ",")
				for _, url := range parts {
					url = strings.TrimSpace(url)
					if url != "" && strings.HasPrefix(url, "http") {
						result.VerifiedURLs = append(result.VerifiedURLs, url)
					}
				}
			}
		}
	}

	if result.Capability == "" {
		result.Capability = "Unable to assess"
	}

	return result
}
