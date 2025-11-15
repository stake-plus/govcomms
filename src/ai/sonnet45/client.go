package sonnet45

import (
	"github.com/stake-plus/govcomms/src/ai/anthropic"
	"github.com/stake-plus/govcomms/src/ai/core"
)

const defaultModel = "claude-3.5-sonnet-20241022"

func init() {
	core.RegisterProvider("sonnet-4.5", newClient)
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return anthropic.NewClient(cfg, defaultModel)
}
