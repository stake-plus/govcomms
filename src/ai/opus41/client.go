package opus41

import (
	"github.com/stake-plus/govcomms/src/ai/anthropic"
	"github.com/stake-plus/govcomms/src/ai/core"
)

const defaultModel = "claude-3.5-opus-20241022"

func init() {
	core.RegisterProvider("opus", newClient)
	core.RegisterProvider("opus41", newClient)
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return anthropic.NewClient(cfg, defaultModel)
}
