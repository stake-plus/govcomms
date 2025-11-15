package socialpresence

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	agentcore "github.com/stake-plus/govcomms/src/agents/core"
)

// Config enumerates tunable options for the social presence agent.
type Config struct {
	Providers []string
}

// Agent implements due diligence over social-media accounts.
type Agent struct {
	cfg    Config
	deps   agentcore.RuntimeDeps
	probes []Probe
}

// Probe hydrates raw profile information from a data source.
type Probe interface {
	Name() string
	Lookup(ctx context.Context, mission agentcore.Mission) (*Profile, error)
}

// Profile captures a single source-of-truth snapshot about a handle.
type Profile struct {
	Provider string
	Platform string
	Handle   string
	URL      string

	DisplayName string
	CreatedAt   *time.Time

	Followers *int
	Following *int
	Posts     *int

	AvgInteractions *float64

	Flags []string
	Notes []string
}

// ErrNoData signals a probe had nothing to report.
var ErrNoData = errors.New("socialpresence: no data")

// NewAgent constructs the agent with default probes.
func NewAgent(cfg Config, deps agentcore.RuntimeDeps) *Agent {
	if len(cfg.Providers) == 0 {
		cfg.Providers = []string{"manual"}
	}

	probes := make([]Probe, 0, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		switch strings.ToLower(strings.TrimSpace(provider)) {
		case "manual":
			probes = append(probes, ManualProbe{})
		}
	}
	if len(probes) == 0 {
		probes = append(probes, ManualProbe{})
	}

	return &Agent{
		cfg:    cfg,
		deps:   deps,
		probes: probes,
	}
}

func (a *Agent) Name() string { return "social_presence" }

func (a *Agent) Synopsis() string {
	return "Investigates a social-media account to estimate reach, liveliness, and bot risk."
}

func (a *Agent) Categories() []string {
	return []string{"due_diligence", "social", "risk"}
}

func (a *Agent) Capabilities() []agentcore.Capability {
	return []agentcore.Capability{
		{
			Name:        "account_profile",
			Description: "Collect account age, URL, follower/following counts",
			Signals:     []string{"followers", "following", "account_age"},
		},
		{
			Name:        "engagement_health",
			Description: "Estimate liveliness using posting cadence and engagement rate",
			Signals:     []string{"posts_per_week", "engagement_rate"},
		},
		{
			Name:        "bot_screening",
			Description: "Surface heuristics that indicate inorganic follower patterns",
			Signals:     []string{"bot_score"},
		},
	}
}

// Execute performs the investigation across all configured probes.
func (a *Agent) Execute(ctx context.Context, mission agentcore.Mission) (*agentcore.Result, error) {
	if strings.TrimSpace(mission.Subject.Identifier) == "" {
		return nil, fmt.Errorf("socialpresence: subject identifier required")
	}

	start := time.Now().UTC()
	collected := make([]*Profile, 0, len(a.probes))
	for _, probe := range a.probes {
		if probe == nil {
			continue
		}
		profile, err := probe.Lookup(ctx, mission)
		if err != nil {
			if errors.Is(err, ErrNoData) {
				continue
			}
			if a.deps.Logger != nil {
				a.deps.Logger.Printf("socialpresence: probe %s failed: %v", probe.Name(), err)
			}
			continue
		}
		if profile != nil {
			collected = append(collected, profile)
		}
	}

	result := &agentcore.Result{
		MissionID: mission.ID,
		StartedAt: start,
		Status:    agentcore.MissionStatusCompleted,
	}

	if len(collected) == 0 {
		result.Status = agentcore.MissionStatusPending
		result.Summary = fmt.Sprintf("Unable to collect social signals for %s on %s", mission.Subject.Identifier, mission.Subject.Platform)
		result.Tags = []string{"needs_collection", "social"}
		result.CompletedAt = time.Now().UTC()
		return result, nil
	}

	agg := reduceProfiles(collected)
	analysis := analyzeAggregate(mission.Subject, agg)

	result.Summary = analysis.Summary
	result.Confidence = analysis.Confidence
	result.Findings = analysis.Findings
	result.Metrics = analysis.Metrics
	result.Evidence = analysis.Evidence
	result.Tags = analysis.Tags
	if result.Raw == nil {
		result.Raw = map[string]any{}
	}
	result.Raw["aggregate"] = agg
	result.Raw["sources"] = collected
	result.CompletedAt = time.Now().UTC()
	return result, nil
}

type aggregateProfile struct {
	Platform    string
	Handle      string
	DisplayName string
	URL         string
	Followers   *int
	Following   *int
	Posts       *int
	Engagement  *float64
	CreatedAt   *time.Time
	Sources     []string
	Flags       []string
}

func reduceProfiles(profiles []*Profile) aggregateProfile {
	result := aggregateProfile{}
	seenSources := make(map[string]struct{})
	flagSet := make(map[string]struct{})

	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		if result.Platform == "" {
			result.Platform = profile.Platform
		}
		if result.Handle == "" {
			result.Handle = profile.Handle
		}
		if result.DisplayName == "" && profile.DisplayName != "" {
			result.DisplayName = profile.DisplayName
		}
		if result.URL == "" && profile.URL != "" {
			result.URL = profile.URL
		}
		if profile.CreatedAt != nil {
			if result.CreatedAt == nil || profile.CreatedAt.Before(*result.CreatedAt) {
				t := *profile.CreatedAt
				result.CreatedAt = &t
			}
		}
		result.Followers = pickLargestInt(result.Followers, profile.Followers)
		result.Following = pickLargestInt(result.Following, profile.Following)
		result.Posts = pickLargestInt(result.Posts, profile.Posts)
		result.Engagement = pickAverageFloat(result.Engagement, profile.AvgInteractions)

		if profile.Provider != "" {
			if _, ok := seenSources[profile.Provider]; !ok {
				seenSources[profile.Provider] = struct{}{}
				result.Sources = append(result.Sources, profile.Provider)
			}
		}

		for _, flag := range profile.Flags {
			flag = strings.TrimSpace(flag)
			if flag == "" {
				continue
			}
			if _, ok := flagSet[flag]; !ok {
				flagSet[flag] = struct{}{}
				result.Flags = append(result.Flags, flag)
			}
		}
	}

	sort.Strings(result.Sources)
	sort.Strings(result.Flags)
	return result
}

type analysisOutput struct {
	Summary    string
	Confidence float64
	Findings   []agentcore.Finding
	Metrics    []agentcore.Metric
	Evidence   []agentcore.Evidence
	Tags       []string
}

func analyzeAggregate(subject agentcore.Subject, agg aggregateProfile) analysisOutput {
	followers := derefInt(agg.Followers)
	following := derefInt(agg.Following)
	posts := derefInt(agg.Posts)
	engagement := derefFloat(agg.Engagement)

	accountAgeDays := 0.0
	if agg.CreatedAt != nil {
		accountAgeDays = time.Since(*agg.CreatedAt).Hours() / 24
		if accountAgeDays < 1 {
			accountAgeDays = 1
		}
	}

	ratio := 0.0
	if following > 0 {
		ratio = float64(followers) / float64(following)
	}

	postsPerWeek := 0.0
	if posts > 0 && accountAgeDays > 0 {
		postsPerWeek = (float64(posts) / accountAgeDays) * 7
	}

	engagementRate := 0.0
	if followers > 0 && engagement > 0 {
		engagementRate = engagement / float64(followers)
	}

	liveliness := classifyLiveliness(postsPerWeek, engagementRate)
	botScore := estimateBotScore(followers, following, engagementRate, accountAgeDays, agg.Flags)
	confidence := clamp(0.35+0.15*float64(len(agg.Sources)), 0, 0.95)

	summary := fmt.Sprintf("%s/%s appears %s with %.2f posts/week and %.2f%% engagement; estimated bot risk %.0f%%",
		subject.Platform, subject.Identifier, liveliness, postsPerWeek, engagementRate*100, botScore*100)

	findings := []agentcore.Finding{
		{
			Title:      "Audience scale",
			Details:    fmt.Sprintf("Followers ≈ %d, Following ≈ %d, ratio %.2f", followers, following, ratio),
			Severity:   classifyAudienceSeverity(followers),
			Confidence: clamp(0.4+0.1*float64(len(agg.Sources)), 0, 0.9),
		},
		{
			Title:      "Engagement health",
			Details:    fmt.Sprintf("Posts/week ≈ %.2f, engagement rate ≈ %.2f%%", postsPerWeek, engagementRate*100),
			Severity:   livelinessSeverity(liveliness),
			Confidence: clamp(0.35+0.1*float64(len(agg.Sources)), 0, 0.85),
		},
		{
			Title:      "Bot / inorganic risk",
			Details:    fmt.Sprintf("Heuristic score %.0f%% based on ratio %.2f, age %.0fd, engagement %.2f%%", botScore*100, ratio, accountAgeDays, engagementRate*100),
			Severity:   classifyBotSeverity(botScore),
			Confidence: clamp(0.3+0.05*float64(len(agg.Sources)), 0, 0.8),
		},
	}

	metrics := []agentcore.Metric{
		{Key: "followers", Value: float64(followers), Units: "count"},
		{Key: "following", Value: float64(following), Units: "count"},
		{Key: "follower_following_ratio", Value: ratio},
		{Key: "posts_total", Value: float64(posts), Units: "count"},
		{Key: "posts_per_week", Value: postsPerWeek},
		{Key: "engagement_rate", Value: engagementRate, Units: "ratio"},
		{Key: "bot_score", Value: botScore, Units: "ratio"},
	}

	evidence := []agentcore.Evidence{}
	if agg.URL != "" {
		label := agg.Platform
		if label == "" {
			label = "social"
		}
		label = strings.ToUpper(label)
		evidence = append(evidence, agentcore.Evidence{
			Label:      fmt.Sprintf("%s profile", label),
			URL:        agg.URL,
			Source:     agg.Platform,
			CapturedAt: time.Now().UTC(),
		})
	}

	tags := []string{"social"}
	if engagementRate < 0.01 {
		tags = append(tags, "social:low-engagement")
	}
	if botScore > 0.6 {
		tags = append(tags, "social:bot-risk")
	}
	if postsPerWeek > 5 {
		tags = append(tags, "social:high-activity")
	}

	return analysisOutput{
		Summary:    summary,
		Confidence: confidence,
		Findings:   findings,
		Metrics:    metrics,
		Evidence:   evidence,
		Tags:       tags,
	}
}

func classifyAudienceSeverity(followers int) string {
	switch {
	case followers >= 50000:
		return "info"
	case followers >= 5000:
		return "low"
	case followers == 0:
		return "high"
	default:
		return "medium"
	}
}

func livelinessSeverity(label string) string {
	switch label {
	case "active":
		return "info"
	case "stable":
		return "low"
	case "cooling":
		return "medium"
	default:
		return "high"
	}
}

func classifyBotSeverity(score float64) string {
	switch {
	case score >= 0.75:
		return "high"
	case score >= 0.55:
		return "medium"
	case score >= 0.35:
		return "low"
	default:
		return "info"
	}
}

func classifyLiveliness(postsPerWeek, engagementRate float64) string {
	switch {
	case postsPerWeek >= 5 && engagementRate >= 0.02:
		return "active"
	case postsPerWeek >= 1 && engagementRate >= 0.01:
		return "stable"
	case postsProximity(postsPerWeek, engagementRate):
		return "cooling"
	default:
		return "dormant"
	}
}

func postsProximity(postsPerWeek, engagementRate float64) bool {
	return postsPerWeek >= 0.3 && engagementRate >= 0.005
}

func estimateBotScore(followers, following int, engagementRate, ageDays float64, flags []string) float64 {
	score := 0.35
	if followers == 0 {
		score += 0.15
	}
	if following == 0 && followers > 0 {
		score += 0.15
	}
	if followers > 0 && engagementRate > 0 {
		switch {
		case engagementRate < 0.005:
			score += 0.35
		case engagementRate < 0.015:
			score += 0.2
		default:
			score -= 0.1
		}
	}
	if following > 0 {
		ratio := float64(followers) / float64(following)
		if ratio > 75 {
			score += 0.2
		} else if ratio > 40 {
			score += 0.1
		}
	}
	if ageDays > 0 && ageDays < 60 && followers > 1000 {
		score += 0.2
	}
	for _, flag := range flags {
		switch strings.ToLower(flag) {
		case "suspicious_followers", "paid_followers":
			score += 0.2
		case "manual_verified":
			score -= 0.15
		}
	}
	return clamp(score, 0, 1)
}

func pickLargestInt(current, candidate *int) *int {
	if candidate == nil {
		return current
	}
	if current == nil {
		val := *candidate
		return &val
	}
	if *candidate > *current {
		val := *candidate
		return &val
	}
	return current
}

func pickAverageFloat(current, candidate *float64) *float64 {
	if candidate == nil || *candidate <= 0 {
		return current
	}
	if current == nil {
		val := *candidate
		return &val
	}
	val := (*current + *candidate) / 2
	return &val
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func clamp(val, min, max float64) float64 {
	return math.Max(min, math.Min(max, val))
}
