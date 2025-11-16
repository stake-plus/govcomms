package gpt4omini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/stake-plus/govcomms/src/ai/core"
	"github.com/stake-plus/govcomms/src/webclient"
)

func init() {
	core.RegisterProvider("gpt4o", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.OpenAIKey == "" {
		return nil, fmt.Errorf("gpt4omini: OpenAI API key not configured")
	}

	return &client{
		apiKey:     cfg.OpenAIKey,
		httpClient: webclient.NewDefault(240 * time.Second),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, "gpt-4o"),
			Temperature:         orFloat(cfg.Temperature, 1),
			MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, 16000),
			SystemPrompt:        cfg.SystemPrompt,
		},
	}, nil
}

func (c *client) AnswerQuestion(ctx context.Context, content string, question string, opts core.Options) (string, error) {
	// Use Chat Completions
	merged := c.merge(opts)
	messages := []map[string]string{
		{"role": "system", "content": merged.SystemPrompt},
		{"role": "user", "content": fmt.Sprintf("Proposal Content:\n%s\n\nQuestion: %s\n\nProvide a direct, concise answer.", content, question)},
	}
	reqBody := map[string]interface{}{
		"model":       merged.Model,
		"messages":    messages,
		"temperature": merged.Temperature,
	}
	bodyBytes, _ := json.Marshal(reqBody)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, b, fmt.Errorf("status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("gpt4omini API error: %w", err)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}
	return result.Choices[0].Message.Content, nil
}

func buildInputBlocks(text string) []map[string]any {
	return []map[string]any{
		{
			"role": "user",
			"content": []map[string]any{
				{
					"type": "input_text",
					"text": text,
				},
			},
		},
	}
}

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	// Use Responses API with optional tools like web_search
	merged := c.merge(opts)
	payload := map[string]interface{}{
		"model":             merged.Model,
		"input":             buildInputBlocks(input),
		"temperature":       merged.Temperature,
		"max_output_tokens": merged.MaxCompletionTokens,
	}
	var toolMap map[string]core.Tool
	var forcedTool string
	if len(tools) > 0 {
		var toolPayload []map[string]interface{}
		toolPayload, toolMap, forcedTool = buildToolsPayload(tools)
		if len(toolPayload) > 0 {
			payload["tools"] = toolPayload
			payload["tool_choice"] = buildToolChoice(forcedTool)
		}
	}
	bodyBytes, _ := json.Marshal(payload)
	_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(bodyBytes))
		if err != nil {
			return 0, nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, b, fmt.Errorf("status %d", resp.StatusCode)
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("gpt4omini API error: %w", err)
	}
	return c.handleResponse(ctx, body, toolMap)
}

func (c *client) merge(opts core.Options) core.Options {
	out := c.defaults
	if opts.Model != "" {
		out.Model = opts.Model
	}
	if opts.Temperature != 0 {
		out.Temperature = opts.Temperature
	}
	if opts.MaxCompletionTokens != 0 {
		out.MaxCompletionTokens = opts.MaxCompletionTokens
	}
	if opts.SystemPrompt != "" {
		out.SystemPrompt = opts.SystemPrompt
	}
	return out
}

func valueOrDefault(val, def string) string {
	if val != "" {
		return val
	}
	return def
}
func orInt(v, d int) int {
	if v != 0 {
		return v
	}
	return d
}
func orFloat(v, d float64) float64 {
	if v != 0 {
		return v
	}
	return d
}

func buildToolsPayload(tools []core.Tool) ([]map[string]interface{}, map[string]core.Tool, string) {
	out := []map[string]interface{}{}
	toolMap := map[string]core.Tool{}
	var forced string
	for idx, t := range tools {
		switch strings.ToLower(t.Type) {
		case "web_search":
			out = append(out, map[string]interface{}{"type": "web_search"})
		case "mcp_referenda":
			name := t.Name
			if strings.TrimSpace(name) == "" {
				name = fmt.Sprintf("mcp_referendum_%d", idx+1)
			}
			funcDef := map[string]interface{}{
				"name":        name,
				"description": t.Description,
			}
			if t.Parameters != nil {
				funcDef["parameters"] = t.Parameters
			}
			tool := map[string]interface{}{
				"type":     "function",
				"name":     name,
				"function": funcDef,
			}
			if t.Description != "" {
				tool["description"] = t.Description
			}
			out = append(out, tool)
			toolCopy := t
			toolCopy.Name = name
			toolMap[name] = toolCopy
			forced = name
		}
	}
	return out, toolMap, forced
}

func (c *client) handleResponse(ctx context.Context, body []byte, toolMap map[string]core.Tool) (string, error) {
	raw := body
	for {
		var envelope openAIResponse
		if err := json.Unmarshal(raw, &envelope); err != nil {
			log.Printf("gpt4o: failed to decode response body=%s", truncatePayload(raw, 1024))
			return "", err
		}
		if calls := pendingToolCalls(envelope); len(calls) > 0 {
			log.Printf("gpt4o: requires action id=%s status=%s", envelope.ID, envelope.Status)
			outputs, err := c.executeToolCalls(ctx, calls, toolMap)
			if err != nil {
				return "", err
			}
			nextBody, err := c.submitToolOutputs(ctx, envelope.ID, outputs)
			if err != nil {
				return "", err
			}
			raw = nextBody
			continue
		}

		switch envelope.Status {
		case "completed":
			if text := extractResponseText(envelope); text != "" {
				return text, nil
			}
			log.Printf("gpt4o: empty response raw=%s", truncatePayload(raw, 2048))
			return "", fmt.Errorf("gpt4omini: empty response")
		case "requires_action":
			return "", fmt.Errorf("gpt4omini: required action missing tool outputs")
		case "queued", "in_progress":
			time.Sleep(500 * time.Millisecond)
			nextBody, err := c.fetchResponse(ctx, envelope.ID)
			if err != nil {
				return "", err
			}
			raw = nextBody
		default:
			return "", fmt.Errorf("gpt4omini: unexpected status %s", envelope.Status)
		}
	}
}

func (c *client) executeToolCalls(ctx context.Context, calls []openAIToolCall, toolMap map[string]core.Tool) ([]toolOutput, error) {
	outputs := make([]toolOutput, 0, len(calls))
	for _, call := range calls {
		toolDef, ok := toolMap[call.Function.Name]
		if !ok {
			return nil, fmt.Errorf("gpt4omini: unknown tool %s", call.Function.Name)
		}
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			log.Printf("gpt4o: tool %s arg parse error: %v", call.Function.Name, err)
			outputs = append(outputs, toolOutput{
				ToolCallID: call.ID,
				Output:     fmt.Sprintf(`{"error":"invalid arguments: %s"}`, sanitizeToolError(err)),
			})
			continue
		}
		rawArgs := copyArgs(args)
		args = mergeArgs(args, toolDef.Defaults)
		log.Printf("gpt4o: tool call %s raw=%v merged=%v", call.Function.Name, rawArgs, args)
		result, execErr := c.dispatchTool(ctx, toolDef, args)
		if execErr != nil {
			log.Printf("gpt4o: tool %s error: %v", call.Function.Name, execErr)
			result = fmt.Sprintf(`{"error":"%s"}`, sanitizeToolError(execErr))
		}
		log.Printf("gpt4o: tool %s output=%s", call.Function.Name, truncatePayload([]byte(result), 256))
		outputs = append(outputs, toolOutput{
			ToolCallID: call.ID,
			Output:     result,
		})
	}
	return outputs, nil
}

func (c *client) dispatchTool(ctx context.Context, toolDef core.Tool, args map[string]any) (string, error) {
	switch strings.ToLower(toolDef.Type) {
	case "mcp_referenda":
		return c.invokeMCP(ctx, toolDef.MCP, args)
	default:
		return "", fmt.Errorf("unsupported tool %s", toolDef.Type)
	}
}

func (c *client) invokeMCP(ctx context.Context, desc *core.MCPDescriptor, args map[string]any) (string, error) {
	if desc == nil || strings.TrimSpace(desc.BaseURL) == "" {
		return "", fmt.Errorf("mcp descriptor missing")
	}
	network := strings.TrimSpace(fmt.Sprint(args["network"]))
	if network == "" {
		return "", fmt.Errorf("mcp: network argument required")
	}
	refIDRaw, ok := args["refId"]
	if !ok {
		return "", fmt.Errorf("mcp: refId argument required")
	}
	refID, err := parseUint(refIDRaw)
	if err != nil {
		return "", fmt.Errorf("mcp: invalid refId: %w", err)
	}
	resource := strings.TrimSpace(fmt.Sprint(args["resource"]))
	resource = strings.ToLower(resource)
	base := strings.TrimRight(desc.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/v1/referenda/%s/%d", base, url.PathEscape(network), refID)
	if resource != "" && resource != "metadata" {
		endpoint += "/" + url.PathEscape(resource)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if desc.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+desc.AuthToken)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mcp: status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

func (c *client) submitToolOutputs(ctx context.Context, responseID string, outputs []toolOutput) ([]byte, error) {
	payload := map[string]any{
		"tool_outputs": outputs,
	}
	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("https://api.openai.com/v1/responses/%s/submit_tool_outputs", responseID),
		bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gpt4omini: submit tool outputs status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *client) fetchResponse(ctx context.Context, responseID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.openai.com/v1/responses/%s", responseID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gpt4omini: fetch response status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func extractResponseText(resp openAIResponse) string {
	for _, o := range resp.Output {
		if text := extractTextFromContent(o.Content); text != "" {
			return text
		}
	}
	if resp.OutputText != "" {
		return resp.OutputText
	}
	for _, msg := range resp.Messages {
		if strings.EqualFold(msg.Role, "assistant") {
			if text := extractTextFromContent(msg.Content); text != "" {
				return text
			}
		}
	}
	return ""
}

func extractTextFromContent(chunks []openAIMessageContent) string {
	for _, c := range chunks {
		if strings.TrimSpace(c.Text) != "" {
			return c.Text
		}
	}
	return ""
}

func parseUint(value any) (uint64, error) {
	switch v := value.(type) {
	case float64:
		return uint64(v), nil
	case uint64:
		return v, nil
	case uint32:
		return uint64(v), nil
	case uint:
		return uint64(v), nil
	case int:
		return uint64(v), nil
	case int64:
		return uint64(v), nil
	case string:
		return strconv.ParseUint(v, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported numeric type %T", value)
	}
}

type openAIResponse struct {
	ID             string               `json:"id"`
	Status         string               `json:"status"`
	Output         []openAIOutput       `json:"output"`
	OutputText     string               `json:"output_text"`
	RequiredAction *requiredActionBlock `json:"required_action"`
	Messages       []openAIMessage      `json:"messages"`
}

type openAIOutput struct {
	Content []openAIMessageContent `json:"content"`
}

type openAIMessage struct {
	Role    string                 `json:"role"`
	Content []openAIMessageContent `json:"content"`
}

type openAIMessageContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type requiredActionBlock struct {
	Type              string                  `json:"type"`
	SubmitToolOutputs *submitToolOutputsBlock `json:"submit_tool_outputs"`
}

type submitToolOutputsBlock struct {
	ToolCalls []openAIToolCall `json:"tool_calls"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type toolOutput struct {
	ToolCallID string `json:"tool_call_id"`
	Output     string `json:"output"`
}

func sanitizeToolError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func decodeToolArguments(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return map[string]any{}, nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return nil, err
	}
	return args, nil
}

func mergeArgs(args map[string]any, defaults map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	for k, v := range defaults {
		if _, exists := args[k]; !exists {
			args[k] = v
		}
	}
	return args
}

func truncatePayload(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "... (truncated)"
}

func buildToolChoice(forced string) any {
	if strings.TrimSpace(forced) == "" {
		return "auto"
	}
	return map[string]any{
		"type": "function",
		"name": forced,
	}
}

func copyArgs(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func pendingToolCalls(resp openAIResponse) []openAIToolCall {
	if resp.RequiredAction == nil || resp.RequiredAction.SubmitToolOutputs == nil {
		return nil
	}
	if len(resp.RequiredAction.SubmitToolOutputs.ToolCalls) == 0 {
		return nil
	}
	return resp.RequiredAction.SubmitToolOutputs.ToolCalls
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(fmt.Sprintf(`{"marshal_error":"%s"}`, sanitizeToolError(err)))
	}
	return b
}
