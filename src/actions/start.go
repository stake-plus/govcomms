package actions

import (
	"context"
	"fmt"
	"log"

	feedbackmodule "github.com/stake-plus/govcomms/src/actions/feedback"
	questionmodule "github.com/stake-plus/govcomms/src/actions/question"
	researchmodule "github.com/stake-plus/govcomms/src/actions/research"
	sharedconfig "github.com/stake-plus/govcomms/src/config"
	"gorm.io/gorm"
)

// StartAll wires up enabled action modules and starts the manager.
func StartAll(ctx context.Context, db *gorm.DB) (*Manager, error) {
	mgr := NewManager()

	qaCfg := sharedconfig.LoadQAConfig(db)
	if qaCfg.Enabled {
		mod, err := questionmodule.NewModule(&qaCfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init question module: %w", err)
		}
		if err := mgr.Add(mod); err != nil {
			return nil, fmt.Errorf("actions: add question module: %w", err)
		}
	} else {
		log.Printf("actions: QA module disabled via configuration")
	}

	researchCfg := sharedconfig.LoadResearchConfig(db)
	if researchCfg.Enabled {
		mod, err := researchmodule.NewModule(&researchCfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init research module: %w", err)
		}
		if err := mgr.Add(mod); err != nil {
			return nil, fmt.Errorf("actions: add research module: %w", err)
		}
	} else {
		log.Printf("actions: research module disabled via configuration")
	}

	feedbackCfg := sharedconfig.LoadFeedbackConfig(db)
	if feedbackCfg.Enabled {
		mod, err := feedbackmodule.NewModule(&feedbackCfg, db)
		if err != nil {
			return nil, fmt.Errorf("actions: init feedback module: %w", err)
		}
		if err := mgr.Add(mod); err != nil {
			return nil, fmt.Errorf("actions: add feedback module: %w", err)
		}
	} else {
		log.Printf("actions: feedback module disabled via configuration")
	}

	if err := mgr.Start(ctx); err != nil {
		return nil, err
	}

	return mgr, nil
}
