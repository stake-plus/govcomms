package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/cache"
)

// Analyzer generates additional AI analysis sections for reports
type Analyzer struct {
	client aicore.Client
}

// NewAnalyzer creates a new report analyzer
func NewAnalyzer(client aicore.Client) (*Analyzer, error) {
	if client == nil {
		return nil, fmt.Errorf("reports: ai client is nil")
	}
	return &Analyzer{client: client}, nil
}

// AnalyzeFinancials extracts and analyzes financial information
func (a *Analyzer) AnalyzeFinancials(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData) (*FinancialAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	prompt := fmt.Sprintf(`Analyze the financial aspects of this blockchain governance proposal.

Extract and analyze:
1. Total funding amount requested
2. Budget breakdown by category (if provided)
3. Payment milestones and deliverables
4. Expected ROI or value proposition
5. Any financial concerns or red flags

%s

Respond with JSON:
{
  "totalAmount": "e.g., 50,000 DOT",
  "breakdown": [
    {
      "category": "Development",
      "amount": "30,000 DOT",
      "purpose": "Software development costs"
    }
  ],
  "milestones": [
    {
      "name": "Milestone 1",
      "amount": "25,000 DOT",
      "deliverable": "Complete MVP",
      "timeline": "3 months"
    }
  ],
  "roi": "Expected value or return on investment",
  "concerns": ["List any financial concerns"]
}`, a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var analysis FinancialAnalysis
	if err := a.extractJSON(response, &analysis); err != nil {
		log.Printf("reports: failed to parse financials JSON, using fallback: %v", err)
		// Fallback: try to extract basic info
		analysis = FinancialAnalysis{
			TotalAmount: "Not specified",
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// AnalyzeRisks performs risk assessment
func (a *Analyzer) AnalyzeRisks(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData, claims *cache.ClaimsData, teams *cache.TeamsData) (*RiskAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString(a.getProposalInstruction(network, refID, mcpTool != nil))
	contextBuilder.WriteString("\n\n")
	
	if summary != nil {
		contextBuilder.WriteString("\n\nSummary:\n")
		contextBuilder.WriteString(summary.Summary)
	}
	
	if claims != nil {
		contextBuilder.WriteString("\n\nClaims Analysis:\n")
		invalidCount := 0
		for _, claim := range claims.Results {
			if claim.Status == "Rejected" {
				invalidCount++
			}
		}
		contextBuilder.WriteString(fmt.Sprintf("Invalid Claims: %d", invalidCount))
	}
	
	if teams != nil {
		contextBuilder.WriteString("\n\nTeam Analysis:\n")
		unverifiedCount := 0
		for _, member := range teams.Members {
			if member.IsReal != nil && !*member.IsReal {
				unverifiedCount++
			}
		}
		contextBuilder.WriteString(fmt.Sprintf("Unverified Team Members: %d", unverifiedCount))
	}

	prompt := fmt.Sprintf(`Perform a comprehensive risk assessment for this blockchain governance proposal.

Analyze:
1. Technical Risks - Can the proposed technology be delivered?
2. Financial Risks - Are there concerns about budget or funding?
3. Execution Risks - Can the team deliver on promises?

For each risk, assess:
- Severity: Low/Medium/High
- Likelihood: Low/Medium/High
- Description: Detailed explanation

Provide an overall risk level: Low/Medium/High

Respond with JSON:
{
  "technicalRisks": [
    {
      "risk": "Technology may not be feasible",
      "severity": "High",
      "likelihood": "Medium",
      "description": "Detailed explanation"
    }
  ],
  "financialRisks": [...],
  "executionRisks": [...],
  "overallRisk": "Medium",
  "mitigation": ["Suggested mitigation strategies"]
}

Context:
%s`, contextBuilder.String())

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var rawAnalysis struct {
		TechnicalRisks []RiskItem `json:"technicalRisks"`
		FinancialRisks []RiskItem `json:"financialRisks"`
		ExecutionRisks []RiskItem `json:"executionRisks"`
		OverallRisk    string     `json:"overallRisk"`
		Mitigation     []string   `json:"mitigation"`
	}
	
	var analysis RiskAnalysis
	if err := a.extractJSON(response, &rawAnalysis); err != nil {
		log.Printf("reports: failed to parse risks JSON: %v", err)
		analysis = RiskAnalysis{
			OverallRisk: "Unknown",
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.TechnicalRisks = rawAnalysis.TechnicalRisks
		analysis.FinancialRisks = rawAnalysis.FinancialRisks
		analysis.ExecutionRisks = rawAnalysis.ExecutionRisks
		analysis.OverallRisk = rawAnalysis.OverallRisk
		analysis.Mitigation = rawAnalysis.Mitigation
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// AnalyzeTimeline assesses timeline feasibility
func (a *Analyzer) AnalyzeTimeline(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool) (*TimelineAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	prompt := fmt.Sprintf(`Analyze the proposed timeline for this blockchain governance proposal.

Extract:
1. Proposed timeline/delivery schedule
2. Assess if timeline is: Realistic/Unrealistic/Ambitious
3. Identify concerns about timeline
4. Provide recommendations

%s

Respond with JSON:
{
  "proposedTimeline": "Summary of proposed timeline",
  "feasibility": "Realistic/Unrealistic/Ambitious",
  "concerns": ["List timeline concerns"],
  "recommendations": ["Suggestions for timeline adjustments"]
}`, a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var analysis TimelineAnalysis
	if err := a.extractJSON(response, &analysis); err != nil {
		log.Printf("reports: failed to parse timeline JSON: %v", err)
		analysis = TimelineAnalysis{
			Feasibility: "Unknown",
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// AnalyzeGovernance assesses governance impact
func (a *Analyzer) AnalyzeGovernance(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool) (*GovernanceAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	prompt := fmt.Sprintf(`Analyze the governance impact of this proposal on the %s network.

Assess:
1. Impact Level: Low/Medium/High
2. Description of governance implications
3. Network effects (positive or negative)
4. Similar precedents or comparable proposals
5. Governance concerns

%s

Respond with JSON:
{
  "impact": "Low/Medium/High",
  "description": "Detailed impact analysis",
  "networkEffect": "How this affects the network",
  "precedents": ["Similar past proposals or examples"],
  "concerns": ["Governance-related concerns"]
}`, network, a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var analysis GovernanceAnalysis
	if err := a.extractJSON(response, &analysis); err != nil {
		log.Printf("reports: failed to parse governance JSON: %v", err)
		analysis = GovernanceAnalysis{
			Impact: "Unknown",
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// AnalyzePositive identifies positive aspects
func (a *Analyzer) AnalyzePositive(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData) (*PositiveAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString(a.getProposalInstruction(network, refID, mcpTool != nil))
	contextBuilder.WriteString("\n\n")
	
	if summary != nil {
		contextBuilder.WriteString("\n\nSummary:\n")
		contextBuilder.WriteString(summary.Summary)
		if len(summary.ValidClaims) > 0 {
			contextBuilder.WriteString("\n\nValid Claims:\n")
			for _, claim := range summary.ValidClaims {
				contextBuilder.WriteString(fmt.Sprintf("- %s\n", claim))
			}
		}
	}

	prompt := fmt.Sprintf(`Identify the positive aspects and strengths of this blockchain governance proposal.

Analyze:
1. Strengths - What are the proposal's strong points?
2. Opportunities - What opportunities does this create?
3. Value Proposition - What value does this deliver?
4. Innovation - What innovative aspects exist?

Respond with JSON:
{
  "strengths": ["List of strengths"],
  "opportunities": ["List of opportunities"],
  "valueProposition": "Overall value proposition",
  "innovation": ["Innovative aspects"]
}

Context:
%s`, contextBuilder.String())

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var analysis PositiveAnalysis
	if err := a.extractJSON(response, &analysis); err != nil {
		log.Printf("reports: failed to parse positive JSON: %v", err)
		analysis = PositiveAnalysis{
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// AnalyzeSteelMan performs steel manning (critical analysis)
func (a *Analyzer) AnalyzeSteelMan(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData, claims *cache.ClaimsData, teams *cache.TeamsData) (*SteelManAnalysis, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString(a.getProposalInstruction(network, refID, mcpTool != nil))
	contextBuilder.WriteString("\n\n")
	
	if summary != nil {
		contextBuilder.WriteString("\n\nSummary:\n")
		contextBuilder.WriteString(summary.Summary)
	}
	
	if claims != nil {
		contextBuilder.WriteString("\n\nInvalid Claims:\n")
		for _, claim := range claims.Results {
			if claim.Status == "Rejected" {
				contextBuilder.WriteString(fmt.Sprintf("- %s\n", claim.Claim))
			}
		}
	}
	
	if teams != nil {
		contextBuilder.WriteString("\n\nTeam Issues:\n")
		for _, member := range teams.Members {
			if member.IsReal != nil && !*member.IsReal {
				contextBuilder.WriteString(fmt.Sprintf("- %s: Not verified\n", member.Name))
			}
		}
	}

	prompt := fmt.Sprintf(`Perform a "steel manning" analysis - identify the weaknesses, concerns, and potential problems with this blockchain governance proposal.

This is a critical analysis to find what's wrong or concerning:
1. Concerns - What are the main concerns?
2. Weaknesses - What are the proposal's weaknesses?
3. Red Flags - What are serious warning signs?
4. Alternatives - What alternative approaches might be better?

Be thorough and critical. This helps identify potential issues.

Respond with JSON:
{
  "concerns": ["List of concerns"],
  "weaknesses": ["List of weaknesses"],
  "redFlags": ["Serious warning signs"],
  "alternatives": ["Alternative approaches"]
}

Context:
%s`, contextBuilder.String())

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var analysis SteelManAnalysis
	if err := a.extractJSON(response, &analysis); err != nil {
		log.Printf("reports: failed to parse steel man JSON: %v", err)
		analysis = SteelManAnalysis{
			GeneratedAt: time.Now(),
		}
	} else {
		analysis.GeneratedAt = time.Now()
	}

	return &analysis, nil
}

// GenerateRecommendations creates final recommendations
func (a *Analyzer) GenerateRecommendations(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData, financials *FinancialAnalysis, risks *RiskAnalysis, positive *PositiveAnalysis, steelMan *SteelManAnalysis) (*Recommendations, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString(a.getProposalInstruction(network, refID, mcpTool != nil))
	contextBuilder.WriteString("\n\nProposal Summary:\n")
	if summary != nil {
		contextBuilder.WriteString(summary.Summary)
	}
	
	if financials != nil {
		contextBuilder.WriteString(fmt.Sprintf("\n\nFinancials: %s", financials.TotalAmount))
		if len(financials.Concerns) > 0 {
			contextBuilder.WriteString(fmt.Sprintf("\nFinancial Concerns: %s", strings.Join(financials.Concerns, ", ")))
		}
	}
	
	if risks != nil {
		contextBuilder.WriteString(fmt.Sprintf("\n\nOverall Risk: %s", risks.OverallRisk))
	}
	
	if positive != nil && positive.ValueProposition != "" {
		contextBuilder.WriteString(fmt.Sprintf("\n\nValue: %s", positive.ValueProposition))
	}
	
	if steelMan != nil && len(steelMan.RedFlags) > 0 {
		contextBuilder.WriteString(fmt.Sprintf("\n\nRed Flags: %d", len(steelMan.RedFlags)))
	}

	prompt := fmt.Sprintf(`Based on all the analysis, provide final recommendations for this blockchain governance proposal.

Consider:
- Financial analysis
- Risk assessment
- Positive aspects
- Concerns and red flags
- Team capabilities
- Claims verification

Provide a verdict: Approve/Deny/Modify

Respond with JSON:
{
  "verdict": "Approve/Deny/Modify",
  "confidence": "High/Medium/Low",
  "reasoning": "Detailed reasoning for the verdict",
  "keyPoints": ["Key points supporting the recommendation"],
  "conditions": ["If Modify: list required modifications"]
}

Analysis Context:
%s`, contextBuilder.String())

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var recommendations Recommendations
	if err := a.extractJSON(response, &recommendations); err != nil {
		log.Printf("reports: failed to parse recommendations JSON: %v", err)
		recommendations = Recommendations{
			Verdict: "Unknown",
			Confidence: "Low",
			Reasoning: "Unable to generate recommendation",
			GeneratedAt: time.Now(),
		}
	} else {
		recommendations.GeneratedAt = time.Now()
	}

	return &recommendations, nil
}

// GenerateEnhancedContent creates enhanced background context, summary, and financials
func (a *Analyzer) GenerateEnhancedContent(ctx context.Context, network string, refID uint32, mcpTool *aicore.Tool, summary *cache.SummaryData, teams *cache.TeamsData, financials *FinancialAnalysis) (*EnhancedContent, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	prompt := fmt.Sprintf(`Generate enhanced content for a comprehensive referendum report. Provide:

1. Background Context (exactly 2 paragraphs, no more):
   - First paragraph: Background of the people/team behind this proposal
   - Second paragraph: Background of the idea/project and any other relevant context needed to understand this proposal
   
2. Referenda Summary (exactly 2 paragraphs, no more):
   - Everything needed to know about what they want us to vote on
   - Everything needed to make a good voting decision
   
3. Project Financials Detail (exactly 2 paragraphs, no more):
   - How much they're asking for now
   - How much they want in the future (if any)
   - If there will be any other associated or side projects

%s

Respond with JSON:
{
  "backgroundContext": "Two paragraphs about people and idea background",
  "referendaSummary": "Two paragraphs about what we're voting on",
  "financialsDetail": "Two paragraphs about current ask, future asks, side projects"
}`, a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var content EnhancedContent
	if err := a.extractJSON(response, &content); err != nil {
		log.Printf("reports: failed to parse enhanced content JSON: %v", err)
		// Fallback
		if summary != nil {
			content.BackgroundContext = summary.BackgroundContext
			content.ReferendaSummary = summary.Summary
		}
		if financials != nil {
			content.FinancialsDetail = fmt.Sprintf("Total requested: %s", financials.TotalAmount)
		}
	}
	content.GeneratedAt = time.Now()

	return &content, nil
}

// GenerateSectionNotes creates green/red box content for a section
func (a *Analyzer) GenerateSectionNotes(ctx context.Context, sectionName string, sectionContent string, positiveAnalysis *PositiveAnalysis, steelManAnalysis *SteelManAnalysis) (*SectionNotes, error) {
	var tools []aicore.Tool
	// Note: GenerateSectionNotes doesn't need proposal content, so no MCP tool needed
	prompt := fmt.Sprintf(`Analyze this section of a referendum report and identify noteworthy positive aspects and concerns.

Section: %s
Content: %s

Provide ONLY noteworthy items. If there's nothing noteworthy (positive or negative), return empty arrays.

Respond with JSON:
{
  "positive": ["Noteworthy positive aspects - only if truly noteworthy"],
  "concerns": ["Noteworthy concerns or problems - only if truly noteworthy"]
}

Do not include empty boxes. Only include items that are truly noteworthy.`, sectionName, sectionContent)

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var notes SectionNotes
	if err := a.extractJSON(response, &notes); err != nil {
		log.Printf("reports: failed to parse section notes JSON: %v", err)
		notes = SectionNotes{
			Positive: []string{},
			Concerns: []string{},
		}
	}

	return &notes, nil
}

// GenerateTeamMemberDetails creates enhanced team member information
func (a *Analyzer) GenerateTeamMemberDetails(ctx context.Context, member cache.TeamMemberData, network string, refID uint32, mcpTool *aicore.Tool) (*TeamMemberDetails, error) {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}
	var socialHandles strings.Builder
	socialHandles.WriteString("GitHub: ")
	if len(member.GitHub) > 0 {
		socialHandles.WriteString(strings.Join(member.GitHub, ", "))
	} else {
		socialHandles.WriteString("None")
	}
	socialHandles.WriteString("\nTwitter: ")
	if len(member.Twitter) > 0 {
		socialHandles.WriteString(strings.Join(member.Twitter, ", "))
	} else {
		socialHandles.WriteString("None")
	}
	socialHandles.WriteString("\nLinkedIn: ")
	if len(member.LinkedIn) > 0 {
		socialHandles.WriteString(strings.Join(member.LinkedIn, ", "))
	} else {
		socialHandles.WriteString("None")
	}
	socialHandles.WriteString("\nOther URLs: ")
	if len(member.Other) > 0 {
		socialHandles.WriteString(strings.Join(member.Other, ", "))
	} else {
		socialHandles.WriteString("None")
	}

	prompt := fmt.Sprintf(`Analyze this team member from a blockchain governance proposal and extract:

1. All social handles (Twitter, GitHub, Discord, Element, Email, LinkedIn, Facebook, Forum, YouTube, etc.)
2. Known skills and capabilities
3. Work history and background
4. Verified/confirmed positive aspects
5. Concerns or worries

Team Member: %s
Role: %s
Capability: %s
Social URLs: %s
Is Real Person: %v
Has Stated Skills: %v

Respond with JSON:
{
  "socialHandles": {
    "twitter": ["handles"],
    "github": ["handles"],
    "discord": ["handles"],
    "element": ["handles"],
    "email": ["emails"],
    "linkedin": ["urls"],
    "facebook": ["urls"],
    "forum": ["urls"],
    "youtube": ["urls"],
    "other": ["other urls"]
  },
  "skills": ["List of known skills"],
  "workHistory": "Detailed work history and background",
  "verified": ["Verified/confirmed positive aspects"],
  "concerns": ["Concerns or worries about this team member"]
}

%s`, member.Name, member.Role, member.Capability, socialHandles.String(),
		member.IsReal != nil && *member.IsReal,
		member.HasStatedSkills != nil && *member.HasStatedSkills,
		a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return nil, fmt.Errorf("AI response: %w", err)
	}

	var details TeamMemberDetails
	if err := a.extractJSON(response, &details); err != nil {
		log.Printf("reports: failed to parse team member details JSON: %v", err)
		// Fallback: use existing data
		details.SocialHandles = make(map[string][]string)
		details.SocialHandles["github"] = member.GitHub
		details.SocialHandles["twitter"] = member.Twitter
		details.SocialHandles["linkedin"] = member.LinkedIn
		details.SocialHandles["other"] = member.Other
		details.Skills = []string{}
		details.WorkHistory = member.Capability
		if member.IsReal != nil && *member.IsReal {
			details.Verified = []string{"Verified as real person"}
		}
		if member.HasStatedSkills != nil && !*member.HasStatedSkills {
			details.Concerns = []string{"Skills not verified"}
		}
	}

	return &details, nil
}

// EnhanceRecommendations adds idea quality, team capability, and AI vote
func (a *Analyzer) EnhanceRecommendations(ctx context.Context, recommendations *Recommendations, network string, refID uint32, mcpTool *aicore.Tool, teams *cache.TeamsData, positive *PositiveAnalysis, steelMan *SteelManAnalysis) error {
	var tools []aicore.Tool
	if mcpTool != nil {
		tools = append(tools, *mcpTool)
	}

	prompt := fmt.Sprintf(`Based on the analysis, determine:

1. Idea Quality: Is the idea itself good? (Good/Bad/Uncertain)
2. Team Capability: Can the team pull it off? (Can deliver/Cannot deliver/Uncertain)
3. AI Vote: Should we vote Aye, Nay, or Abstain? (Aye/Nay/Abstain)

Current Verdict: %s
Reasoning: %s

%s

Consider:
- The proposal's merits
- Team capabilities and verification
- Financial feasibility
- Technical feasibility
- Risks and concerns

Respond with JSON:
{
  "ideaQuality": "Good/Bad/Uncertain",
  "teamCapability": "Can deliver/Cannot deliver/Uncertain",
  "aiVote": "Aye/Nay/Abstain"
}`, recommendations.Verdict, recommendations.Reasoning, a.getProposalInstruction(network, refID, mcpTool != nil))

	response, err := a.client.Respond(ctx, prompt, tools, aicore.Options{})
	if err != nil {
		return fmt.Errorf("AI response: %w", err)
	}

	var enhanced struct {
		IdeaQuality    string `json:"ideaQuality"`
		TeamCapability string `json:"teamCapability"`
		AIVote         string `json:"aiVote"`
	}

	if err := a.extractJSON(response, &enhanced); err != nil {
		log.Printf("reports: failed to parse enhanced recommendations JSON: %v", err)
		enhanced.IdeaQuality = "Uncertain"
		enhanced.TeamCapability = "Uncertain"
		enhanced.AIVote = "Abstain"
	}

	recommendations.IdeaQuality = enhanced.IdeaQuality
	recommendations.TeamCapability = enhanced.TeamCapability
	recommendations.AIVote = enhanced.AIVote

	return nil
}

// extractJSON extracts JSON from AI response
func (a *Analyzer) extractJSON(response string, target interface{}) error {
	// Try to find JSON block
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	
	if startIdx < 0 || endIdx <= startIdx {
		return fmt.Errorf("no JSON found in response")
	}
	
	jsonStr := response[startIdx : endIdx+1]
	
	// Clean the JSON string to fix common issues
	jsonStr = a.cleanJSONString(jsonStr)
	
	// Try to parse
	err := json.Unmarshal([]byte(jsonStr), target)
	if err != nil {
		// Log a snippet of the problematic JSON for debugging (first 500 chars)
		snippet := jsonStr
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		log.Printf("reports: JSON parse error, snippet: %s", snippet)
		return fmt.Errorf("JSON parse error: %w", err)
	}
	
	return nil
}

// cleanJSONString attempts to fix common JSON issues like unescaped newlines in strings
func (a *Analyzer) cleanJSONString(jsonStr string) string {
	// This is a simple approach - try to escape newlines within string values
	// We'll look for patterns like: "key": "value\nwith newline"
	var result strings.Builder
	inString := false
	escapeNext := false
	
	for i, r := range jsonStr {
		if escapeNext {
			result.WriteRune(r)
			escapeNext = false
			continue
		}
		
		if r == '\\' {
			result.WriteRune(r)
			escapeNext = true
			continue
		}
		
		if r == '"' {
			// Check if this is an escaped quote
			if i > 0 && jsonStr[i-1] == '\\' {
				result.WriteRune(r)
				continue
			}
			inString = !inString
			result.WriteRune(r)
			continue
		}
		
		if inString {
			// Inside a string, escape newlines and other problematic characters
			switch r {
			case '\n':
				result.WriteString("\\n")
			case '\r':
				result.WriteString("\\r")
			case '\t':
				result.WriteString("\\t")
			case '\u0000': // Null byte
				result.WriteString("\\u0000")
			default:
				// Only write printable characters or valid escape sequences
				if r >= 32 || r == '\t' || r == '\n' || r == '\r' {
					result.WriteRune(r)
				}
			}
		} else {
			result.WriteRune(r)
		}
	}
	
	return result.String()
}

// getProposalInstruction returns instructions for how to get proposal content
func (a *Analyzer) getProposalInstruction(network string, refID uint32, hasMCP bool) string {
	if hasMCP {
		networkSlug := strings.ToLower(strings.TrimSpace(network))
		return fmt.Sprintf(`First, use the fetch_referendum_data tool to retrieve the full proposal content:
- Call with {"network": "%s", "refId": %d, "resource": "content"}
- Review the proposal content returned by the tool
- Then perform the analysis based on that content`, networkSlug, refID)
	}
	return fmt.Sprintf("Network: %s, Referendum ID: %d\n\n[Note: Proposal content should be provided via MCP tool when available]", network, refID)
}

