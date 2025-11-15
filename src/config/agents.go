package config

import (
	"strconv"
	"strings"
	"time"

	shareddata "github.com/stake-plus/govcomms/src/data"
	"gorm.io/gorm"
)

// AgentsConfig exposes feature gates + knobs for background agents.
type AgentsConfig struct {
	Enabled     bool
	HTTPTimeout time.Duration
	AIConfig    AIConfig

	Social SocialAgentConfig
	Alias  AliasAgentConfig
	Grant  GrantAgentConfig
}

// SocialAgentConfig tunes the social presence investigator.
type SocialAgentConfig struct {
	Enabled   bool
	Providers []string
}

// AliasAgentConfig configures the alias hunter.
type AliasAgentConfig struct {
	Enabled        bool
	MinConfidence  float64
	MaxSuggestions int
}

// GrantAgentConfig configures the grant abuse agent.
type GrantAgentConfig struct {
	Enabled         bool
	LookbackDays    int
	RepeatThreshold int
}

// LoadAgentsConfig reads configuration values for the agent subsystem.
func LoadAgentsConfig(db *gorm.DB) AgentsConfig {
	aiCfg := LoadAIConfig(db)

	enabled := getBoolSetting("enable_agents", "ENABLE_AGENTS", true)
	socialEnabled := getBoolSetting("enable_agent_social", "ENABLE_AGENT_SOCIAL", true)
	aliasEnabled := getBoolSetting("enable_agent_alias", "ENABLE_AGENT_ALIAS", true)
	grantEnabled := getBoolSetting("enable_agent_grantwatch", "ENABLE_AGENT_GRANTWATCH", true)

	httpTimeout := 90 * time.Second
	if raw := shareddata.GetSetting("agents_http_timeout_seconds"); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
			httpTimeout = time.Duration(secs) * time.Second
		}
	}

	socialProviders := []string{"manual"}
	if raw := shareddata.GetSetting("agents_social_providers"); raw != "" {
		if parsed := parseCSV(raw); len(parsed) > 0 {
			socialProviders = parsed
		}
	}

	minConfidence := 0.6
	if raw := shareddata.GetSetting("agents_alias_min_confidence"); raw != "" {
		if val, err := strconv.ParseFloat(raw, 64); err == nil && val > 0 && val <= 1 {
			minConfidence = val
		}
	}

	maxSuggestions := 10
	if raw := shareddata.GetSetting("agents_alias_max_suggestions"); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val > 0 {
			maxSuggestions = val
		}
	}

	lookbackDays := 540
	if raw := shareddata.GetSetting("agents_grant_lookback_days"); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val > 0 {
			lookbackDays = val
		}
	}

	repeatThreshold := 3
	if raw := shareddata.GetSetting("agents_grant_repeat_threshold"); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val > 0 {
			repeatThreshold = val
		}
	}

	return AgentsConfig{
		Enabled:     enabled,
		HTTPTimeout: httpTimeout,
		AIConfig:    aiCfg,
		Social: SocialAgentConfig{
			Enabled:   socialEnabled,
			Providers: socialProviders,
		},
		Alias: AliasAgentConfig{
			Enabled:        aliasEnabled,
			MinConfidence:  minConfidence,
			MaxSuggestions: maxSuggestions,
		},
		Grant: GrantAgentConfig{
			Enabled:         grantEnabled,
			LookbackDays:    lookbackDays,
			RepeatThreshold: repeatThreshold,
		},
	}
}

func parseCSV(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			out = append(out, strings.ToLower(trimmed))
		}
	}
	return out
}
