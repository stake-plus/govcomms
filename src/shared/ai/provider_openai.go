package ai

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/stake-plus/govcomms/src/shared/httpx"
)

type openAIClient struct {
    apiKey     string
    httpClient *http.Client
    defaults   Options
}

func newOpenAIClient(cfg FactoryConfig) *openAIClient {
    return &openAIClient{
        apiKey: cfg.OpenAIKey,
        httpClient: httpx.NewDefault(300 * time.Second),
        defaults: Options{
            Model:               valueOrDefault(cfg.Model, "gpt-5"),
            Temperature:         orFloat(cfg.Temperature, 1),
            MaxCompletionTokens: orInt(cfg.MaxCompletionTokens, 50000),
            SystemPrompt:        cfg.SystemPrompt,
        },
    }
}

func (c *openAIClient) AnswerQuestion(ctx context.Context, content string, question string, opts Options) (string, error) {
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
    b, _ := json.Marshal(reqBody)
    req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(b))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.apiKey)
    resp, err := c.httpClient.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil { return "", err }
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("openAI API error: %s", string(body))
    }
    var result struct {
        Choices []struct { Message struct{ Content string `json:"content"` } `json:"message"` } `json:"choices"`
    }
    if err := json.Unmarshal(body, &result); err != nil { return "", err }
    if len(result.Choices) == 0 { return "", fmt.Errorf("no response from OpenAI") }
    return result.Choices[0].Message.Content, nil
}

func (c *openAIClient) Respond(ctx context.Context, input string, tools []Tool, opts Options) (string, error) {
    // Use Responses API with optional tools like web_search
    merged := c.merge(opts)
    payload := map[string]interface{}{
        "model": merged.Model,
        "input": input,
        "temperature": merged.Temperature,
        "max_output_tokens": merged.MaxCompletionTokens,
    }
    if len(tools) > 0 {
        var toolPayload []map[string]interface{}
        for _, t := range tools { toolPayload = append(toolPayload, map[string]interface{}{"type": t.Type}) }
        payload["tools"] = toolPayload
        payload["tool_choice"] = "auto"
    }
    b, _ := json.Marshal(payload)
    req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(b))
    if err != nil { return "", err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+c.apiKey)
    resp, err := c.httpClient.Do(req)
    if err != nil { return "", err }
    defer resp.Body.Close()
    body, err := io.ReadAll(resp.Body)
    if err != nil { return "", err }
    if resp.StatusCode != http.StatusOK {
        return "", fmt.Errorf("openAI API error: %s", string(body))
    }
    // Tolerate multiple shapes by extracting text fields
    var result struct { Output []struct { Content []struct { Text string `json:"text"` } `json:"content"` } `json:"output"` }
    if err := json.Unmarshal(body, &result); err == nil {
        for _, o := range result.Output { for _, c := range o.Content { if c.Text != "" { return c.Text, nil } } }
    }
    // Fallback minimal parse for standard responses
    var alt struct { OutputText string `json:"output_text"` }
    if err := json.Unmarshal(body, &alt); err == nil && alt.OutputText != "" { return alt.OutputText, nil }
    return "", fmt.Errorf("failed to parse OpenAI response")
}

func (c *openAIClient) merge(opts Options) Options {
    out := c.defaults
    if opts.Model != "" { out.Model = opts.Model }
    if opts.Temperature != 0 { out.Temperature = opts.Temperature }
    if opts.MaxCompletionTokens != 0 { out.MaxCompletionTokens = opts.MaxCompletionTokens }
    if opts.SystemPrompt != "" { out.SystemPrompt = opts.SystemPrompt }
    return out
}

func valueOrDefault(val, def string) string { if val != "" { return val }; return def }
func orInt(v, d int) int { if v != 0 { return v }; return d }
func orFloat(v, d float64) float64 { if v != 0 { return v }; return d }


