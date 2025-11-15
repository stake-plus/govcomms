package aliashunter

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	agentcore "github.com/stake-plus/govcomms/src/agents/core"
)

// Config controls scoring thresholds for alias suggestions.
type Config struct {
	MinConfidence  float64
	MaxSuggestions int
}

// Agent clusters social accounts / identities that likely belong together.
type Agent struct {
	cfg  Config
	deps agentcore.RuntimeDeps
}

// NewAgent constructs a hunter with reasonable defaults.
func NewAgent(cfg Config, deps agentcore.RuntimeDeps) *Agent {
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = 0.55
	}
	if cfg.MaxSuggestions <= 0 {
		cfg.MaxSuggestions = 10
	}
	return &Agent{cfg: cfg, deps: deps}
}

func (a *Agent) Name() string { return "alias_hunter" }

func (a *Agent) Synopsis() string {
	return "Correlates social handles, github usernames, and other metadata to uncover likely aliases."
}

func (a *Agent) Categories() []string {
	return []string{"due_diligence", "identity"}
}

func (a *Agent) Capabilities() []agentcore.Capability {
	return []agentcore.Capability{
		{
			Name:        "handle_correlation",
			Description: "Detect matching handles across multiple platforms",
			Signals:     []string{"normalized_handle", "platform_overlap"},
		},
		{
			Name:        "name_similarity",
			Description: "Compare display names, org names, and email stems",
			Signals:     []string{"display_name_match", "email_domain"},
		},
	}
}

// Execute groups similar identities and returns the strongest matches.
func (a *Agent) Execute(ctx context.Context, mission agentcore.Mission) (*agentcore.Result, error) {
	start := time.Now().UTC()
	seeds := collectIdentities(mission)
	if len(seeds) == 0 {
		return &agentcore.Result{
			MissionID:   mission.ID,
			StartedAt:   start,
			CompletedAt: time.Now().UTC(),
			Status:      agentcore.MissionStatusPending,
			Summary:     "No identities supplied to alias hunter",
			Tags:        []string{"alias:missing-input"},
		}, nil
	}

	clusters := clusterIdentities(seeds)
	suggestions := scoreClusters(clusters, a.cfg.MinConfidence)
	if len(suggestions) == 0 {
		return &agentcore.Result{
			MissionID:   mission.ID,
			StartedAt:   start,
			CompletedAt: time.Now().UTC(),
			Status:      agentcore.MissionStatusCompleted,
			Summary:     "No high-confidence alias clusters identified",
			Confidence:  0.35,
			Tags:        []string{"alias:none"},
		}, nil
	}

	if len(suggestions) > a.cfg.MaxSuggestions {
		suggestions = suggestions[:a.cfg.MaxSuggestions]
	}

	findings := make([]agentcore.Finding, 0, len(suggestions))
	evidence := []agentcore.Evidence{}
	totalPlatforms := map[string]struct{}{}
	for idx, suggestion := range suggestions {
		platforms := uniquePlatforms(suggestion.Items)
		for _, p := range platforms {
			totalPlatforms[p] = struct{}{}
		}
		label := fmt.Sprintf("Alias cluster %d (%s)", idx+1, strings.Join(platforms, ", "))
		findings = append(findings, agentcore.Finding{
			Title:      label,
			Details:    describeCluster(suggestion),
			Confidence: suggestion.Score,
			Severity:   severityForScore(suggestion.Score),
		})
		for _, item := range suggestion.Items {
			if item.URL == "" {
				continue
			}
			evidence = append(evidence, agentcore.Evidence{
				Label:      fmt.Sprintf("%s (%s)", item.Value, item.Platform),
				URL:        item.URL,
				Source:     item.Source,
				CapturedAt: time.Now().UTC(),
			})
		}
	}

	summary := fmt.Sprintf("Identified %d alias clusters across %d platforms.", len(findings), len(totalPlatforms))
	result := &agentcore.Result{
		MissionID:   mission.ID,
		StartedAt:   start,
		CompletedAt: time.Now().UTC(),
		Status:      agentcore.MissionStatusCompleted,
		Summary:     summary,
		Confidence:  aggregateConfidence(suggestions),
		Findings:    findings,
		Evidence:    evidence,
		Tags:        []string{"alias"},
	}
	result.Raw = map[string]any{
		"suggestions": suggestions,
	}
	return result, nil
}

type identity struct {
	Value       string
	DisplayName string
	Platform    string
	Kind        string
	URL         string
	Source      string
	Metadata    map[string]string
}

type cluster struct {
	Key   string
	Items []identity
}

type clusterSuggestion struct {
	Key   string
	Score float64
	Items []identity
}

func collectIdentities(mission agentcore.Mission) []identity {
	out := []identity{}
	if mission.Subject.Identifier != "" {
		out = append(out, identityFromSubject("subject", mission.Subject))
	}
	for idx, aliasSubject := range mission.Aliases {
		if aliasSubject.Identifier == "" && aliasSubject.DisplayName == "" {
			continue
		}
		id := identityFromSubject(fmt.Sprintf("alias[%d]", idx), aliasSubject)
		out = append(out, id)
	}
	if handles, ok := mission.Inputs["handles"]; ok {
		out = append(out, identitiesFromValue(handles, "handles")...)
	}
	if accounts, ok := mission.Inputs["accounts"]; ok {
		out = append(out, identitiesFromValue(accounts, "accounts")...)
	}
	for _, artifact := range mission.Artifacts {
		if artifact.Type != agentcore.ArtifactAliasMapping {
			continue
		}
		if artifact.Data == nil {
			continue
		}
		if val, ok := artifact.Data["value"]; ok {
			id := identity{
				Value:    fmt.Sprint(val),
				Platform: stringValue(artifact.Data["platform"]),
				Kind:     stringValue(artifact.Data["kind"]),
				URL:      stringValue(artifact.Data["url"]),
				Source:   artifact.Source,
				Metadata: mapFromAny(artifact.Data["metadata"]),
			}
			if id.Value != "" {
				out = append(out, id)
			}
		}
	}
	return dedupeIdentities(out)
}

func identityFromSubject(source string, subject agentcore.Subject) identity {
	return identity{
		Value:       subject.Identifier,
		DisplayName: subject.DisplayName,
		Platform:    subject.Platform,
		Kind:        string(subject.Type),
		URL:         subject.URL,
		Source:      source,
		Metadata:    subject.Metadata,
	}
}

func identitiesFromValue(raw any, source string) []identity {
	out := []identity{}
	switch typed := raw.(type) {
	case []string:
		for _, v := range typed {
			if cleaned := strings.TrimSpace(v); cleaned != "" {
				out = append(out, identity{Value: cleaned, Source: source})
			}
		}
	case []any:
		for _, v := range typed {
			out = append(out, identitiesFromValue(v, source)...)
		}
	case string:
		for _, part := range strings.Split(typed, ",") {
			if cleaned := strings.TrimSpace(part); cleaned != "" {
				out = append(out, identity{Value: cleaned, Source: source})
			}
		}
	case map[string]any:
		id := identity{
			Value:       stringValue(typed["value"]),
			Platform:    stringValue(typed["platform"]),
			URL:         stringValue(typed["url"]),
			DisplayName: stringValue(typed["display_name"]),
			Source:      source,
			Metadata:    mapFromAny(typed["metadata"]),
			Kind:        stringValue(typed["kind"]),
		}
		if id.Value != "" {
			out = append(out, id)
		}
	}
	return out
}

func dedupeIdentities(ids []identity) []identity {
	seen := map[string]struct{}{}
	out := make([]identity, 0, len(ids))
	for _, id := range ids {
		key := normalizeHandle(id.Value, id.Platform)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}

func clusterIdentities(ids []identity) map[string]*cluster {
	clusters := map[string]*cluster{}
	for _, id := range ids {
		key := normalizeHandle(id.Value, "")
		if key == "" {
			continue
		}
		if _, ok := clusters[key]; !ok {
			clusters[key] = &cluster{Key: key}
		}
		clusters[key].Items = append(clusters[key].Items, id)
	}
	return clusters
}

func scoreClusters(clusters map[string]*cluster, threshold float64) []clusterSuggestion {
	suggestions := []clusterSuggestion{}
	for _, cl := range clusters {
		if cl == nil || len(cl.Items) < 2 {
			continue
		}
		score := clusterScore(cl)
		if score < threshold {
			continue
		}
		suggestions = append(suggestions, clusterSuggestion{
			Key:   cl.Key,
			Items: cl.Items,
			Score: score,
		})
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		return suggestions[i].Score > suggestions[j].Score
	})
	return suggestions
}

func clusterScore(cl *cluster) float64 {
	if cl == nil {
		return 0
	}
	platforms := uniquePlatforms(cl.Items)
	score := 0.2 * float64(len(platforms)-1)
	score += 0.1 * float64(len(cl.Items)-1)
	nameSim := averageNameSimilarity(cl.Items)
	score += 0.3 * nameSim
	if hasSharedDomains(cl.Items) {
		score += 0.2
	}
	if score > 1 {
		score = 1
	}
	if score < 0 {
		score = 0
	}
	return score
}

func averageNameSimilarity(items []identity) float64 {
	if len(items) < 2 {
		return 0
	}
	total := 0.0
	count := 0
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			total += simpleNameSimilarity(items[i].DisplayName, items[j].DisplayName)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func hasSharedDomains(items []identity) bool {
	domains := map[string]struct{}{}
	for _, item := range items {
		if email := normalizeEmail(item.Metadata["email"]); email != "" {
			if _, ok := domains[email]; ok {
				return true
			}
			domains[email] = struct{}{}
		}
	}
	return false
}

func describeCluster(s clusterSuggestion) string {
	parts := make([]string, 0, len(s.Items))
	for _, item := range s.Items {
		label := item.Value
		if item.Platform != "" {
			label = fmt.Sprintf("%s@%s", item.Value, item.Platform)
		}
		parts = append(parts, label)
	}
	return fmt.Sprintf("%s (score %.2f)", strings.Join(parts, ", "), s.Score)
}

func uniquePlatforms(items []identity) []string {
	set := map[string]struct{}{}
	for _, item := range items {
		if item.Platform == "" {
			continue
		}
		set[strings.ToLower(item.Platform)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for platform := range set {
		out = append(out, platform)
	}
	sort.Strings(out)
	return out
}

func severityForScore(score float64) string {
	switch {
	case score >= 0.8:
		return "info"
	case score >= 0.65:
		return "low"
	default:
		return "medium"
	}
}

func aggregateConfidence(suggestions []clusterSuggestion) float64 {
	if len(suggestions) == 0 {
		return 0.4
	}
	total := 0.0
	for _, suggestion := range suggestions {
		total += suggestion.Score
	}
	conf := total / float64(len(suggestions))
	if conf > 0.95 {
		conf = 0.95
	}
	return conf
}

func normalizeHandle(handle string, platform string) string {
	if handle == "" {
		return ""
	}
	clean := strings.ToLower(strings.TrimSpace(handle))
	clean = strings.TrimPrefix(clean, "@")
	clean = strings.ReplaceAll(clean, " ", "")
	if platform != "" {
		return fmt.Sprintf("%s:%s", strings.ToLower(platform), clean)
	}
	return clean
}

func stringValue(val any) string {
	if val == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(val))
}

func mapFromAny(val any) map[string]string {
	result := map[string]string{}
	if val == nil {
		return result
	}
	if typed, ok := val.(map[string]string); ok {
		return typed
	}
	if typed, ok := val.(map[string]any); ok {
		for k, v := range typed {
			result[k] = fmt.Sprint(v)
		}
	}
	return result
}

func simpleNameSimilarity(a, b string) float64 {
	a = normalizeName(a)
	b = normalizeName(b)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.85
	}
	return 0.35
}

func normalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	return strings.Join(strings.Fields(name), " ")
}

func normalizeEmail(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}
