// Package agentcli is the resource module for installing and managing AI agent
// CLIs on a remote VM over SSH — Claude Code, OpenAI Codex, Google Gemini CLI,
// the Hermes Agent, and OpenClaw. It mirrors the backend services/agentcli and
// services/openclaw HTTP contract.
//
// Every call SSHes into the target VM (the node resolves the SSH key referenced
// by KeyPairName from the workspace vault) and runs the agent's installer or a
// configure/health/test action there. The four operations per agent:
//
//	Install        POST /api/v2/infrastructure/services/{agent}/install
//	Configure      POST /api/v2/infrastructure/services/{agent}/configure
//	Health         POST /api/v2/infrastructure/services/{agent}/health
//	TestConnection POST /api/v2/infrastructure/services/{agent}/test-connection
//
// All requests are multipart/form-data with the shared SSH fields plus the
// per-operation options.
package agentcli

import (
	"context"
	"fmt"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// Agent identifies which agent CLI to act on. The value is the backend resource
// segment used in the URL path.
type Agent string

const (
	ClaudeCode Agent = "claudecode"
	Codex      Agent = "codex"
	Gemini     Agent = "gemini"
	Hermes     Agent = "hermesagent"
	OpenClaw   Agent = "openclaw"
)

// ResolveAgent maps friendly names ("claude", "hermes", …) to the canonical
// Agent value. Unknown names return ok=false.
func ResolveAgent(name string) (Agent, bool) {
	switch name {
	case "claude", "claudecode", "claude-code":
		return ClaudeCode, true
	case "codex", "openai-codex":
		return Codex, true
	case "gemini", "google-gemini":
		return Gemini, true
	case "hermes", "hermesagent", "hermes-agent":
		return Hermes, true
	case "openclaw":
		return OpenClaw, true
	default:
		return "", false
	}
}

// Result is a decoded JSON object response (install/configure/health/test all
// return JSON objects with status/message + operation-specific fields).
type Result = map[string]interface{}

// Client is the agent-CLI resource facade. Acquire it with c.AgentCLI().
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// SSH is the target VM + vault key. KeyPairName is required for every operation
// because each one SSHes into the host (the node fetches the key from vault).
type SSH struct {
	Host          string // target VM IP or DNS (required)
	User          string // SSH login user, e.g. "ubuntu" (required)
	KeyPairName   string // workspace vault key entry (required)
	WorkspaceUser string // overrides the form username used for vault lookup
	Organization  string // overrides the org segment of the vault path
}

func (c *Client) fields(s SSH) (map[string]string, error) {
	if s.Host == "" || s.User == "" || s.KeyPairName == "" {
		return nil, fmt.Errorf("agentcli: SSH.Host, SSH.User, and SSH.KeyPairName are required")
	}
	user := s.WorkspaceUser
	if user == "" {
		user = c.AuthedUsername
	}
	org := s.Organization
	if org == "" {
		org = user
	}
	return map[string]string{
		"hostname":      s.Host,
		"ssh_username":  s.User,
		"key_pair_name": s.KeyPairName,
		"username":      user,
		"organization":  org,
	}, nil
}

func (c *Client) post(ctx context.Context, op string, agent Agent, action string, fields map[string]string) (Result, error) {
	url := transport.JoinURL(c.NodeURL, "/api/v2/infrastructure/services/"+string(agent)+"/"+action)
	var out Result
	if err := c.T.Multipart(ctx, op, url, fields, nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// InstallOpts are the optional knobs for Install.
type InstallOpts struct {
	InstallMethod string // "npm" | "tarball" (default npm)
	NodeVersion   string // Node.js major version (default 24)
	SkipDocker    bool
	SkipCleanup   bool
	Domain        string // gateway agents only (hermes/openclaw): front via Traefik
	SSLEmail      string
}

// Install runs the agent's installer on the target VM.
func (c *Client) Install(ctx context.Context, agent Agent, ssh SSH, opts InstallOpts) (Result, error) {
	f, err := c.fields(ssh)
	if err != nil {
		return nil, err
	}
	if opts.InstallMethod != "" {
		f["install_method"] = opts.InstallMethod
	}
	if opts.NodeVersion != "" {
		f["node_version"] = opts.NodeVersion
	}
	if opts.SkipDocker {
		f["skip_docker"] = "true"
	}
	if opts.SkipCleanup {
		f["skip_cleanup"] = "true"
	}
	if opts.Domain != "" {
		f["domain"] = opts.Domain
		if opts.SSLEmail != "" {
			f["ssl_email"] = opts.SSLEmail
		}
	}
	return c.post(ctx, "agentcli.Install", agent, "install", f)
}

// ConfigureOpts are the API keys / model / gateway settings for Configure.
type ConfigureOpts struct {
	AnthropicKey string
	OpenAIKey    string
	GeminiKey    string
	Model        string
	GatewayPort  string
	StartGateway bool
}

// Configure writes API keys / model / gateway settings on the target VM.
func (c *Client) Configure(ctx context.Context, agent Agent, ssh SSH, opts ConfigureOpts) (Result, error) {
	f, err := c.fields(ssh)
	if err != nil {
		return nil, err
	}
	for k, v := range map[string]string{
		"anthropic_key": opts.AnthropicKey,
		"openai_key":    opts.OpenAIKey,
		"gemini_key":    opts.GeminiKey,
		"model":         opts.Model,
		"gateway_port":  opts.GatewayPort,
	} {
		if v != "" {
			f[k] = v
		}
	}
	if opts.StartGateway {
		f["start_gateway"] = "true"
	}
	return c.post(ctx, "agentcli.Configure", agent, "configure", f)
}

// Health runs a health action: "status" (default), "logs", "doctor", "restart".
// lines controls how many log lines to tail (default 50).
func (c *Client) Health(ctx context.Context, agent Agent, ssh SSH, action string, lines int) (Result, error) {
	f, err := c.fields(ssh)
	if err != nil {
		return nil, err
	}
	if action != "" {
		f["action"] = action
	}
	if lines > 0 {
		f["log_lines"] = strconv.Itoa(lines)
	}
	return c.post(ctx, "agentcli.Health", agent, "health", f)
}

// TestConnection verifies SSH reachability and whether the agent binary is
// already installed on the target VM.
func (c *Client) TestConnection(ctx context.Context, agent Agent, ssh SSH) (Result, error) {
	f, err := c.fields(ssh)
	if err != nil {
		return nil, err
	}
	return c.post(ctx, "agentcli.TestConnection", agent, "test-connection", f)
}
