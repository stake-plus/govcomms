package gpt51

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
	core.RegisterProvider("gpt51", newClient)
}

type client struct {
	apiKey     string
	httpClient *http.Client
	defaults   core.Options
}

func newClient(cfg core.FactoryConfig) (core.Client, error) {
	if cfg.OpenAIKey == "" {
		return nil, fmt.Errorf("gpt51: OpenAI API key not configured")
	}

	return &client{
		apiKey:     cfg.OpenAIKey,
		httpClient: webclient.NewDefault(240 * time.Second),
		defaults: core.Options{
			Model:               valueOrDefault(cfg.Model, "gpt-5.1"),
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
			return resp.StatusCode, b, fmt.Errorf("status %d: %s", resp.StatusCode, truncatePayload(b, 512))
		}
		return resp.StatusCode, b, nil
	})
	if err != nil {
		return "", fmt.Errorf("gpt51 API error: %w", err)
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

func (c *client) Respond(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	merged := c.merge(opts)
	return c.respondWithChatTools(ctx, input, tools, merged)
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

func (c *client) executeToolCalls(ctx context.Context, calls []openAIToolCall, toolMap map[string]core.Tool) ([]toolOutput, error) {
	outputs := make([]toolOutput, 0, len(calls))
	for _, call := range calls {
		toolDef, ok := toolMap[call.Function.Name]
		if !ok {
			return nil, fmt.Errorf("gpt51: unknown tool %s", call.Function.Name)
		}
		args, err := decodeToolArguments(call.Function.Arguments)
		if err != nil {
			log.Printf("gpt51: tool %s arg parse error: %v", call.Function.Name, err)
			outputs = append(outputs, toolOutput{
				ToolCallID: call.ID,
				Output:     fmt.Sprintf(`{"error":"invalid arguments: %s"}`, sanitizeToolError(err)),
			})
			continue
		}
		rawArgs := copyArgs(args)
		args = mergeArgs(args, toolDef.Defaults)
		log.Printf("gpt51: tool call %s raw=%v merged=%v", call.Function.Name, rawArgs, args)
		result, execErr := c.dispatchTool(ctx, toolDef, args)
		if execErr != nil {
			log.Printf("gpt51: tool %s error: %v", call.Function.Name, execErr)
			result = fmt.Sprintf(`{"error":"%s"}`, sanitizeToolError(execErr))
		}
		log.Printf("gpt51: tool %s output=%s", call.Function.Name, truncatePayload([]byte(result), 256))
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
		return "", fmt.Errorf("network argument required")
	}
	refIDRaw, ok := args["refId"]
	if !ok {
		return "", fmt.Errorf("refId argument required")
	}
	refID, err := parseUint(refIDRaw)
	if err != nil {
		return "", fmt.Errorf("invalid refId: %w", err)
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

func truncatePayload(b []byte, limit int) string {
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "... (truncated)"
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

func (c *client) respondWithChatTools(ctx context.Context, input string, tools []core.Tool, opts core.Options) (string, error) {
	messages := make([]chatMessagePayload, 0, 4)
	if strings.TrimSpace(opts.SystemPrompt) != "" {
		messages = append(messages, chatMessagePayload{Role: "system", Content: opts.SystemPrompt})
	}
	messages = append(messages, chatMessagePayload{Role: "user", Content: input})

	toolDefs, toolMap, forced := buildChatToolsPayload(tools)
	toolCache := make(map[string]string)
	stallCount := 0
	metadataFetched := false
	contentFetched := false

	for iteration := 0; iteration < 20; iteration++ {
		reqBody := map[string]any{
			"model":       opts.Model,
			"messages":    messages,
			"temperature": opts.Temperature,
		}
		if opts.MaxCompletionTokens > 0 {
			reqBody["max_completion_tokens"] = opts.MaxCompletionTokens
		}

		if len(toolDefs) > 0 {
			reqBody["tools"] = toolDefs
			if !(metadataFetched && contentFetched) && strings.TrimSpace(forced) != "" {
				reqBody["tool_choice"] = buildChatToolChoice(forced)
			} else {
				reqBody["tool_choice"] = "auto"
			}
		}

		bodyBytes, _ := json.Marshal(reqBody)
		_, body, err := webclient.DoWithRetry(ctx, 3, 2*time.Second, func() (int, []byte, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(bodyBytes))
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
				return resp.StatusCode, b, fmt.Errorf("status %d: %s", resp.StatusCode, truncatePayload(b, 512))
			}
			return resp.StatusCode, b, nil
		})
		if err != nil {
			return "", fmt.Errorf("gpt51 chat fallback error: %w", err)
		}

		var resp chatCompletionResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("gpt51: chat completion returned no choices")
		}
		msg := resp.Choices[0].Message

		assistantPayload := chatMessagePayload{
			Role:    "assistant",
			Content: msg.Content,
		}
		if len(msg.ToolCalls) > 0 {
			assistantPayload.ToolCalls = msg.ToolCalls
		}
		messages = append(messages, assistantPayload)

		if len(msg.ToolCalls) == 0 {
			if strings.TrimSpace(msg.Content) == "" {
				continue
			}
			return msg.Content, nil
		}

		convertedCalls := convertChatToolCalls(msg.ToolCalls)
		if len(convertedCalls) == 0 {
			continue
		}

		callOutputs := make(map[string]string, len(convertedCalls))
		pendingCalls := make([]openAIToolCall, 0, len(convertedCalls))
		pendingKeys := make(map[string]string, len(convertedCalls))
		for _, call := range convertedCalls {
			key := toolCacheKey(call)
			if val, ok := toolCache[key]; ok {
				callOutputs[call.ID] = val
				continue
			}
			pendingCalls = append(pendingCalls, call)
			pendingKeys[call.ID] = key
		}

		if len(pendingCalls) > 0 {
			outputs, err := c.executeToolCalls(ctx, pendingCalls, toolMap)
			if err != nil {
				return "", err
			}
			for _, out := range outputs {
				callOutputs[out.ToolCallID] = out.Output
				if key := pendingKeys[out.ToolCallID]; key != "" {
					toolCache[key] = out.Output
				}
			}
		}

		pendingCallExecuted := false
		for _, call := range convertedCalls {
			content := callOutputs[call.ID]
			messages = append(messages, chatMessagePayload{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    content,
			})
			if _, ok := pendingKeys[call.ID]; ok {
				pendingCallExecuted = true
			}
			switch normalizeResource(resourceFromToolCall(call)) {
			case "content":
				contentFetched = true
			case "metadata":
				metadataFetched = true
			}
		}

		if !pendingCallExecuted {
			stallCount++
			if stallCount >= 2 {
				messages = append(messages, chatMessagePayload{
					Role:    "user",
					Content: "You already retrieved the referendum metadata and content. Use the information you have and provide the final answer without calling the tool again.",
				})
				stallCount = 0
			}
		} else {
			stallCount = 0
		}
	}

	return "", fmt.Errorf("gpt51: chat tool loop exceeded")
}

func buildChatToolsPayload(tools []core.Tool) ([]map[string]any, map[string]core.Tool, string) {
	out := []map[string]any{}
	toolMap := map[string]core.Tool{}
	var forced string
	for idx, t := range tools {
		if strings.ToLower(t.Type) != "mcp_referenda" {
			continue
		}
		name := t.Name
		if strings.TrimSpace(name) == "" {
			name = fmt.Sprintf("mcp_referendum_%d", idx+1)
		}
		funcDef := map[string]any{
			"name":        name,
			"description": t.Description,
		}
		if t.Parameters != nil {
			funcDef["parameters"] = t.Parameters
		}
		out = append(out, map[string]any{
			"type":     "function",
			"function": funcDef,
		})
		toolCopy := t
		toolCopy.Name = name
		toolMap[name] = toolCopy
		forced = name
	}
	return out, toolMap, forced
}

func buildChatToolChoice(forced string) any {
	if strings.TrimSpace(forced) == "" {
		return "auto"
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name": forced,
		},
	}
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatMessagePayload struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

func convertChatToolCalls(calls []chatToolCall) []openAIToolCall {
	out := make([]openAIToolCall, len(calls))
	for i, call := range calls {
		out[i] = openAIToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		}
	}
	return out
}

func toolCacheKey(call openAIToolCall) string {
	name := strings.ToLower(strings.TrimSpace(call.Function.Name))
	args := strings.TrimSpace(call.Function.Arguments)
	return name + "::" + args
}

func resourceFromToolCall(call openAIToolCall) string {
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" {
		return "metadata"
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(args), &obj); err != nil {
		return "metadata"
	}
	res := strings.TrimSpace(strings.ToLower(fmt.Sprint(obj["resource"])))
	if res == "" {
		return "metadata"
	}
	return res
}

func normalizeResource(res string) string {
	r := strings.TrimSpace(strings.ToLower(res))
	if r == "" {
		return "metadata"
	}
	if strings.HasPrefix(r, "attachment") {
		return "attachments"
	}
	return r
}
