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
func (a *Analyzer) AnalyzeFinancials(ctx context.Context, proposalContent string, summary *cache.SummaryData) (*FinancialAnalysis, error) {
	prompt := fmt.Sprintf(`Analyze the financial aspects of this blockchain governance proposal.

Extract and analyze:
1. Total funding amount requested
2. Budget breakdown by category (if provided)
3. Payment milestones and deliverables
4. Expected ROI or value proposition
5. Any financial concerns or red flags

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
}

Proposal Content:
%s`, proposalContent)

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) AnalyzeRisks(ctx context.Context, proposalContent string, summary *cache.SummaryData, claims *cache.ClaimsData, teams *cache.TeamsData) (*RiskAnalysis, error) {
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Proposal Content:\n")
	contextBuilder.WriteString(proposalContent)
	
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

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) AnalyzeTimeline(ctx context.Context, proposalContent string) (*TimelineAnalysis, error) {
	prompt := fmt.Sprintf(`Analyze the proposed timeline for this blockchain governance proposal.

Extract:
1. Proposed timeline/delivery schedule
2. Assess if timeline is: Realistic/Unrealistic/Ambitious
3. Identify concerns about timeline
4. Provide recommendations

Respond with JSON:
{
  "proposedTimeline": "Summary of proposed timeline",
  "feasibility": "Realistic/Unrealistic/Ambitious",
  "concerns": ["List timeline concerns"],
  "recommendations": ["Suggestions for timeline adjustments"]
}

Proposal:
%s`, proposalContent)

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) AnalyzeGovernance(ctx context.Context, proposalContent string, network string) (*GovernanceAnalysis, error) {
	prompt := fmt.Sprintf(`Analyze the governance impact of this proposal on the %s network.

Assess:
1. Impact Level: Low/Medium/High
2. Description of governance implications
3. Network effects (positive or negative)
4. Similar precedents or comparable proposals
5. Governance concerns

Respond with JSON:
{
  "impact": "Low/Medium/High",
  "description": "Detailed impact analysis",
  "networkEffect": "How this affects the network",
  "precedents": ["Similar past proposals or examples"],
  "concerns": ["Governance-related concerns"]
}

Proposal:
%s`, network, proposalContent)

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) AnalyzePositive(ctx context.Context, proposalContent string, summary *cache.SummaryData) (*PositiveAnalysis, error) {
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Proposal Content:\n")
	contextBuilder.WriteString(proposalContent)
	
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

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) AnalyzeSteelMan(ctx context.Context, proposalContent string, summary *cache.SummaryData, claims *cache.ClaimsData, teams *cache.TeamsData) (*SteelManAnalysis, error) {
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Proposal Content:\n")
	contextBuilder.WriteString(proposalContent)
	
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

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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
func (a *Analyzer) GenerateRecommendations(ctx context.Context, proposalContent string, summary *cache.SummaryData, financials *FinancialAnalysis, risks *RiskAnalysis, positive *PositiveAnalysis, steelMan *SteelManAnalysis) (*Recommendations, error) {
	var contextBuilder strings.Builder
	contextBuilder.WriteString("Proposal Summary:\n")
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

	response, err := a.client.Respond(ctx, prompt, nil, aicore.Options{})
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

// extractJSON extracts JSON from AI response
func (a *Analyzer) extractJSON(response string, target interface{}) error {
	// Try to find JSON block
	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	
	if startIdx < 0 || endIdx <= startIdx {
		return fmt.Errorf("no JSON found in response")
	}
	
	jsonStr := response[startIdx : endIdx+1]
	return json.Unmarshal([]byte(jsonStr), target)
}

