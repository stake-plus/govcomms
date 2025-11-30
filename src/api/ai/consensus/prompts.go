package consensus

import (
	"encoding/json"
	"fmt"
	"strings"
)

const researchTemplate = `
You are %s, a specialist model collaborating with other AIs on a due diligence
mission. Follow the mission brief precisely:

%s

Return STRICT JSON (no Markdown) using this schema:
{
  "answer": "concise synthesis under 250 words",
  "rationale": "bullet or sentence summary of key reasoning",
  "confidence": 0.0-1.0,
  "evidence": [
    {"claim": "short label", "support": "why it matters", "sources": ["url"], "confidence": 0.0-1.0}
  ],
  "findings": [
    {"statement": "fact/alias", "verdict": "supported|inconclusive|rejected", "score": 0.0-1.0, "notes": "optional"}
  ]
}

Use web/browsing tools if available to validate claims. Cite URLs whenever you
reference external knowledge. Focus on verifiable evidence, not speculation.
`

const reviewTemplate = `
You are %s, serving on the verification council for this mission:

%s

You must review peer submissions (JSON array) and score each candidate:
%s

Return STRICT JSON:
{
  "votes": [
    {
      "candidate": "provider or label",
      "score": 0.0-1.0,
      "verdict": "accept|reject|revise",
      "notes": "short reasoning",
      "strengths": ["optional highlights"],
      "weaknesses": ["optional gaps"]
    }
  ],
  "preferred": "candidate you trust most",
  "confidence": 0.0-1.0,
  "summary": "one paragraph describing consensus view"
}
`

const finalTemplate = `
You are the arbiter responsible for issuing the final consensus decision for
this mission:

%s

Council report (JSON):
%s

Write the final answer with these sections:
1. **Decision** – plain-language summary and recommended action.
2. **Confidence** – numeric confidence (0-100%%) and key justifications.
3. **Evidence** – bullet list citing claims + sources.
4. **Dissent** – mention notable disagreements or uncertainties.

Do not invent facts not present in the report.`

func buildResearchPrompt(participant, mission string) string {
	name := participant
	if strings.TrimSpace(name) == "" {
		name = "delegate"
	}
	return fmt.Sprintf(researchTemplate, name, mission)
}

func buildReviewPrompt(participant, mission, dossier string) string {
	name := participant
	if strings.TrimSpace(name) == "" {
		name = "reviewer"
	}
	if strings.TrimSpace(dossier) == "" {
		dossier = "[]"
	}
	return fmt.Sprintf(reviewTemplate, name, mission, dossier)
}

func buildFinalPrompt(mission, report string) string {
	if strings.TrimSpace(report) == "" {
		report = "{}"
	}
	return fmt.Sprintf(finalTemplate, mission, report)
}

func buildDossier(contributions []analysisPacket) string {
	type snapshot struct {
		Candidate  string        `json:"candidate"`
		Summary    string        `json:"summary"`
		Rationale  string        `json:"rationale"`
		Confidence float64       `json:"confidence"`
		Evidence   []evidenceRef `json:"evidence"`
		Findings   []findingRef  `json:"findings"`
	}
	payload := make([]snapshot, 0, len(contributions))
	for _, c := range contributions {
		payload = append(payload, snapshot{
			Candidate:  c.Participant,
			Summary:    c.Summary,
			Rationale:  c.Rationale,
			Confidence: c.Confidence,
			Evidence:   c.Evidence,
			Findings:   c.Findings,
		})
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "[]"
	}
	return string(data)
}
