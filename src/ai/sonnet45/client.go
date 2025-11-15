package sonnet

import (
	"github.com/stake-plus/govcomms/src/ai/anthropic"
	"github.com/stake-plus/govcomms/src/ai/core"
)

const defaultModel = "claude-3.5-sonnet-20241022"

func init() {
	core.RegisterProvider("claude", newClient)
	core.RegisterProvider("sonnet", newClient)
	core.RegisterProvider("anthropic", newClient)
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return anthropic.NewClient(cfg, defaultModel)
}
