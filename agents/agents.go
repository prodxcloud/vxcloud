// Package agents is the resource module for the AI-agent surface that
// vxcli exposes via `vxcli agent {coding, devops, git, parallel, presets,
// tool, tools}`.
//
// IMPORTANT: vxcli's agent commands run *client-side* — they read provider
// credentials (Anthropic / OpenAI / Google / OpenClaw) from the Vault via
// /api/v2/setup/ai-get-all-credentials and call the provider directly.
// This package mirrors that pattern, so SDK consumers don't need to
// vendor a provider client; the SDK reads creds and dispatches.
//
// Each agent kind = a system prompt + a tool set. The kinds enumerated
// below match vxcli's verbs.
package agents

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client is the entry point. Construct via:
//
//	c.Agents()
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// Kind enumerates the agent verbs vxcli supports.
type Kind string

const (
	KindCoding   Kind = "coding"
	KindDevops   Kind = "devops"
	KindGit      Kind = "git"
	KindParallel Kind = "parallel"
)

// RunInput is the request shape for any agent invocation.
type RunInput struct {
	Kind Kind
	// Task — the user's natural-language request.
	Task string
	// Lang, only used by KindCoding (python / go / ts / terraform / dockerfile / sh).
	Lang string
	// Provider — anthropic | openai | google | openclaw. Empty = first
	// available in the workspace.
	Provider string
	// Model — provider-specific identifier (e.g. claude-opus-4-7).
	Model string
	// Context — extra k/v passed to the agent (file paths, repo state, …).
	Context map[string]string
}

// RunOutput is the response envelope every agent invocation returns.
type RunOutput struct {
	Output   string                 `json:"output"`
	Provider string                 `json:"provider,omitempty"`
	Model    string                 `json:"model,omitempty"`
	Tokens   int                    `json:"tokens,omitempty"`
	Raw      map[string]interface{} `json:"-"`
}

// Run dispatches an agent invocation against the platform's agent
// orchestrator. The platform routes to the appropriate AI provider
// using credentials stored under /api/v2/setup/ai-*.
//
// Endpoint: POST /api/v2/agents/run (planned; soft-fails today).
func (c *Client) Run(ctx context.Context, in RunInput) (*RunOutput, error) {
	if in.Task == "" {
		return nil, errors.New("agents.Run: Task is required")
	}
	if in.Kind == "" {
		in.Kind = KindCoding
	}
	body := map[string]interface{}{
		"kind":     string(in.Kind),
		"task":     in.Task,
		"lang":     in.Lang,
		"provider": in.Provider,
		"model":    in.Model,
		"context":  in.Context,
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/agents/run")
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, "agents.Run", "POST", url, body, &raw); err != nil {
		return nil, fmt.Errorf("agents.Run: %w", err)
	}
	r := &RunOutput{Raw: raw}
	if v, ok := raw["output"].(string); ok {
		r.Output = v
	}
	if v, ok := raw["provider"].(string); ok {
		r.Provider = v
	}
	if v, ok := raw["model"].(string); ok {
		r.Model = v
	}
	if v, ok := raw["tokens"].(float64); ok {
		r.Tokens = int(v)
	}
	return r, nil
}

// Coding — multi-language code generation. Convenience wrapper over Run.
func (c *Client) Coding(ctx context.Context, task, lang string) (*RunOutput, error) {
	return c.Run(ctx, RunInput{Kind: KindCoding, Task: task, Lang: lang})
}

// Devops — multi-tool DevOps orchestration (git + docker + VM + pipeline).
func (c *Client) Devops(ctx context.Context, task string) (*RunOutput, error) {
	return c.Run(ctx, RunInput{Kind: KindDevops, Task: task})
}

// Git — AI-powered git operations.
func (c *Client) Git(ctx context.Context, task string) (*RunOutput, error) {
	return c.Run(ctx, RunInput{Kind: KindGit, Task: task})
}

// Parallel runs N sub-agents in parallel using a named preset.
func (c *Client) Parallel(ctx context.Context, preset, task string) (*RunOutput, error) {
	return c.Run(ctx, RunInput{
		Kind: KindParallel, Task: task,
		Context: map[string]string{"preset": preset},
	})
}

// Presets returns the available parallel-agent presets, if the server
// exposes the catalog endpoint.
func (c *Client) Presets(ctx context.Context) ([]map[string]interface{}, error) {
	var resp struct {
		Presets []map[string]interface{} `json:"presets"`
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/agents/presets")
	if err := c.T.JSON(ctx, "agents.Presets", "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Presets, nil
}

// Tools returns the available tools an agent can invoke.
func (c *Client) Tools(ctx context.Context, kind Kind) ([]map[string]interface{}, error) {
	var resp struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/agents/tools")
	if kind != "" {
		url += "?kind=" + string(kind)
	}
	if err := c.T.JSON(ctx, "agents.Tools", "GET", url, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tools, nil
}

// Tool calls a specific tool directly with no LLM in the loop —
// deterministic, useful in CI scripts.
func (c *Client) Tool(ctx context.Context, name string, args map[string]interface{}) (map[string]interface{}, error) {
	if name == "" {
		return nil, errors.New("agents.Tool: name is required")
	}
	body := map[string]interface{}{
		"tool": name,
		"args": args,
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/agents/tool")
	var out map[string]interface{}
	if err := c.T.JSON(ctx, "agents.Tool", "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
