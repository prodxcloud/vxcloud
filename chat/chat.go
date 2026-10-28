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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

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

type Conversation struct {
	SessionID     string `json:"session_id"`
	Title         string `json:"title"`
	FirstQuery    string `json:"first_query"`
	AgentType     string `json:"agent_type"`
	MessageCount  int    `json:"message_count"`
	StartedAt     string `json:"started_at"`
	LastMessageAt string `json:"last_message_at"`
}

type ConversationHistoryEntry struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	UserID    string                 `json:"user_id"`
	AgentType string                 `json:"agent_type"`
	Query     string                 `json:"query"`
	Response  string                 `json:"response"`
	Timestamp string                 `json:"timestamp"`
	Title     string                 `json:"title"`
	Metadata  map[string]interface{} `json:"metadata"`
	AIModel   string                 `json:"ai_model"`
	Intent    string                 `json:"intent"`
}

type ConversationList struct {
	Conversations []Conversation `json:"conversations"`
	Total         int            `json:"total"`
	Limit         int            `json:"limit"`
	Offset        int            `json:"offset"`
}

type StreamEvent struct {
	Event string
	Data  interface{}
	Raw   string
	Err   error
}

type AgentStreamInput struct {
	Query       string
	Agent       string
	Model       string
	SessionID   string
	UserID      string
	WorkspaceID string
	Context     map[string]interface{}
	History     []Message
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

func (c *Client) ListConversations(ctx context.Context, limit, offset int, search, agentType string, sessionIDs []string) (*ConversationList, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	v := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if search != "" {
		v.Set("q", search)
	}
	if agentType != "" {
		v.Set("agent_type", agentType)
	}
	if len(sessionIDs) > 0 {
		v.Set("session_ids", strings.Join(sessionIDs, ","))
	}
	var out ConversationList
	if err := c.T.JSON(ctx, "chat.ListConversations", "GET", transport.JoinURL(c.NodeURL, "/api/v3/ai/agent/conversations")+"?"+v.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) History(ctx context.Context, sessionID string, agentType string) ([]ConversationHistoryEntry, error) {
	if sessionID == "" {
		return nil, errors.New("chat.History: sessionID is required")
	}
	v := url.Values{"session_id": {sessionID}, "limit": {"5000"}}
	if agentType != "" {
		v.Set("agent_type", agentType)
	}
	var raw struct {
		History []ConversationHistoryEntry `json:"history"`
	}
	if err := c.T.JSON(ctx, "chat.History", "GET", transport.JoinURL(c.NodeURL, "/api/v3/ai/agent/history")+"?"+v.Encode(), nil, &raw); err != nil {
		return nil, err
	}
	for i, j := 0, len(raw.History)-1; i < j; i, j = i+1, j-1 {
		raw.History[i], raw.History[j] = raw.History[j], raw.History[i]
	}
	return raw.History, nil
}

func (c *Client) SaveMessage(ctx context.Context, sessionID, messageType, content, agentType string) (map[string]interface{}, error) {
	if sessionID == "" || content == "" {
		return nil, errors.New("chat.SaveMessage: sessionID and content are required")
	}
	if messageType != "human" && messageType != "ai" {
		return nil, errors.New("chat.SaveMessage: messageType must be human or ai")
	}
	var out map[string]interface{}
	err := c.T.JSON(ctx, "chat.SaveMessage", "POST", transport.JoinURL(c.NodeURL, "/api/v3/ai/agent/conversation/save"), map[string]interface{}{"session_id": sessionID, "message_type": messageType, "content": content, "agent_type": agentType}, &out)
	return out, err
}
func (c *Client) RenameConversation(ctx context.Context, sessionID, title string) (map[string]interface{}, error) {
	if sessionID == "" || strings.TrimSpace(title) == "" {
		return nil, errors.New("chat.RenameConversation: sessionID and title are required")
	}
	var out map[string]interface{}
	err := c.T.JSON(ctx, "chat.RenameConversation", "POST", transport.JoinURL(c.NodeURL, "/api/v3/ai/agent/conversation/rename"), map[string]interface{}{"session_id": sessionID, "title": title}, &out)
	return out, err
}
func (c *Client) DeleteConversation(ctx context.Context, sessionID string) (map[string]interface{}, error) {
	if sessionID == "" {
		return nil, errors.New("chat.DeleteConversation: sessionID is required")
	}
	var out map[string]interface{}
	err := c.T.JSON(ctx, "chat.DeleteConversation", "POST", transport.JoinURL(c.NodeURL, "/api/v3/ai/agent/conversation/delete"), map[string]interface{}{"session_id": sessionID}, &out)
	return out, err
}

func (c *Client) streamEvents(ctx context.Context, op, path string, body map[string]interface{}) (<-chan StreamEvent, error) {
	resp, err := c.T.StreamJSON(ctx, op, "POST", transport.JoinURL(c.NodeURL, path), body, nil)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamEvent)
	go func() {
		defer close(out)
		defer resp.Close()
		scan := bufio.NewScanner(resp)
		scan.Buffer(make([]byte, 4096), 4*1024*1024)
		named := ""
		for scan.Scan() {
			line := strings.TrimRight(scan.Text(), "\r")
			if strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "event:") {
				named = strings.TrimSpace(line[6:])
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			raw := strings.TrimSpace(line[5:])
			if raw == "" {
				continue
			}
			var data interface{}
			if json.Unmarshal([]byte(raw), &data) != nil {
				data = raw
			}
			event := named
			if event == "" {
				event = "message"
			}
			if obj, ok := data.(map[string]interface{}); ok {
				if s, ok := obj["type"].(string); ok {
					event = s
				}
				if s, ok := obj["event"].(string); ok && event == "message" {
					event = s
				}
				if named == "" {
					if inner, ok := obj["data"]; ok {
						data = inner
					}
				}
			}
			out <- StreamEvent{Event: event, Data: data, Raw: raw}
			named = ""
		}
		if err := scan.Err(); err != nil {
			out <- StreamEvent{Err: err}
		}
	}()
	return out, nil
}

// StreamAgent starts the FastAPI agent stream. The returned channel closes on
// EOF; inspect StreamEvent.Err for a transport or decoding failure.
func (c *Client) StreamAgent(ctx context.Context, in AgentStreamInput) (<-chan StreamEvent, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("chat.StreamAgent: Query is required")
	}
	body := map[string]interface{}{"query": in.Query, "agent": in.Agent, "model": in.Model, "session_id": in.SessionID, "user_id": in.UserID, "workspace_id": in.WorkspaceID, "context": in.Context, "history": in.History, "stream": true}
	if body["agent"] == "" {
		body["agent"] = "networking"
	}
	return c.streamEvents(ctx, "chat.StreamAgent", "/api/v3/ai/agent/chat", body)
}

// StreamWorkflow streams workflow-agent planning events. A workflow is never
// run by this method unless confirmRun is true and query is an explicit
// confirmation phrase accepted by the platform.
func (c *Client) StreamWorkflow(ctx context.Context, in AgentStreamInput, nodes, edges []interface{}, confirmRun bool) (<-chan StreamEvent, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("chat.StreamWorkflow: Query is required")
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	if confirmRun && q != "run it" && q != "yes, run it" && q != "go ahead and run it" {
		return nil, errors.New("chat.StreamWorkflow: confirmRun requires an explicit run-it command")
	}
	body := map[string]interface{}{"query": in.Query, "model": in.Model, "session_id": in.SessionID, "user_id": in.UserID, "action": "generate", "mode": "build", "surface": "chat", "history": in.History, "nodes": nodes, "edges": edges, "stream": true}
	if confirmRun {
		body["action"] = "run"
	}
	return c.streamEvents(ctx, "chat.StreamWorkflow", "/api/v2/workflow/agent/chat", body)
}

func (c *Client) MissionCommand(ctx context.Context, command, conversationID, timezone string) (map[string]interface{}, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("chat.MissionCommand: command is required")
	}
	var out map[string]interface{}
	err := c.T.JSON(ctx, "chat.MissionCommand", "POST", transport.JoinURL(c.NodeURL, "/api/v2/vxchrono/chat/command"), map[string]interface{}{"command": command, "surface": "chat", "conversation_id": conversationID, "timezone": timezone}, &out)
	return out, err
}
