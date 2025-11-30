package actions

import (
	"context"
	"fmt"
	"log"

	feedbackmodule "github.com/stake-plus/govcomms/src/actions/feedback"
	questionmodule "github.com/stake-plus/govcomms/src/actions/question"
	reportsmodule "github.com/stake-plus/govcomms/src/actions/reports"
	sharedconfig "github.com/stake-plus/govcomms/src/data/config"
	"gorm.io/gorm"
)

// StartAll wires up enabled action modules and starts the manager.
func StartAll(ctx context.Context, db *gorm.DB) (*Manager, error) {
	mgr := NewManager()

	// Initialize reports module first (if enabled) so we can pass it to question module
	var reportsMod *reportsmodule.Module
	reportsCfg := sharedconfig.LoadReportsConfig(db)
	log.Printf("actions: reports module config - Enabled: %v, TempDir: %s", reportsCfg.Enabled, reportsCfg.TempDir)
	if reportsCfg.Enabled {
		mod, err := reportsmodule.NewModule(&reportsCfg, db)
		if err != nil {
			log.Printf("actions: reports module initialization failed: %v", err)
			return nil, fmt.Errorf("actions: init reports module: %w", err)
		}
		reportsMod = mod
		log.Printf("actions: reports module initialized successfully")
		if err := mgr.Add(mod); err != nil {
			return nil, fmt.Errorf("actions: add reports module: %w", err)
		}
	} else {
		log.Printf("actions: reports module disabled via configuration")
	}

	qaCfg := sharedconfig.LoadQAConfig(db)
	if qaCfg.Enabled {
		// Pass reports module to question module if available
		var mod *questionmodule.Module
		var err error
		if reportsMod != nil {
			log.Printf("actions: passing reports module to question module")
			mod, err = questionmodule.NewModuleWithReports(&qaCfg, db, reportsMod, reportsMod)
		} else {
			log.Printf("actions: reports module not available, creating question module without reports")
			mod, err = questionmodule.NewModule(&qaCfg, db)
		}
		if err != nil {
			return nil, fmt.Errorf("actions: init question module: %w", err)
		}
		if err := mgr.Add(mod); err != nil {
			return nil, fmt.Errorf("actions: add question module: %w", err)
		}
	} else {
		log.Printf("actions: QA module disabled via configuration")
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
