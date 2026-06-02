// Package chat exposes multi-provider AI chat — the same surface
// `vxcli chat` ships. Provider routing dispatches to Anthropic / OpenAI
// / Google / OpenClaw using credentials stored under /api/v2/setup/ai-*.
//
// The platform handler at POST /api/v2/chat/send accepts a provider key
// and forwards the request, so SDK consumers don't need to vendor each
// provider's client. Streaming via Server-Sent Events is documented in
// BIG_PLAN.md M3 (followup); v0.1 is one-shot only.
package chat

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Provider names that the platform's chat router recognizes. Match the
// AI credential keys under /api/v2/setup/ai-<provider>-credentials.
type Provider string

const (
	ProviderAnthropic  Provider = "anthropic"
	ProviderOpenAI     Provider = "openai"
	ProviderGoogle     Provider = "google"
	ProviderOpenClaw   Provider = "openclaw"
	ProviderDeepseek   Provider = "deepseek"
	ProviderQwen       Provider = "qwen"
	ProviderGroq       Provider = "groq"
	ProviderMistral    Provider = "mistral"
	ProviderPerplexity Provider = "perplexity"
	ProviderHF         Provider = "huggingface"
	ProviderOllama     Provider = "ollama"
	ProviderHermes     Provider = "hermes"
)

// Message is one entry in a chat conversation.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Client is the entry point. Construct via:
//
//	c.Chat()
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// SendInput is the request shape for one-shot chat completion.
type SendInput struct {
	Provider     Provider
	Model        string
	Messages     []Message
	SystemPrompt string  // optional convenience; prepended as a system message
	Temperature  float64 // 0–1; default 0.7 server-side if zero
	MaxTokens    int     // 0 = provider default
}

// SendOutput is the response envelope.
type SendOutput struct {
	Completion   string                 `json:"completion"`
	Provider     string                 `json:"provider,omitempty"`
	Model        string                 `json:"model,omitempty"`
	InputTokens  int                    `json:"input_tokens,omitempty"`
	OutputTokens int                    `json:"output_tokens,omitempty"`
	Raw          map[string]interface{} `json:"-"`
}

// Send issues a one-shot chat completion request. The platform routes
// to the configured provider using credentials stored in your workspace
// Vault.
//
// Endpoint: POST /api/v2/chat/send
func (c *Client) Send(ctx context.Context, in SendInput) (*SendOutput, error) {
	if len(in.Messages) == 0 && in.SystemPrompt == "" {
		return nil, errors.New("chat.Send: Messages or SystemPrompt is required")
	}

	msgs := append([]Message{}, in.Messages...)
	if in.SystemPrompt != "" {
		msgs = append([]Message{{Role: "system", Content: in.SystemPrompt}}, msgs...)
	}
	body := map[string]interface{}{
		"provider": string(in.Provider),
		"model":    in.Model,
		"messages": msgs,
	}
	if in.Temperature > 0 {
		body["temperature"] = in.Temperature
	}
	if in.MaxTokens > 0 {
		body["max_tokens"] = in.MaxTokens
	}

	url := transport.JoinURL(c.NodeURL, "/api/v2/chat/send")
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, "chat.Send", "POST", url, body, &raw); err != nil {
		return nil, fmt.Errorf("chat.Send: %w", err)
	}
	r := &SendOutput{Raw: raw}
	if v, ok := raw["completion"].(string); ok {
		r.Completion = v
	}
	if v, ok := raw["provider"].(string); ok {
		r.Provider = v
	}
	if v, ok := raw["model"].(string); ok {
		r.Model = v
	}
	if v, ok := raw["input_tokens"].(float64); ok {
		r.InputTokens = int(v)
	}
	if v, ok := raw["output_tokens"].(float64); ok {
		r.OutputTokens = int(v)
	}
	return r, nil
}

// Quick is a one-shot helper: ask a single question, get a string back.
// Useful for scripts that don't care about token counts or alternate
// completions.
func (c *Client) Quick(ctx context.Context, provider Provider, model, question string) (string, error) {
	out, err := c.Send(ctx, SendInput{
		Provider: provider, Model: model,
		Messages: []Message{{Role: "user", Content: question}},
	})
	if err != nil {
		return "", err
	}
	return out.Completion, nil
}
