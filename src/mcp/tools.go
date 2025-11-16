package mcp

import (
	"strings"

	aicore "github.com/stake-plus/govcomms/src/ai/core"
)

// NewReferendaTool returns an MCP-aware tool definition for referendum data.
func NewReferendaTool(baseURL, authToken string) *aicore.Tool {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		return nil
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")

	return &aicore.Tool{
		Type:        "mcp_referenda",
		Name:        "fetch_referendum_data",
		Description: "Fetch cached Polkadot/Kusama referendum data from GovComms. Provide `network` (e.g. polkadot), `refId` (integer), optional `resource` (metadata|content|attachments), and optional `file` name when retrieving attachment bytes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"network": map[string]any{
					"type":        "string",
					"description": "Network slug such as polkadot, kusama, collectives.",
				},
				"refId": map[string]any{
					"type":        "integer",
					"description": "Referendum identifier.",
				},
				"resource": map[string]any{
					"type":        "string",
					"description": "Optional data segment to fetch (metadata, content, attachments). Defaults to metadata.",
					"enum":        []string{"metadata", "content", "attachments"},
				},
				"file": map[string]any{
					"type":        "string",
					"description": "Optional attachment path returned in metadata (only used when resource=attachments) to download the file content.",
				},
			},
			"required": []string{"network", "refId"},
		},
		MCP: &aicore.MCPDescriptor{
			BaseURL:   base,
			AuthToken: authToken,
		},
	}
}
