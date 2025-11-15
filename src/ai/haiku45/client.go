package haiku45

import (
	"github.com/stake-plus/govcomms/src/ai/anthropic"
	"github.com/stake-plus/govcomms/src/ai/core"
)

const defaultModel = "claude-3.5-haiku-20241022"

func init() {
	core.RegisterProvider("haiku", newClient)
	core.RegisterProvider("haiku45", newClient)
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return anthropic.NewClient(cfg, defaultModel)
}
