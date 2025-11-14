package actions

import (
	"context"
	"fmt"

	feedbackmodule "github.com/stake-plus/govcomms/src/actions/feedback"
	questionmodule "github.com/stake-plus/govcomms/src/actions/question"
	researchmodule "github.com/stake-plus/govcomms/src/actions/research"
	sharedconfig "github.com/stake-plus/govcomms/src/shared/config"
	"gorm.io/gorm"
)

// Options describes which action modules should run.
type Options struct {
	EnableQA       bool
	EnableResearch bool
	EnableFeedback bool
}

// StartAll wires up enabled action modules and starts the manager.
func StartAll(ctx context.Context, db *gorm.DB, opts Options) (*Manager, error) {
	mgr := NewManager()

	if opts.EnableQA {
		cfg := sharedconfig.LoadQAConfig(db)
		mod, err := questionmodule.NewModule(&cfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init question module: %w", err)
		}
		mgr.Add(mod)
	}

	if opts.EnableResearch {
		cfg := sharedconfig.LoadResearchConfig(db)
		mod, err := researchmodule.NewModule(&cfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init research module: %w", err)
		}
		mgr.Add(mod)
	}

	if opts.EnableFeedback {
		cfg := sharedconfig.LoadFeedbackConfig(db)
		mod, err := feedbackmodule.NewModule(&cfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init feedback module: %w", err)
		}
		mgr.Add(mod)
	}

	if err := mgr.Start(ctx); err != nil {
		return nil, err
	}

	return mgr, nil
}
