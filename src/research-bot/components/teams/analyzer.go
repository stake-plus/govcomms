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

	request := openai.ChatRequest{
		Model: "gpt-5-mini",
		Messages: []openai.Message{
			{Role: "system", Content: "Extract team member information. Output valid JSON array only."},
			{Role: "user", Content: fmt.Sprintf("%s\n\nProposal:\n%s", prompt, proposalContent)},
		},
		Temperature:         1,
		MaxCompletionTokens: 2000,
	}

	log.Printf("Extracting team members from proposal")

	response, err := a.client.CreateChatCompletionWithWebSearch(ctx, request)
	if err != nil {
		return nil, err
	}

	if len(response.Choices) == 0 || response.Choices[0].Message.Content == "" {
		return []TeamMember{}, nil
	}

	var members []TeamMember
	responseContent := strings.TrimSpace(response.Choices[0].Message.Content)

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

	request := openai.ChatRequest{
		Model: "gpt-5-mini",
		Messages: []openai.Message{
			{Role: "user", Content: prompt},
		},
		Temperature:         1,
		MaxCompletionTokens: 500,
	}

	response, err := a.client.CreateChatCompletionWithWebSearch(ctx, request)
	if err != nil {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "Failed to analyze",
		}
	}

	if len(response.Choices) == 0 {
		return TeamAnalysisResult{
			Name:            member.Name,
			Role:            member.Role,
			IsReal:          false,
			HasStatedSkills: false,
			Capability:      "No response",
		}
	}

	return a.parseTeamAnalysisResponse(member, response.Choices[0].Message.Content)
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
