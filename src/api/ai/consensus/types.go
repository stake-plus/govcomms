package consensus

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/stake-plus/govcomms/src/api/ai/core"
)

type participantSpec struct {
	Provider string
	Model    string
}

type participantSpecs struct {
	Researchers []participantSpec
	Reviewers   []participantSpec
	Voters      []participantSpec
}

type analysisPacket struct {
	Participant string        `json:"participant"`
	Provider    string        `json:"provider"`
	Summary     string        `json:"summary"`
	Rationale   string        `json:"rationale"`
	Confidence  float64       `json:"confidence"`
	Evidence    []evidenceRef `json:"evidence"`
	Findings    []findingRef  `json:"findings"`
	Raw         string        `json:"-"`
	Err         error         `json:"-"`
}

type evidenceRef struct {
	Claim      string   `json:"claim"`
	Support    string   `json:"support"`
	Sources    []string `json:"sources"`
	Confidence float64  `json:"confidence"`
}

type findingRef struct {
	Statement string  `json:"statement"`
	Verdict   string  `json:"verdict"`
	Score     float64 `json:"score"`
	Notes     string  `json:"notes"`
}

type analysisPayload struct {
	Answer     string        `json:"answer"`
	Rationale  string        `json:"rationale"`
	Confidence float64       `json:"confidence"`
	Evidence   []evidenceRef `json:"evidence"`
	Findings   []findingRef  `json:"findings"`
}

type ballot struct {
	Judge      string  `json:"judge"`
	Provider   string  `json:"provider"`
	Votes      []vote  `json:"votes"`
	Preferred  string  `json:"preferred"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
	Raw        string  `json:"-"`
	Err        error   `json:"-"`
}

type vote struct {
	Candidate  string   `json:"candidate"`
	Score      float64  `json:"score"`
	Verdict    string   `json:"verdict"`
	Notes      string   `json:"notes"`
	Strengths  []string `json:"strengths"`
	Weaknesses []string `json:"weaknesses"`
}

type ballotPayload struct {
	Votes      []vote  `json:"votes"`
	Preferred  string  `json:"preferred"`
	Confidence float64 `json:"confidence"`
	Summary    string  `json:"summary"`
}

type consensusReport struct {
	Mission       string           `json:"mission"`
	Contributions []analysisPacket `json:"contributions"`
	Ballots       []ballot         `json:"ballots"`
	Metrics       consensusMetrics `json:"metrics"`
	AgreementGoal float64          `json:"agreement_goal"`
}

type consensusMetrics struct {
	TopCandidate string            `json:"top_candidate"`
	Agreement    float64           `json:"agreement"`
	MeanScore    float64           `json:"mean_score"`
	TotalVotes   int               `json:"total_votes"`
	Candidates   []candidateMetric `json:"candidates"`
	Supporters   []string          `json:"supporters"`
	Dissenters   []string          `json:"dissenters"`
}

type candidateMetric struct {
	Name     string         `json:"name"`
	Score    float64        `json:"score"`
	Votes    int            `json:"votes"`
	Verdicts map[string]int `json:"verdicts"`
}

func parseParticipantSpecs(raw string) []participantSpec {
	tokens := splitTokens(raw)
	out := make([]participantSpec, 0, len(tokens))
	for _, token := range tokens {
		if token == "" {
			continue
		}
		spec := participantSpec{}
		if strings.Contains(token, ":") {
			parts := strings.SplitN(token, ":", 2)
			spec.Provider = normalizeProvider(parts[0])
			spec.Model = strings.TrimSpace(parts[1])
		} else {
			spec.Provider = normalizeProvider(token)
		}
		if spec.Provider == "" {
			continue
		}
		out = append(out, spec)
	}
	return out
}

func normalizeProvider(provider string) string {
	return strings.TrimSpace(strings.ToLower(provider))
}

func splitTokens(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\t':
			return true
		default:
			return false
		}
	})
}

func buildParticipants(cfg core.FactoryConfig, specs []participantSpec) ([]participant, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	extra := sanitizeExtras(cfg.Extra)
	list := make([]participant, 0, len(specs))
	nameCount := map[string]int{}
	for _, spec := range specs {
		if spec.Provider == "consensus" {
			return nil, fmt.Errorf("consensus: cannot nest consensus provider as a participant")
		}
		subCfg := cfg
		subCfg.Provider = spec.Provider
		if spec.Model != "" {
			subCfg.Model = spec.Model
		}
		subCfg.Extra = cloneExtras(extra)
		client, err := core.NewClient(subCfg)
		if err != nil {
			return nil, fmt.Errorf("consensus: init participant %s: %w", spec.Provider, err)
		}
		nameCount[spec.Provider]++
		label := spec.Provider
		if nameCount[spec.Provider] > 1 {
			label = fmt.Sprintf("%s#%d", spec.Provider, nameCount[spec.Provider])
		}
		list = append(list, participant{
			name:     label,
			provider: spec.Provider,
			model:    spec.Model,
			client:   client,
		})
	}
	return list, nil
}

func sanitizeExtras(extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return nil
	}
	clean := map[string]string{}
	for k, v := range extra {
		if strings.HasPrefix(strings.ToLower(k), "consensus_") {
			continue
		}
		clean[k] = v
	}
	return clean
}

func cloneExtras(extra map[string]string) map[string]string {
	if len(extra) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(extra))
	for k, v := range extra {
		cloned[k] = v
	}
	return cloned
}

func defaultSpecsFromKeys(cfg core.FactoryConfig) []participantSpec {
	var specs []participantSpec
	add := func(provider string) {
		for _, existing := range specs {
			if existing.Provider == provider {
				return
			}
		}
		specs = append(specs, participantSpec{Provider: provider})
	}
	if cfg.OpenAIKey != "" {
		add("gpt51")
	}
	if cfg.GeminiKey != "" {
		add("gemini25")
	}
	if cfg.GrokKey != "" {
		add("grok4")
	}
	if cfg.DeepSeekKey != "" {
		add("deepseek3")
	}
	if cfg.ClaudeKey != "" {
		add("sonnet45")
	}
	return specs
}

func parseFloat(raw string, fallback float64) float64 {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	if val, err := strconv.ParseFloat(raw, 64); err == nil {
		return val
	}
	return fallback
}

func parseInt(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	if val, err := strconv.Atoi(raw); err == nil {
		return val
	}
	return fallback
}

func clampFloat(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func applyOverrides(base, overrides core.Options) core.Options {
	result := base
	if strings.TrimSpace(overrides.Model) != "" {
		result.Model = overrides.Model
	}
	if overrides.Temperature != 0 {
		result.Temperature = overrides.Temperature
	}
	if overrides.MaxCompletionTokens != 0 {
		result.MaxCompletionTokens = overrides.MaxCompletionTokens
	}
	if strings.TrimSpace(overrides.SystemPrompt) != "" {
		result.SystemPrompt = overrides.SystemPrompt
	}
	if overrides.EnableWebSearch {
		result.EnableWebSearch = true
	}
	if overrides.EnableDeepSearch {
		result.EnableDeepSearch = true
	}
	return result
}

func parseAnalysisPacket(p participant, raw string, err error) analysisPacket {
	packet := analysisPacket{
		Participant: p.name,
		Provider:    p.provider,
		Raw:         raw,
		Err:         err,
	}
	if err != nil {
		return packet
	}
	payload := analysisPayload{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(&payload); decodeErr != nil {
		extracted := extractJSON(raw)
		if extracted != "" {
			_ = json.Unmarshal([]byte(extracted), &payload)
		}
	}
	packet.Summary = coalesce(payload.Answer, truncate(raw, 800))
	packet.Rationale = payload.Rationale
	packet.Confidence = clampFloat(payload.Confidence, 0, 1)
	packet.Evidence = payload.Evidence
	packet.Findings = payload.Findings
	return packet
}

func parseBallot(p participant, raw string, err error) ballot {
	item := ballot{
		Judge:    p.name,
		Provider: p.provider,
		Raw:      raw,
		Err:      err,
	}
	if err != nil {
		return item
	}
	payload := ballotPayload{}
	decoder := json.NewDecoder(strings.NewReader(raw))
	if decodeErr := decoder.Decode(&payload); decodeErr != nil {
		extracted := extractJSON(raw)
		if extracted != "" {
			_ = json.Unmarshal([]byte(extracted), &payload)
		}
	}
	item.Votes = normalizeVotes(payload.Votes)
	item.Preferred = payload.Preferred
	item.Confidence = clampFloat(payload.Confidence, 0, 1)
	item.Summary = payload.Summary
	return item
}

func normalizeVotes(votes []vote) []vote {
	for idx, v := range votes {
		if v.Score != 0 {
			votes[idx].Score = clampFloat(v.Score, 0, 1)
		}
		votes[idx].Candidate = normalizeProvider(v.Candidate)
		votes[idx].Verdict = normalizeVerdict(v.Verdict)
	}
	return votes
}

func normalizeVerdict(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "accept", "accepted", "approve", "approved":
		return "accept"
	case "reject", "rejected", "deny", "denied":
		return "reject"
	case "revise", "revise_required", "needs_work", "revise-request":
		return "revise"
	default:
		return "unknown"
	}
}

func extractJSON(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		return raw[start : end+1]
	}
	return ""
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func coalesce(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func buildConsensusReport(mission string, contributions []analysisPacket, ballots []ballot, goal float64) consensusReport {
	metrics := aggregateMetrics(contributions, ballots)
	if goal <= 0 {
		goal = 0.67
	}
	return consensusReport{
		Mission:       mission,
		Contributions: contributions,
		Ballots:       ballots,
		Metrics:       metrics,
		AgreementGoal: goal,
	}
}

func aggregateMetrics(contributions []analysisPacket, ballots []ballot) consensusMetrics {
	scoreboard := map[string]*candidateMetric{}
	totalVotes := 0
	totalScore := 0.0
	supporters := map[string]struct{}{}
	dissenters := map[string]struct{}{}

	for _, ballot := range ballots {
		for _, vote := range ballot.Votes {
			name := vote.Candidate
			if name == "" {
				continue
			}
			entry, ok := scoreboard[name]
			if !ok {
				entry = &candidateMetric{
					Name:     name,
					Verdicts: map[string]int{},
				}
				scoreboard[name] = entry
			}
			entry.Score += vote.Score
			entry.Votes++
			entry.Verdicts[vote.Verdict]++
			totalVotes++
			totalScore += vote.Score
			switch vote.Verdict {
			case "accept":
				supporters[ballot.Judge] = struct{}{}
			case "reject":
				dissenters[ballot.Judge] = struct{}{}
			}
		}
	}

	var metrics consensusMetrics
	if totalVotes > 0 {
		metrics.MeanScore = totalScore / float64(totalVotes)
	}
	metrics.TotalVotes = totalVotes
	metrics.Supporters = mapKeys(supporters)
	metrics.Dissenters = mapKeys(dissenters)

	topScore := -1.0
	for _, entry := range scoreboard {
		metrics.Candidates = append(metrics.Candidates, *entry)
		if entry.Score > topScore {
			topScore = entry.Score
			metrics.TopCandidate = entry.Name
		}
	}

	sort.Slice(metrics.Candidates, func(i, j int) bool {
		if metrics.Candidates[i].Score == metrics.Candidates[j].Score {
			return metrics.Candidates[i].Name < metrics.Candidates[j].Name
		}
		return metrics.Candidates[i].Score > metrics.Candidates[j].Score
	})

	if metrics.TopCandidate == "" && len(metrics.Candidates) > 0 {
		metrics.TopCandidate = metrics.Candidates[0].Name
	}

	if metrics.TopCandidate != "" {
		if entry, ok := scoreboard[metrics.TopCandidate]; ok && entry.Votes > 0 {
			accepts := entry.Verdicts["accept"]
			metrics.Agreement = clampFloat(float64(accepts)/float64(entry.Votes), 0, 1)
		}
	}

	return metrics
}

func mapKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func synthesizeFallback(report consensusReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Consensus summary (deterministic fallback)\n\n")
	if report.Metrics.TopCandidate != "" {
		fmt.Fprintf(&b, "Selected candidate: **%s** with %.0f%% agreement across %d votes.\n\n",
			report.Metrics.TopCandidate,
			report.Metrics.Agreement*100,
			report.Metrics.TotalVotes,
		)
	}
	if len(report.Contributions) > 0 {
		top := report.Contributions[0]
		for _, contrib := range report.Contributions {
			if strings.EqualFold(contrib.Participant, report.Metrics.TopCandidate) ||
				strings.EqualFold(contrib.Provider, report.Metrics.TopCandidate) {
				top = contrib
				break
			}
		}
		fmt.Fprintf(&b, "Summary:\n%s\n\n", coalesce(top.Summary, "No summary available"))
		if len(top.Evidence) > 0 {
			fmt.Fprintf(&b, "Evidence:\n")
			for _, ev := range top.Evidence {
				fmt.Fprintf(&b, "- %s (sources: %s)\n", ev.Claim, strings.Join(ev.Sources, ", "))
			}
		}
	}
	return b.String()
}
