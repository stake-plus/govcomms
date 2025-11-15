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

	providers := resolveProviders(*providersFlag)
	if len(providers) == 0 {
		log.Fatal("no providers specified")
	}

	aiEnv := sharedconfig.LoadAIFromEnv()
	systemPrompt := pickFirst(*systemFlag, aiEnv.SystemPrompt, defaultSystemPrompt)
	model := pickFirst(*modelFlag, aiEnv.Model)

	mode, err := parseMode(*modeFlag)
	if err != nil {
		log.Fatalf("invalid mode: %v", err)
	}

	for _, provider := range providers {
		if err := runProvider(provider, mode, model, systemPrompt, aiEnv); err != nil {
			log.Printf("[%s] ERROR: %v", provider, err)
		}
	}
}

func runProvider(provider string, mode runMode, model, systemPrompt string, aiEnv sharedconfig.AI) error {
	cfg := aicore.FactoryConfig{
		Provider:     provider,
		SystemPrompt: systemPrompt,
		Model:        model,
		Temperature:  *tempFlag,
		OpenAIKey:    aiEnv.OpenAIKey,
		ClaudeKey:    aiEnv.ClaudeKey,
		GeminiKey:    aiEnv.GeminiKey,
		DeepSeekKey:  aiEnv.DeepSeekKey,
		GrokKey:      aiEnv.GrokKey,
		Extra:        map[string]string{},
	}
	if *webFlag {
		cfg.Extra["enable_web_search"] = "1"
	}

	client, err := aicore.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}

	fmt.Printf("=== %s ===\n", provider)
	if mode == modeRespond || mode == modeBoth {
		if err := executeRespondTest(client, provider); err != nil {
			fmt.Printf("respond ❌ %v\n", err)
		}
	}
	if mode == modeQA || mode == modeBoth {
		if err := executeQATest(client, provider); err != nil {
			fmt.Printf("qa ❌ %v\n", err)
		}
	}
	return nil
}

func executeRespondTest(client aicore.Client, provider string) error {
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	start := time.Now()
	opts := aicore.Options{
		Model:           pickFirst(*modelFlag),
		SystemPrompt:    pickFirst(*systemFlag, defaultSystemPrompt),
		Temperature:     *tempFlag,
		EnableWebSearch: *webFlag,
	}
	tools := []aicore.Tool{}
	if *webFlag {
		tools = append(tools, aicore.Tool{Type: "web_search"})
	}

	reply, err := client.Respond(ctx, *promptFlag, tools, opts)
	if err != nil {
		return err
	}
	fmt.Printf("respond ✅ (%.1fs)\n%s\n", time.Since(start).Seconds(), truncate(reply, *maxLenFlag))
	return nil
}

func executeQATest(client aicore.Client, provider string) error {
	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	start := time.Now()
	opts := aicore.Options{
		Model:        pickFirst(*modelFlag),
		SystemPrompt: pickFirst(*systemFlag, defaultSystemPrompt),
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

func init() {
	if _, present := os.LookupEnv("AI_SMOKETEST"); !present {
		// environment variable can be used to silence init logs in CI
	}
}
