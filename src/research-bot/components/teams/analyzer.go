package teams

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

func (a *Analyzer) ExtractTeamMembers(ctx context.Context, proposalContent string) ([]TeamMember, error) {
	maxContentLength := 10000
	if len(proposalContent) > maxContentLength {
		proposalContent = proposalContent[:maxContentLength] + "\n\n[Content truncated]"
	}

	prompt := fmt.Sprintf(`Extract team members from this proposal. Focus on finding their verifiable online profiles.

Look for:
- Full names of team members
- Their roles in the project
- GitHub usernames or profile URLs (look for github.com links or @mentions)
- LinkedIn profile URLs
- Twitter/X handles or profile URLs
- Any other professional links mentioned

Extract URLs exactly as they appear in the proposal. If only a username is mentioned, construct the likely URL.

Respond with JSON array:
[
  {
    "name": "César Escobedo",
    "role": "Founder/Lead",
    "github": "https://github.com/cesarescobedo",
    "twitter": "https://twitter.com/cesarescobedo",
    "linkedin": ""
  }
]

Only include team members where you find at least a name and role. Include empty strings for missing profile URLs.

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
			result := a.analyzeSingleMember(ctx, m)
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
		return results, nil
	case <-ctx.Done():
		return results, ctx.Err()
	}
}

func (a *Analyzer) analyzeSingleMember(ctx context.Context, member TeamMember) TeamAnalysisResult {
	prompt := fmt.Sprintf(`Verify this team member for a blockchain/Polkadot project using web search.

Name: %s
Role: %s`, member.Name, member.Role)

	// Add profile URLs if provided
	hasProfiles := false
	if member.GitHub != "" {
		prompt += fmt.Sprintf("\nGitHub: %s", member.GitHub)
		hasProfiles = true
	}
	if member.Twitter != "" {
		prompt += fmt.Sprintf("\nTwitter: %s", member.Twitter)
		hasProfiles = true
	}
	if member.LinkedIn != "" {
		prompt += fmt.Sprintf("\nLinkedIn: %s", member.LinkedIn)
		hasProfiles = true
	}

	prompt += `

Tasks:
1. Verify if this is a real person:`

	if hasProfiles {
		prompt += `
   - Check if the provided profile URLs are valid and active
   - Verify the profiles belong to the named person
   - Check for consistent identity across profiles`
	} else {
		prompt += `
   - Search for this person online
   - Look for any professional profiles or mentions`
	}

	prompt += `
2. Verify their skills for the stated role:
   - For developers: Check GitHub contributions, repositories, commit history
   - For designers: Look for portfolio or design work
   - For community managers: Check social media activity and engagement
   - Look for blockchain/Web3/Polkadot experience specifically

3. Assess capability for this project based on evidence found

Respond with EXACTLY this format:
IS_REAL: [true/false]
HAS_SKILLS: [true/false]
CAPABILITY: [One detailed sentence about their verified experience and suitability]`

	response, err := a.client.CreateResponseWithWebSearch(ctx, prompt)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze",
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
		}
	}

	return a.parseTeamAnalysisResponse(member, responseText)
}

func (a *Analyzer) parseTeamAnalysisResponse(member TeamMember, response string) TeamAnalysisResult {
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
