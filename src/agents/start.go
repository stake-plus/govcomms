package agents

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/stake-plus/govcomms/src/agents/aliashunter"
	agentcore "github.com/stake-plus/govcomms/src/agents/core"
	"github.com/stake-plus/govcomms/src/agents/grantwatch"
	"github.com/stake-plus/govcomms/src/agents/socialpresence"
	aicore "github.com/stake-plus/govcomms/src/ai/core"
	_ "github.com/stake-plus/govcomms/src/ai/providers"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	"github.com/stake-plus/govcomms/src/webclient"
	"gorm.io/gorm"
)

// StartAll wires up enabled agents and returns the manager. When agents are
// globally disabled it returns (nil, nil).
func StartAll(ctx context.Context, db *gorm.DB) (*Manager, error) {
	cfg := sharedconfig.LoadAgentsConfig(db)
	if !cfg.Enabled {
		log.Printf("agents: disabled via configuration")
		return nil, nil
	}

	logger := log.New(os.Stdout, "[agents] ", log.LstdFlags|log.Lmsgprefix)
	aiClient := buildAIClient(cfg.AIConfig, logger)

	deps := agentcore.RuntimeDeps{
		DB:     db,
		HTTP:   webclient.NewDefault(cfg.HTTPTimeout),
		AI:     aiClient,
		Logger: logger,
	}

	manager := agentcore.NewManager()

	if cfg.Social.Enabled {
		agent := socialpresence.NewAgent(socialpresence.Config{
			Providers: cfg.Social.Providers,
		}, deps)
		if err := manager.Add(agent); err != nil {
			return nil, fmt.Errorf("agents: social presence: %w", err)
		}
	} else {
		logger.Printf("agents: social presence agent disabled")
	}

	if cfg.Alias.Enabled {
		agent := aliashunter.NewAgent(aliashunter.Config{
			MinConfidence:  cfg.Alias.MinConfidence,
			MaxSuggestions: cfg.Alias.MaxSuggestions,
		}, deps)
		if err := manager.Add(agent); err != nil {
			return nil, fmt.Errorf("agents: alias hunter: %w", err)
		}
	} else {
		logger.Printf("agents: alias hunter agent disabled")
	}

	if cfg.Grant.Enabled {
		agent := grantwatch.NewAgent(grantwatch.Config{
			LookbackDays:    cfg.Grant.LookbackDays,
			RepeatThreshold: cfg.Grant.RepeatThreshold,
		}, deps)
		if err := manager.Add(agent); err != nil {
			return nil, fmt.Errorf("agents: grant watch: %w", err)
		}
	} else {
		logger.Printf("agents: grant watch agent disabled")
	}

	if err := manager.Start(ctx); err != nil {
		return nil, err
	}

	return manager, nil
}

func buildAIClient(cfg sharedconfig.AIConfig, logger *log.Logger) aicore.Client {
	client, err := aicore.NewClient(aicore.FactoryConfig{
		Provider:            cfg.AIProvider,
		SystemPrompt:        cfg.AISystemPrompt,
		Model:               cfg.AIModel,
		OpenAIKey:           cfg.OpenAIKey,
		ClaudeKey:           cfg.ClaudeKey,
		Extra:               map[string]string{},
		MaxCompletionTokens: 2048,
	})
	if err != nil && logger != nil {
		logger.Printf("agents: ai client unavailable: %v", err)
	}
	return client
}
