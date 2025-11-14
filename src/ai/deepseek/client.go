package deepseek

import (
	"context"
	"fmt"

	"github.com/stake-plus/govcomms/src/ai/core"
)

func init() {
	core.RegisterProvider("deepseek", newClient)
}

type client struct{}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.DeepSeekKey == "" {
		return nil, fmt.Errorf("deepseek: API key not configured")
	}
	return nil, fmt.Errorf("deepseek provider integration not implemented yet")
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	return "", fmt.Errorf("deepseek provider not implemented")
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	return "", fmt.Errorf("deepseek provider not implemented")
}
