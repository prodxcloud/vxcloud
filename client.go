package vxsdk

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/prodxcloud/vxcloud/agentcli"
	"github.com/prodxcloud/vxcloud/agentcontrol"
	"github.com/prodxcloud/vxcloud/agents"
	"github.com/prodxcloud/vxcloud/auth"
	"github.com/prodxcloud/vxcloud/billing"
	"github.com/prodxcloud/vxcloud/chat"
	"github.com/prodxcloud/vxcloud/cicd"
	"github.com/prodxcloud/vxcloud/cloud"
	"github.com/prodxcloud/vxcloud/connector"
	"github.com/prodxcloud/vxcloud/deploy"
	vxerrors "github.com/prodxcloud/vxcloud/errors"
	"github.com/prodxcloud/vxcloud/install"
	"github.com/prodxcloud/vxcloud/internal/cred"
	"github.com/prodxcloud/vxcloud/marketplace"
	"github.com/prodxcloud/vxcloud/metaldb"
	"github.com/prodxcloud/vxcloud/networks"
	"github.com/prodxcloud/vxcloud/nodes"
	"github.com/prodxcloud/vxcloud/observability"
	"github.com/prodxcloud/vxcloud/robotic"
	"github.com/prodxcloud/vxcloud/services"
	"github.com/prodxcloud/vxcloud/sessions"
	"github.com/prodxcloud/vxcloud/transport"
	"github.com/prodxcloud/vxcloud/vxchrono"
	"github.com/prodxcloud/vxcloud/vxcomputer"
	"github.com/prodxcloud/vxcloud/webscraper"
	"github.com/prodxcloud/vxcloud/workflow"
	"github.com/prodxcloud/vxcloud/workspace"
)

// Version is the SDK version string. Bumped manually on tag.
const Version = "0.1.0-preview"

const (
	defaultInfinityURL = "https://api.vxcloud.io"
	defaultTimeout     = 30 * time.Second
)

// Client is the entry point of the SDK. Construct one with New, hold it
// for the life of your process, and acquire resource modules with the
// methods Sessions(), CICD(), etc.
//
// Client is safe for concurrent use by multiple goroutines.
type Client struct {
	cfg *config

	mu    sync.RWMutex
	state auth.State

	t *transport.Transport
}

// New constructs a Client. At least one of WithAPIKey or WithJWT must be
// supplied — or LoadFromVxcli (which reads ~/.vxcloud/credentials.json).
//
// The constructor does not perform a network round-trip. The first method
// call on a resource module triggers credential exchange if needed.
func New(_ context.Context, opts ...Option) (*Client, error) {
	c := &config{
		infinityURL: defaultInfinityURL,
		timeout:     defaultTimeout,
		userAgent:   "vxsdk-go/" + Version,
	}
	for _, o := range opts {
		o(c)
	}

	if c.apiKey == "" && c.jwt == "" {
		return nil, &vxerrors.Failure{Op: "vxsdk.New", Message: "no credentials: pass WithAPIKey or WithJWT"}
	}
	if c.apiKey != "" {
		if err := auth.APIKey(c.apiKey).Validate(); err != nil {
			return nil, &vxerrors.AuthError{Failure: &vxerrors.Failure{Op: "vxsdk.New", Message: err.Error()}}
		}
	}

	hc := c.httpClient
	if hc == nil {
		hc = &http.Client{Timeout: c.timeout}
	}
	c.httpClient = hc // memoize so refresh() reuses the same client

	cl := &Client{
		cfg: c,
		state: auth.State{
			APIKey:  auth.APIKey(c.apiKey),
			Token:   auth.Token{Access: c.jwt, Refresh: c.refreshJWT},
			User:    auth.User{Username: c.username},
			NodeURL: c.nodeURL,
		},
	}
	cl.t = &transport.Transport{
		Doer:          hc,
		UserAgent:     c.userAgent,
		Authorization: cl.authHeaders,
		// NodeURL lets the transport drop X-API-Key for node-targeted requests
		// when a Bearer JWT is present (see transport.Transport.NodeURL).
		NodeURL:      c.nodeURL,
		RefreshOn401: cl.refresh,
	}
	return cl, nil
}

// refresh exchanges the stored API key for a fresh JWT pair against the
// Infinity control plane and updates the client's auth state. Called by
// the transport layer on 401, and exposed publicly via Authenticate().
//
// Returns an error if no API key is configured (in which case the original
// 401 should be surfaced to the caller).
func (c *Client) refresh(ctx context.Context) error {
	c.mu.RLock()
	apiKey := string(c.state.APIKey)
	username := c.state.User.Username
	c.mu.RUnlock()
	if apiKey == "" {
		return &vxerrors.AuthError{Failure: &vxerrors.Failure{
			Op: "vxsdk.refresh", Message: "no api key configured — cannot refresh JWT",
		}}
	}
	resp, err := auth.Exchange(ctx, c.cfg.httpClient, c.cfg.infinityURL, apiKey, username)
	if err != nil {
		return &vxerrors.AuthError{Failure: &vxerrors.Failure{
			Op: "vxsdk.refresh", Message: "exchange api key for jwt", Cause: err,
		}}
	}
	c.mu.Lock()
	c.state.Token = auth.Token{Access: resp.Access, Refresh: resp.Refresh}
	if resp.User.Username != "" {
		c.state.User.Username = resp.User.Username
	}
	if resp.User.Email != "" {
		c.state.User.Email = resp.User.Email
	}
	if resp.User.Organization != nil {
		c.state.User.Organization = resp.User.Organization.Name
	}
	if resp.User.Workspace != nil {
		c.state.User.Workspace = resp.User.Workspace.Name
	}
	c.mu.Unlock()
	return nil
}

// Authenticate proactively exchanges the configured API key for a fresh
// JWT pair. Optional — the SDK refreshes automatically on the first 401 —
// but useful for fail-fast startup checks.
func (c *Client) Authenticate(ctx context.Context) error { return c.refresh(ctx) }

// LoadFromVxcli is a convenience constructor that reads
// ~/.vxcloud/credentials.json (the file `vxcli auth login` writes) and
// returns a Client using the same identity. opts may add overrides.
func LoadFromVxcli(ctx context.Context, opts ...Option) (*Client, error) {
	f, err := cred.Load()
	if err != nil {
		return nil, &vxerrors.Failure{Op: "vxsdk.LoadFromVxcli", Message: "load credentials.json", Cause: err}
	}
	merged := []Option{
		WithAPIKey(f.APIKey),
		WithUsername(f.Username),
	}
	if f.AccessToken != "" {
		merged = append(merged, WithJWT(f.AccessToken, f.RefreshToken))
	}
	if f.BaseURL != "" {
		merged = append(merged, WithInfinityURL(f.BaseURL))
	}
	if f.NodeURL != "" {
		merged = append(merged, WithNodeURL(f.NodeURL))
	}
	if tid := f.TenantID; tid != "" {
		merged = append(merged, WithTenantID(tid))
	} else if f.OrganizationID != "" {
		merged = append(merged, WithTenantID(f.OrganizationID))
	}
	merged = append(merged, opts...)
	return New(ctx, merged...)
}

// authHeaders returns the bearer + apikey pair for the transport layer.
// Both are sent when present — the same pattern vxcli uses.
func (c *Client) authHeaders() (bearer, apiKey string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.Token.Access, string(c.state.APIKey)
}

// Whoami returns the authenticated principal. Useful for logging / display.
func (c *Client) Whoami() auth.User {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.User
}

// InfinityURL returns the control-plane base URL the Client is targeting.
func (c *Client) InfinityURL() string { return c.cfg.infinityURL }

// NodeURL returns the per-tenant node base URL.
func (c *Client) NodeURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state.NodeURL
}

// TenantID returns the tenant id used for the agentcontrol surface.
// Empty unless set via WithTenantID or loaded by LoadFromVxcli.
func (c *Client) TenantID() string { return c.cfg.tenantID }

// Sessions returns the deployment-sessions resource module.
func (c *Client) Sessions() *sessions.Client {
	return &sessions.Client{T: c.t, NodeURL: c.NodeURL()}
}

// CICD returns the CI/CD resource module.
func (c *Client) CICD() *cicd.Client {
	return &cicd.Client{T: c.t, NodeURL: c.NodeURL()}
}

// Install returns the script/compose installer resource module.
func (c *Client) Install() *install.Client {
	return &install.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// AgentCLI returns the AI agent-CLI installer module — install / configure /
// health / test-connection for Claude Code, OpenAI Codex, Google Gemini CLI,
// the Hermes Agent, and OpenClaw on a remote VM over SSH.
func (c *Client) AgentCLI() *agentcli.Client {
	return &agentcli.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Deploy returns the container/stack deploy resource module.
func (c *Client) Deploy() *deploy.Client {
	return &deploy.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Marketplace returns the agents/models/solutions resource module.
func (c *Client) Marketplace() *marketplace.Client {
	return &marketplace.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Cloud returns the cloud-provisioning resource module (S3, IAM, VM, …).
func (c *Client) Cloud() *cloud.Client {
	return &cloud.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Connector returns the SDK-direct deploy module — EC2/S3/Cloud Run/Route53/ALB
// provisioned by calling cloud-provider SDKs and REST APIs directly (no
// Terraform). Backed by /api/v2/connector/* on the tenant node.
func (c *Client) Connector() *connector.Client {
	return &connector.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Nodes returns the tenant-node management resource module.
//
// Note: nodes endpoints live on the Infinity control plane (not on a
// per-tenant node), so the InfinityURL is used.
func (c *Client) Nodes() *nodes.Client {
	return &nodes.Client{T: c.t, InfinityURL: c.cfg.infinityURL}
}

// Services returns the lifecycle plane: start/stop/restart/remove a
// container, plus host-level operations under c.Services().VM().
//
// Mirrors the `vxcli services` CLI surface.
func (c *Client) Services() *services.Client {
	return &services.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Networks returns the network-diagnostic resource module — list and
// remote-execute the embedded scripts (DNS, bandwidth, port checks,
// security audits).
func (c *Client) Networks() *networks.Client {
	return &networks.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Agents returns the AI-agent resource module (coding/devops/git/
// parallel/presets/tool/tools).
func (c *Client) Agents() *agents.Client {
	return &agents.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// WebScraper returns the web-research agent module — concurrent BFS crawl,
// multi-engine web search, and deep research (LLM or non-AI extractive).
func (c *Client) WebScraper() *webscraper.Client {
	return &webscraper.Client{T: c.t, NodeURL: c.NodeURL()}
}

// Chat returns the multi-provider AI chat module.
func (c *Client) Chat() *chat.Client {
	return &chat.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Observability returns the backups + migrations + sync module.
func (c *Client) Observability() *observability.Client {
	return &observability.Client{T: c.t, NodeURL: c.NodeURL()}
}

// Billing returns the multicloud cost / optimization module.
func (c *Client) Billing() *billing.Client {
	return &billing.Client{T: c.t, NodeURL: c.NodeURL()}
}

// Workspace returns the /api/v2/setup/* surface — workspace + organization
// lifecycle, cloud-provider credentials, AI-provider credentials,
// API tokens, Git/payment/SMTP/SSL/OAuth/OKTA credential storage.
func (c *Client) Workspace() *workspace.Client {
	return &workspace.Client{
		T:                  c.t,
		NodeURL:            c.NodeURL(),
		AuthedUsername:     c.Whoami().Username,
		AuthedOrganization: c.Whoami().Organization,
	}
}

// VxComputer returns the VXCOMPUTER control-plane module — the node-local
// policy-governed agent runtime (Plan→Act→Reflect loop, risk policy,
// signed approvals, hash-chained audit ledger).
func (c *Client) VxComputer() *vxcomputer.Client {
	return &vxcomputer.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// Robotic returns the Robotic control-cloud module — register robots,
// send commands, push telemetry, request plans, issue fleet commands.
func (c *Client) Robotic() *robotic.Client {
	return &robotic.Client{T: c.t, NodeURL: c.NodeURL()}
}

// VxChrono returns the VxChrono module — the autonomous goal executor and
// scheduler (goals, cron/interval schedules, run lifecycle).
func (c *Client) VxChrono() *vxchrono.Client {
	return &vxchrono.Client{T: c.t, NodeURL: c.NodeURL()}
}

// Workflow returns the Workflow orchestration module — the n8n-style
// visual workflow engine (definitions, validation, execution, exports).
func (c *Client) Workflow() *workflow.Client {
	return &workflow.Client{T: c.t, NodeURL: c.NodeURL()}
}

// MetalDB returns the MetalDB module — self-managed PostgreSQL
// provisioned over SSH onto a customer VM (test-connection + provision).
func (c *Client) MetalDB() *metaldb.Client {
	return &metaldb.Client{T: c.t, NodeURL: c.NodeURL(), AuthedUsername: c.Whoami().Username}
}

// AgentControl returns the AgentControl module — fine-tuning, training,
// knowledge bases, datasets, server-side agents, and GitHub import.
//
// Every AgentControl call sends an X-Tenant-ID header. The tenant id is
// taken from WithTenantID / LoadFromVxcli; override per use by setting
// the returned client's TenantID field.
func (c *Client) AgentControl() *agentcontrol.Client {
	return &agentcontrol.Client{T: c.t, NodeURL: c.NodeURL(), TenantID: c.cfg.tenantID}
}
