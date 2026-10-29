package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one entry in an OpenAI-compatible chat transcript.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function FunctionCallBody `json:"function"`
}

// FunctionCallBody carries the function name and raw JSON arguments.
type FunctionCallBody struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

// FunctionSpec is the JSON-schema description of a tool.
type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatRequest struct {
	Model      string    `json:"model"`
	Messages   []Message `json:"messages"`
	Tools      []Tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// Client is a minimal OpenAI-compatible chat-completions client.
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// NewClient builds a client with a bounded HTTP timeout.
func NewClient(baseURL, apiKey, model string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

// Chat sends the transcript and returns the next assistant message.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (Message, error) {
	if c == nil {
		return Message{}, fmt.Errorf("AI client is not configured")
	}
	body, err := json.Marshal(chatRequest{
		Model:      c.Model,
		Messages:   messages,
		Tools:      tools,
		ToolChoice: toolChoice(tools),
	})
	if err != nil {
		return Message{}, fmt.Errorf("encode chat request: %w", err)
	}
	endpoint := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Message{}, fmt.Errorf("build chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(c.APIKey))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("contact AI endpoint: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return Message{}, fmt.Errorf("read AI response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Message{}, fmt.Errorf("AI endpoint returned %s: %s", resp.Status, truncateRunes(strings.TrimSpace(string(raw)), 400))
	}
	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Message{}, fmt.Errorf("decode AI response: %w", err)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return Message{}, fmt.Errorf("AI endpoint error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return Message{}, fmt.Errorf("AI endpoint returned no choices")
	}
	msg := decoded.Choices[0].Message
	if msg.Role == "" {
		msg.Role = "assistant"
	}
	return msg, nil
}

func toolChoice(tools []Tool) string {
	if len(tools) == 0 {
		return ""
	}
	return "auto"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
