package grantwatch

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	agentcore "github.com/stake-plus/govcomms/src/agents/core"
)

// Config exposes heuristics for the grant watch agent.
type Config struct {
	LookbackDays    int
	RepeatThreshold int
}

// Agent inspects grant history to catch treasury abuse patterns.
type Agent struct {
	cfg  Config
	deps agentcore.RuntimeDeps
}

// NewAgent builds a grant watch agent with defaults.
func NewAgent(cfg Config, deps agentcore.RuntimeDeps) *Agent {
	if cfg.LookbackDays == 0 {
		cfg.LookbackDays = 540
	}
	if cfg.RepeatThreshold <= 1 {
		cfg.RepeatThreshold = 3
	}
	return &Agent{cfg: cfg, deps: deps}
}

func (a *Agent) Name() string { return "grant_watch" }

func (a *Agent) Synopsis() string {
	return "Reviews historical treasury awards to flag repeat-collector and non-delivery risks."
}

func (a *Agent) Categories() []string {
	return []string{"due_diligence", "grants", "risk"}
}

func (a *Agent) Capabilities() []agentcore.Capability {
	return []agentcore.Capability{
		{
			Name:        "grant_history",
			Description: "Aggregate grant awards across ecosystems and programs",
			Signals:     []string{"grant_count", "grant_amount"},
		},
		{
			Name:        "abuse_detection",
			Description: "Flag patterns such as ecosystem hopping or habitual failures",
			Signals:     []string{"failure_rate", "rapid_awards"},
		},
	}
}

// Execute evaluates the mission's grant records.
func (a *Agent) Execute(ctx context.Context, mission agentcore.Mission) (*agentcore.Result, error) {
	start := time.Now().UTC()
	records := collectRecords(mission)
	if len(records) == 0 {
		return &agentcore.Result{
			MissionID:   mission.ID,
			StartedAt:   start,
			CompletedAt: time.Now().UTC(),
			Status:      agentcore.MissionStatusPending,
			Summary:     "No grant history data available",
			Tags:        []string{"grant:no-data"},
		}, nil
	}

	filtered := filterRecords(records, a.cfg.LookbackDays)
	stats := summarize(filtered)
	findings, tags := buildFindings(stats, a.cfg.RepeatThreshold)

	totalChains := len(stats.ByChain)
	summary := fmt.Sprintf("Reviewed %d grants (~%s) across %d ecosystems; %d findings.",
		stats.TotalCount, stats.TotalAmountHuman(), totalChains, len(findings))

	result := &agentcore.Result{
		MissionID:   mission.ID,
		StartedAt:   start,
		CompletedAt: time.Now().UTC(),
		Status:      agentcore.MissionStatusCompleted,
		Summary:     summary,
		Confidence:  stats.Confidence(),
		Findings:    findings,
		Metrics:     stats.Metrics(),
		Evidence:    stats.Evidence(),
		Tags:        append([]string{"grant"}, tags...),
	}
	result.Raw = map[string]any{
		"records":  filtered,
		"stats":    stats,
		"lookback": a.cfg.LookbackDays,
	}
	return result, nil
}

type GrantRecord struct {
	Program   string
	Chain     string
	Amount    float64
	Currency  string
	Status    string
	AwardedAt time.Time
	Outcome   string
	URL       string
	Source    string
	Notes     string
}

func collectRecords(mission agentcore.Mission) []GrantRecord {
	out := []GrantRecord{}
	if val, ok := mission.Inputs["grants"]; ok {
		out = append(out, parseRecords(val, "inputs.grants")...)
	}
	for idx, artifact := range mission.Artifacts {
		if artifact.Type != agentcore.ArtifactGrantHistory {
			continue
		}
		source := artifact.Source
		if source == "" {
			source = fmt.Sprintf("artifact[%d]", idx)
		}
		out = append(out, parseRecords(artifact.Data, source)...)
	}
	return out
}

func parseRecords(raw any, source string) []GrantRecord {
	switch v := raw.(type) {
	case []any:
		results := []GrantRecord{}
		for _, entry := range v {
			results = append(results, parseRecords(entry, source)...)
		}
		return results
	case []map[string]any:
		results := []GrantRecord{}
		for _, entry := range v {
			record := recordFromMap(entry, source)
			if record != nil {
				results = append(results, *record)
			}
		}
		return results
	case map[string]any:
		if _, ok := v["records"]; ok {
			return parseRecords(v["records"], source)
		}
		if record := recordFromMap(v, source); record != nil {
			return []GrantRecord{*record}
		}
	}
	return nil
}

func recordFromMap(data map[string]any, source string) *GrantRecord {
	if data == nil {
		return nil
	}
	amount, _ := toFloat(data["amount"])
	record := GrantRecord{
		Program:   stringValue(data["program"]),
		Chain:     stringValue(data["chain"]),
		Amount:    amount,
		Currency:  stringValue(data["currency"]),
		Status:    normalizeStatus(stringValue(data["status"])),
		Outcome:   normalizeStatus(stringValue(data["outcome"])),
		URL:       stringValue(data["url"]),
		Notes:     stringValue(data["notes"]),
		Source:    source,
		AwardedAt: parseTime(data["awarded_at"]),
	}
	return &record
}

func normalizeStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "paid", "distributed", "delivered", "completed":
		return "delivered"
	case "in_progress", "progress", "ongoing":
		return "in_progress"
	case "failed", "default", "cancelled", "canceled":
		return "failed"
	default:
		return status
	}
}

func filterRecords(records []GrantRecord, lookbackDays int) []GrantRecord {
	if lookbackDays <= 0 {
		return records
	}
	cutoff := time.Now().Add(-time.Duration(lookbackDays) * 24 * time.Hour)
	out := []GrantRecord{}
	for _, record := range records {
		if record.AwardedAt.IsZero() || record.AwardedAt.After(cutoff) {
			out = append(out, record)
		}
	}
	return out
}

type summary struct {
	TotalCount  int
	TotalAmount float64
	Currency    string
	ByChain     map[string]int
	ByStatus    map[string]int
	Intervals   []float64
	LatestAt    time.Time
	Records     []GrantRecord
}

func summarize(records []GrantRecord) summary {
	sum := summary{
		TotalCount: len(records),
		ByChain:    map[string]int{},
		ByStatus:   map[string]int{},
		Records:    records,
	}
	for _, record := range records {
		sum.TotalAmount += record.Amount
		if sum.Currency == "" && record.Currency != "" {
			sum.Currency = record.Currency
		}
		if record.Chain != "" {
			key := strings.ToLower(record.Chain)
			sum.ByChain[key]++
		}
		if record.Status != "" {
			sum.ByStatus[record.Status]++
		}
		if record.AwardedAt.After(sum.LatestAt) {
			sum.LatestAt = record.AwardedAt
		}
	}
	sort.Slice(sum.Records, func(i, j int) bool {
		return sum.Records[i].AwardedAt.Before(sum.Records[j].AwardedAt)
	})
	for i := 1; i < len(sum.Records); i++ {
		prev := sum.Records[i-1].AwardedAt
		curr := sum.Records[i].AwardedAt
		if prev.IsZero() || curr.IsZero() {
			continue
		}
		sum.Intervals = append(sum.Intervals, curr.Sub(prev).Hours()/24)
	}
	return sum
}

func (s summary) TotalAmountHuman() string {
	if s.Currency == "" {
		return fmt.Sprintf("%.0f units", s.TotalAmount)
	}
	return fmt.Sprintf("%.0f %s", s.TotalAmount, strings.ToUpper(s.Currency))
}

func (s summary) Confidence() float64 {
	if s.TotalCount == 0 {
		return 0.3
	}
	conf := 0.4 + 0.1*mathLog(float64(s.TotalCount))
	if conf > 0.9 {
		conf = 0.9
	}
	return conf
}

func (s summary) Metrics() []agentcore.Metric {
	failureRate := s.failureRate()
	avgInterval := s.averageInterval()
	metrics := []agentcore.Metric{
		{Key: "grants_total", Value: float64(s.TotalCount), Units: "count"},
		{Key: "grants_amount_total", Value: s.TotalAmount, Units: s.Currency},
		{Key: "grant_failure_rate", Value: failureRate, Units: "ratio"},
		{Key: "grant_avg_spacing_days", Value: avgInterval, Units: "days"},
	}
	for chain, count := range s.ByChain {
		metrics = append(metrics, agentcore.Metric{
			Key:   fmt.Sprintf("grant_chain_%s", chain),
			Value: float64(count),
			Units: "count",
		})
	}
	return metrics
}

func (s summary) Evidence() []agentcore.Evidence {
	out := []agentcore.Evidence{}
	for _, record := range s.Records {
		if record.URL == "" {
			continue
		}
		out = append(out, agentcore.Evidence{
			Label:      fmt.Sprintf("%s %s", record.Program, record.Chain),
			URL:        record.URL,
			Source:     record.Source,
			CapturedAt: time.Now().UTC(),
		})
	}
	return out
}

func (s summary) failureRate() float64 {
	if s.TotalCount == 0 {
		return 0
	}
	failures := s.ByStatus["failed"]
	return float64(failures) / float64(s.TotalCount)
}

func (s summary) averageInterval() float64 {
	if len(s.Intervals) == 0 {
		return 0
	}
	total := 0.0
	for _, interval := range s.Intervals {
		total += interval
	}
	return total / float64(len(s.Intervals))
}

func buildFindings(stats summary, repeatThreshold int) ([]agentcore.Finding, []string) {
	findings := []agentcore.Finding{}
	tags := []string{}

	if stats.TotalCount >= repeatThreshold && len(stats.ByChain) >= 3 {
		findings = append(findings, agentcore.Finding{
			Title:      "Cross-ecosystem grant hopping",
			Details:    fmt.Sprintf("%d grants across %d ecosystems in lookback window", stats.TotalCount, len(stats.ByChain)),
			Severity:   "medium",
			Confidence: clamp(0.5+0.1*float64(len(stats.ByChain)), 0, 0.9),
		})
		tags = append(tags, "grant:ecosystem-hopping")
	}

	if stats.failureRate() >= 0.3 {
		findings = append(findings, agentcore.Finding{
			Title:      "High failure or cancellation rate",
			Details:    fmt.Sprintf("%.0f%% of recorded grants failed or defaulted", stats.failureRate()*100),
			Severity:   "high",
			Confidence: 0.7,
		})
		tags = append(tags, "grant:failure-risk")
	}

	if stats.averageInterval() > 0 && stats.averageInterval() < 45 {
		findings = append(findings, agentcore.Finding{
			Title:      "Unusual grant frequency",
			Details:    fmt.Sprintf("Average spacing between grants is %.1f days", stats.averageInterval()),
			Severity:   "medium",
			Confidence: 0.6,
		})
		tags = append(tags, "grant:rapid-awards")
	}

	if len(findings) == 0 {
		findings = append(findings, agentcore.Finding{
			Title:      "No abuse signals detected",
			Details:    "Grant history appears normal within the configured window.",
			Severity:   "info",
			Confidence: stats.Confidence(),
		})
	}
	return findings, tags
}

func clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}

func toFloat(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		if v == "" {
			return 0, false
		}
		num, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return num, true
		}
	}
	return 0, false
}

func stringValue(val any) string {
	if val == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(val))
}

func parseTime(val any) time.Time {
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		formats := []string{time.RFC3339, "2006-01-02", "2006/01/02"}
		trimmed := strings.TrimSpace(v)
		for _, layout := range formats {
			if parsed, err := time.Parse(layout, trimmed); err == nil {
				return parsed
			}
		}
	case float64:
		return time.Unix(int64(v), 0)
	case int64:
		return time.Unix(v, 0)
	}
	return time.Time{}
}

func mathLog(value float64) float64 {
	if value <= 0 {
		return 0
	}
	return math.Log(value + 1)
}
