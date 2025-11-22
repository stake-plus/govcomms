package config

import (
	"strconv"
	"strings"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
	shareddata "github.com/stake-plus/govcomms/src/data"
	"gorm.io/gorm"
)

// AIConfig holds AI-related configuration
type AIConfig struct {
	OpenAIKey      string
	ClaudeKey      string
	GeminiKey      string
	DeepSeekKey    string
	GrokKey        string
	AIProvider     string
	AISystemPrompt string
	AIModel        string
	AIEnableWeb    bool
	AIEnableDeep   bool
	Consensus      ConsensusConfig
}

// ConsensusConfig enumerates the council of models and thresholds used by the
// consensus provider/orchestrator.
type ConsensusConfig struct {
	Researchers   []string
	Reviewers     []string
	Voters        []string
	Agreement     float64
	DebateRounds  int
	MaxRoundDelay int
}

// LoadAIConfig loads AI configuration
func LoadAIConfig(db *gorm.DB) AIConfig {
	openAIKey := GetSetting("openai_api_key", "OPENAI_API_KEY", "")
	claudeKey := GetSetting("claude_api_key", "CLAUDE_API_KEY", "")
	geminiKey := GetSetting("gemini_api_key", "GEMINI_API_KEY", "")
	deepSeekKey := GetSetting("deepseek_api_key", "DEEPSEEK_API_KEY", "")
	grokKey := GetSetting("grok_api_key", "GROK_API_KEY", "")
	aiProvider := GetSetting("ai_provider", "AI_PROVIDER", "gpt51")

	aiSystemPrompt := GetSetting("ai_system_prompt", "AI_SYSTEM_PROMPT",
		`You are a helpful assistant that analyzes Polkadot/Kusama governance proposals and answers questions about them. 
Provide clear, concise, and accurate information based on the proposal content provided. 
Focus on facts from the proposal and avoid speculation. 
If information is not available in the provided content, clearly state that.`)

	aiModel := GetSetting("ai_model", "AI_MODEL", "")
	aiEnableWeb := shareddata.GetSetting("ai_enable_web_search") == "1"
	aiEnableDeep := shareddata.GetSetting("ai_enable_deep_search") == "1"

	cfg := AIConfig{
		OpenAIKey:      openAIKey,
		ClaudeKey:      claudeKey,
		GeminiKey:      geminiKey,
		DeepSeekKey:    deepSeekKey,
		GrokKey:        grokKey,
		AIProvider:     aiProvider,
		AISystemPrompt: aiSystemPrompt,
		AIModel:        aiModel,
		AIEnableWeb:    aiEnableWeb,
		AIEnableDeep:   aiEnableDeep,
	}

	consensus := buildConsensusConfig(cfg)
	cfg.Consensus = consensus

	return cfg
}

func buildConsensusConfig(base AIConfig) ConsensusConfig {
	researchers := parseCSV(GetSetting("ai_consensus_researchers", "AI_CONSENSUS_RESEARCHERS", ""))
	reviewers := parseCSV(GetSetting("ai_consensus_reviewers", "AI_CONSENSUS_REVIEWERS", ""))
	voters := parseCSV(GetSetting("ai_consensus_voters", "AI_CONSENSUS_VOTERS", ""))

	auto := func(list []string) []string {
		if len(list) > 0 {
			return list
		}
		return defaultConsensusParticipants(base)
	}

	researchers = auto(researchers)
	if len(reviewers) == 0 {
		reviewers = append([]string{}, researchers...)
	}
	if len(voters) == 0 {
		voters = append([]string{}, reviewers...)
	}

	agreement := 0.67
	if raw := GetSetting("ai_consensus_agreement", "AI_CONSENSUS_AGREEMENT", ""); raw != "" {
		if val, err := strconv.ParseFloat(raw, 64); err == nil && val >= 0.5 && val <= 1 {
			agreement = val
		}
	}

	rounds := 1
	if raw := GetSetting("ai_consensus_rounds", "AI_CONSENSUS_ROUNDS", ""); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val > 0 {
			rounds = val
		}
	}

	maxDelay := 120
	if raw := GetSetting("ai_consensus_round_delay", "AI_CONSENSUS_ROUND_DELAY", ""); raw != "" {
		if val, err := strconv.Atoi(raw); err == nil && val >= 30 {
			maxDelay = val
		}
	}

	return ConsensusConfig{
		Researchers:   uniqueStrings(researchers),
		Reviewers:     uniqueStrings(reviewers),
		Voters:        uniqueStrings(voters),
		Agreement:     agreement,
		DebateRounds:  rounds,
		MaxRoundDelay: maxDelay,
	}
}

func defaultConsensusParticipants(cfg AIConfig) []string {
	var participants []string
	add := func(val string) {
		for _, existing := range participants {
			if existing == val {
				return
			}
		}
		participants = append(participants, val)
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
	return participants
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(v))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

// FactoryConfig returns a ready-to-use ai/core factory payload with the current
// configuration embedded, including consensus metadata in the Extra map.
func (cfg AIConfig) FactoryConfig() aicore.FactoryConfig {
	return aicore.FactoryConfig{
		Provider:            cfg.AIProvider,
		SystemPrompt:        cfg.AISystemPrompt,
		Model:               cfg.AIModel,
		OpenAIKey:           cfg.OpenAIKey,
		ClaudeKey:           cfg.ClaudeKey,
		GeminiKey:           cfg.GeminiKey,
		DeepSeekKey:         cfg.DeepSeekKey,
		GrokKey:             cfg.GrokKey,
		MaxCompletionTokens: 0,
		Extra:               cfg.extraSettings(),
	}
}

func (cfg AIConfig) extraSettings() map[string]string {
	extra := map[string]string{}
	if cfg.AIEnableWeb {
		extra["enable_web_search"] = "1"
	}
	if cfg.AIEnableDeep {
		extra["enable_deep_search"] = "1"
	}
	if len(cfg.Consensus.Researchers) > 0 {
		extra["consensus_researchers"] = strings.Join(cfg.Consensus.Researchers, ",")
	}
	if len(cfg.Consensus.Reviewers) > 0 {
		extra["consensus_reviewers"] = strings.Join(cfg.Consensus.Reviewers, ",")
	}
	if len(cfg.Consensus.Voters) > 0 {
		extra["consensus_voters"] = strings.Join(cfg.Consensus.Voters, ",")
	}
	if cfg.Consensus.Agreement > 0 {
		extra["consensus_agreement"] = strconv.FormatFloat(cfg.Consensus.Agreement, 'f', 2, 64)
	}
	if cfg.Consensus.DebateRounds > 0 {
		extra["consensus_rounds"] = strconv.Itoa(cfg.Consensus.DebateRounds)
	}
	if cfg.Consensus.MaxRoundDelay > 0 {
		extra["consensus_round_delay"] = strconv.Itoa(cfg.Consensus.MaxRoundDelay)
	}
	return extra
}

// QAConfig holds AI Q&A bot configuration
type QAConfig struct {
	Base
	AIConfig
	QARoleID string
	TempDir  string
	Enabled  bool
}

// LoadQAConfig loads Q&A bot configuration
func LoadQAConfig(db *gorm.DB) QAConfig {
	base := LoadBase(db)
	ai := LoadAIConfig(db)
	qaRoleID := GetSetting("qa_role_id", "QA_ROLE_ID", "")
	tempDir := GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")
	enabled := getBoolSetting("enable_qa", "ENABLE_QA", true)

	return QAConfig{
		Base:     base,
		AIConfig: ai,
		QARoleID: qaRoleID,
		TempDir:  tempDir,
		Enabled:  enabled,
	}
}

// ResearchConfig holds Research bot configuration
type ResearchConfig struct {
	Base
	AIConfig
	ResearchRoleID string
	TempDir        string
	Enabled        bool
}

// LoadResearchConfig loads Research bot configuration
func LoadResearchConfig(db *gorm.DB) ResearchConfig {
	base := LoadBase(db)
	ai := LoadAIConfig(db)
	researchRoleID := GetSetting("research_role_id", "RESEARCH_ROLE_ID", "")
	tempDir := GetSetting("research_temp_dir", "RESEARCH_TEMP_DIR", "")
	if tempDir == "" {
		tempDir = GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")
	}
	enabled := getBoolSetting("enable_research", "ENABLE_RESEARCH", true)

	return ResearchConfig{
		Base:           base,
		AIConfig:       ai,
		ResearchRoleID: researchRoleID,
		TempDir:        tempDir,
		Enabled:        enabled,
	}
}

// FeedbackConfig holds Feedback bot configuration
type FeedbackConfig struct {
	Base
	FeedbackRoleID         string
	IndexerWorkers         int
	IndexerIntervalMinutes int
	PolkassemblyEndpoint   string
	Enabled                bool
}

// MCPConfig holds configuration for the local MCP server.
type MCPConfig struct {
	Enabled   bool
	Listen    string
	AuthToken string
	CacheDir  string
}

// LoadFeedbackConfig loads Feedback bot configuration
func LoadFeedbackConfig(db *gorm.DB) FeedbackConfig {
	base := LoadBase(db)
	feedbackRoleID := GetSetting("feedback_role_id", "FEEDBACK_ROLE_ID", "")
	polkassemblyEndpoint := GetSetting("polkassembly_endpoint", "POLKASSEMBLY_ENDPOINT", "https://api.polkassembly.io/api/v1")

	workers := 10
	if workerStr := shareddata.GetSetting("indexer_workers"); workerStr != "" {
		if w, err := strconv.Atoi(workerStr); err == nil {
			workers = w
		}
	}

	intervalMinutes := 60
	if intervalStr := shareddata.GetSetting("indexer_interval_minutes"); intervalStr != "" {
		if i, err := strconv.Atoi(intervalStr); err == nil {
			intervalMinutes = i
		}
	}
	enabled := getBoolSetting("enable_feedback", "ENABLE_FEEDBACK", true)

	return FeedbackConfig{
		Base:                   base,
		FeedbackRoleID:         feedbackRoleID,
		IndexerWorkers:         workers,
		IndexerIntervalMinutes: intervalMinutes,
		PolkassemblyEndpoint:   polkassemblyEndpoint,
		Enabled:                enabled,
	}
}

// LoadMCPConfig loads configuration for the MCP server.
func LoadMCPConfig(db *gorm.DB) MCPConfig {
	enabled := getBoolSetting("enable_mcp", "ENABLE_MCP", true)
	listen := GetSetting("mcp_listen_addr", "MCP_LISTEN_ADDR", "127.0.0.1:7081")
	authToken := GetSetting("mcp_auth_token", "MCP_AUTH_TOKEN", "")

	defaultCache := GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")
	cacheDir := GetSetting("mcp_cache_dir", "MCP_CACHE_DIR", defaultCache)

	return MCPConfig{
		Enabled:   enabled,
		Listen:    listen,
		AuthToken: authToken,
		CacheDir:  cacheDir,
	}
}

// ReportsConfig holds Reports bot configuration
type ReportsConfig struct {
	Base
	AIConfig
	ReportsRoleID string
	TempDir        string
	Enabled        bool
}

// LoadReportsConfig loads Reports bot configuration
func LoadReportsConfig(db *gorm.DB) ReportsConfig {
	base := LoadBase(db)
	ai := LoadAIConfig(db)
	reportsRoleID := GetSetting("reports_role_id", "REPORTS_ROLE_ID", "")
	tempDir := GetSetting("reports_temp_dir", "REPORTS_TEMP_DIR", "")
	if tempDir == "" {
		tempDir = GetSetting("qa_temp_dir", "QA_TEMP_DIR", "/tmp/govcomms-qa")
	}
	enabled := getBoolSetting("enable_reports", "ENABLE_REPORTS", true)

	return ReportsConfig{
		Base:          base,
		AIConfig:      ai,
		ReportsRoleID: reportsRoleID,
		TempDir:       tempDir,
		Enabled:       enabled,
	}
}
