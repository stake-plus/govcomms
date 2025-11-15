package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
	_ "github.com/stake-plus/govcomms/src/ai/providers"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	shareddata "github.com/stake-plus/govcomms/src/data"
)

var (
	providersFlag = flag.String("providers", "gpt5", "Comma-separated provider list or 'all'")
	modeFlag      = flag.String("mode", "respond", "respond|qa|both")
	systemFlag    = flag.String("system", "", "Override system prompt")
	modelFlag     = flag.String("model", "", "Override model name")
	promptFlag    = flag.String("prompt", defaultPrompt, "User prompt for respond mode")
	contentFlag   = flag.String("content", defaultContent, "Context content for QA mode")
	questionFlag  = flag.String("question", defaultQuestion, "Question for QA mode")
	timeoutFlag   = flag.Duration("timeout", 45*time.Second, "Per-provider timeout")
	tempFlag      = flag.Float64("temp", 0.2, "Completion temperature")
	webFlag       = flag.Bool("web", false, "Request web_search tool support")
	maxLenFlag    = flag.Int("max-bytes", 1200, "Maximum bytes of output to print per response (0=unlimited)")
)

var allProviders = []string{
	"gpt5",
	"gpt4o",
	"deepseek32",
	"sonnet45",
	"haiku45",
	"opus41",
	"grok4",
	"gemini25",
}

func main() {
	log.SetFlags(0)
	flag.Parse()

	aiCfg, closer := initAIConfig()
	if closer != nil {
		defer closer()
	}

	providers := resolveProviders(*providersFlag)
	if len(providers) == 0 {
		log.Fatal("no providers specified")
	}

	systemPrompt := pickFirst(*systemFlag, aiCfg.AISystemPrompt, defaultSystemPrompt)
	model := pickFirst(*modelFlag, aiCfg.AIModel, defaultModelForProvider(aiCfg.AIProvider))
	enableWeb := *webFlag || aiCfg.AIEnableWeb

	mode, err := parseMode(*modeFlag)
	if err != nil {
		log.Fatalf("invalid mode: %v", err)
	}

	for _, provider := range providers {
		if err := runProvider(provider, mode, model, systemPrompt, enableWeb, aiCfg); err != nil {
			log.Printf("[%s] ERROR: %v", provider, err)
		}
	}
}

func runProvider(provider string, mode runMode, model, systemPrompt string, enableWeb bool, aiCfg sharedconfig.AIConfig) error {
	cfg := aicore.FactoryConfig{
		Provider:     provider,
		SystemPrompt: systemPrompt,
		Model:        model,
		Temperature:  *tempFlag,
		OpenAIKey:    aiCfg.OpenAIKey,
		ClaudeKey:    aiCfg.ClaudeKey,
		GeminiKey:    aiCfg.GeminiKey,
		DeepSeekKey:  aiCfg.DeepSeekKey,
		GrokKey:      aiCfg.GrokKey,
		Extra:        map[string]string{},
	}
	if enableWeb {
		cfg.Extra["enable_web_search"] = "1"
	}

	client, err := aicore.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}

	fmt.Printf("=== %s ===\n", provider)
	if mode == modeRespond || mode == modeBoth {
		if err := executeRespondTest(client, provider, systemPrompt, model, enableWeb); err != nil {
			fmt.Printf("respond ❌ %v\n", err)
		}
	}
	if mode == modeQA || mode == modeBoth {
		if err := executeQATest(client, provider, systemPrompt, model); err != nil {
			fmt.Printf("qa ❌ %v\n", err)
		}
	}
	return nil
}

func executeRespondTest(client aicore.Client, provider, systemPrompt, model string, enableWeb bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	start := time.Now()
	opts := aicore.Options{
		Model:           model,
		SystemPrompt:    systemPrompt,
		Temperature:     *tempFlag,
		EnableWebSearch: enableWeb,
	}
	tools := []aicore.Tool{}
	if enableWeb {
		tools = append(tools, aicore.Tool{Type: "web_search"})
	}

	reply, err := client.Respond(ctx, *promptFlag, tools, opts)
	if err != nil {
		return err
	}
	fmt.Printf("respond ✅ (%.1fs)\n%s\n", time.Since(start).Seconds(), truncate(reply, *maxLenFlag))
	return nil
}

func executeQATest(client aicore.Client, provider, systemPrompt, model string) error {
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	start := time.Now()
	opts := aicore.Options{
		Model:        model,
		SystemPrompt: systemPrompt,
		Temperature:  *tempFlag,
	}
	reply, err := client.AnswerQuestion(ctx, *contentFlag, *questionFlag, opts)
	if err != nil {
		return err
	}
	fmt.Printf("qa ✅ (%.1fs)\n%s\n", time.Since(start).Seconds(), truncate(reply, *maxLenFlag))
	return nil
}

func resolveProviders(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.EqualFold(raw, "all") {
		return append([]string{}, allProviders...)
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	})
	var out []string
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func pickFirst(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func defaultModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "sonnet45", "haiku45":
		return "claude-3-haiku-20240307"
	case "gpt4o":
		return "gpt-4o-mini"
	default:
		return "gpt-5"
	}
}

func parseMode(input string) (runMode, error) {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "respond":
		return modeRespond, nil
	case "qa":
		return modeQA, nil
	case "both":
		return modeBoth, nil
	default:
		return modeRespond, errors.New("expected respond, qa, or both")
	}
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text[:limit]) + "...(truncated)"
}

type runMode int

const (
	modeRespond runMode = iota
	modeQA
	modeBoth
)

const (
	defaultPrompt  = "Summarize the major risks that a treasury committee should evaluate for this hypothetical proposal."
	defaultContent = `Proposal Title: Treasury Spend for Public Infrastructure

Summary:
- Allocate 500k DOT to upgrade regional validator hardware.
- Deploy new observability stack to monitor parachain liveness.
- Fund incident-response war room contractors for 12 months.`
	defaultQuestion = "What are the most significant technical dependencies and how could they delay the project?"
)

const defaultSystemPrompt = "You are a concise assistant that analyzes Polkadot governance referenda for internal operator testing."

func initAIConfig() (sharedconfig.AIConfig, func()) {
	envCfg := aiConfigFromEnv()
	cfg, closer, err := loadAIConfigFromDB()
	if err != nil {
		log.Printf("warning: falling back to env AI config: %v", err)
		return envCfg, nil
	}
	return mergeAIConfig(cfg, envCfg), closer
}

func loadAIConfigFromDB() (sharedconfig.AIConfig, func(), error) {
	dsn := shareddata.GetMySQLDSN()
	if strings.TrimSpace(dsn) == "" {
		return sharedconfig.AIConfig{}, nil, fmt.Errorf("MYSQL_DSN is not set")
	}

	db, err := shareddata.ConnectMySQL(dsn)
	if err != nil {
		return sharedconfig.AIConfig{}, nil, err
	}

	if err := shareddata.LoadSettings(db); err != nil {
		log.Printf("warning: settings load failed (env fallbacks still apply): %v", err)
	}

	closer := func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	}

	return sharedconfig.LoadAIConfig(db), closer, nil
}

func aiConfigFromEnv() sharedconfig.AIConfig {
	env := sharedconfig.LoadAIFromEnv()
	return sharedconfig.AIConfig{
		OpenAIKey:      env.OpenAIKey,
		ClaudeKey:      env.ClaudeKey,
		GeminiKey:      env.GeminiKey,
		DeepSeekKey:    env.DeepSeekKey,
		GrokKey:        env.GrokKey,
		AIProvider:     env.Provider,
		AISystemPrompt: env.SystemPrompt,
		AIModel:        env.Model,
		AIEnableWeb:    env.EnableWeb,
		AIEnableDeep:   env.EnableDeep,
	}
}

func mergeAIConfig(primary, fallback sharedconfig.AIConfig) sharedconfig.AIConfig {
	result := primary
	if strings.TrimSpace(result.OpenAIKey) == "" {
		result.OpenAIKey = fallback.OpenAIKey
	}
	if strings.TrimSpace(result.ClaudeKey) == "" {
		result.ClaudeKey = fallback.ClaudeKey
	}
	if strings.TrimSpace(result.GeminiKey) == "" {
		result.GeminiKey = fallback.GeminiKey
	}
	if strings.TrimSpace(result.DeepSeekKey) == "" {
		result.DeepSeekKey = fallback.DeepSeekKey
	}
	if strings.TrimSpace(result.GrokKey) == "" {
		result.GrokKey = fallback.GrokKey
	}
	if strings.TrimSpace(result.AIProvider) == "" {
		result.AIProvider = fallback.AIProvider
	}
	if strings.TrimSpace(result.AISystemPrompt) == "" {
		result.AISystemPrompt = fallback.AISystemPrompt
	}
	if strings.TrimSpace(result.AIModel) == "" {
		result.AIModel = fallback.AIModel
	}
	if !result.AIEnableWeb {
		result.AIEnableWeb = fallback.AIEnableWeb
	}
	if !result.AIEnableDeep {
		result.AIEnableDeep = fallback.AIEnableDeep
	}
	return result
}
