package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
)

type Analyzer struct{ client aicore.Client }

func NewAnalyzer(client aicore.Client) (*Analyzer, error) {
	if client == nil {
		return nil, fmt.Errorf("teams: ai client is nil")
	}
	return &Analyzer{client: client}, nil
}

func (a *Analyzer) ExtractTeamMembers(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool) ([]TeamMember, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
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

%s

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

Include empty arrays for missing profile types. Only include team members with at least a name and role.`, a.getProposalInstruction(network, refID, mcpTool != nil))

	responseText, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, err
	}
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

	// Initial delay to let rate limits reset
	log.Printf("Waiting 5 seconds before starting team analysis...")
	select {
	case <-ctx.Done():
		return results, ctx.Err()
	case <-time.After(5 * time.Second):
	}

	// Process members sequentially (one at a time)
	for i := 0; i < len(members); i++ {
		// Check parent context before processing each member
		select {
		case <-ctx.Done():
			// Mark remaining results as cancelled
			for j := i; j < len(members); j++ {
				results[j] = TeamAnalysisResult{
					Name:            members[j].Name,
					Role:            members[j].Role,
					IsReal:          false,
					HasStatedSkills: false,
					Capability:      "Analysis cancelled",
					VerifiedURLs:    []string{},
				}
			}
			return results, ctx.Err()
		default:
		}

		// Create a new context with timeout for this member
		memberCtx, memberCancel := context.WithTimeout(ctx, 3*time.Minute)
		defer memberCancel() // Ensure cleanup even on panic or early return

		log.Printf("Analyzing team member %d of %d: %s", i+1, len(members), members[i].Name)
		result := a.analyzeSingleMember(memberCtx, members[i])
		results[i] = result

		// Wait 5 seconds between each member to avoid rate limiting
		if i < len(members)-1 {
			log.Printf("Waiting 5 seconds before next team member...")
			select {
			case <-ctx.Done():
				// Mark remaining results as cancelled
				for j := i + 1; j < len(members); j++ {
					results[j] = TeamAnalysisResult{
						Name:            members[j].Name,
						Role:            members[j].Role,
						IsReal:          false,
						HasStatedSkills: false,
						Capability:      "Analysis cancelled",
						VerifiedURLs:    []string{},
					}
				}
				return results, ctx.Err()
			case <-time.After(5 * time.Second):
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

	responseText, err := a.client.Respond(ctx, prompt, []aicore.Tool{{Type: "web_search"}}, aicore.Options{})
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

// getProposalInstruction returns instructions for how to get proposal content
func (a *Analyzer) getProposalInstruction(network string, refID uint32, hasMCP bool) string {
	if hasMCP {
		slug := strings.ToLower(strings.TrimSpace(network))
		return fmt.Sprintf(`Use the fetch_referendum_data tool to retrieve metadata and full proposal content before extracting team members.
Metadata example: {"network":"%s","refId":%d,"resource":"metadata"}
Content example: {"network":"%s","refId":%d,"resource":"content"}
Request attachments when metadata lists files. Avoid repeating tool calls after you have the information you need.`, slug, refID, slug, refID)
	}
	return fmt.Sprintf("Network: %s, Referendum ID: %d", network, refID)
}
