// Package marketplace is the resource module for the vxcloud marketplace.
//
// Three sub-resources, all backed by /api/v2/marketplace/* on the tenant
// node:
//
//   - Agents: pre-built AI agents (web scraper, URL status probe,
//     research agent, prompt agent, …). Listed, inspected, and deployed
//     onto a customer-owned VM.
//   - Models: pre-built RAG models (ChromaDB / FAISS retrievers).
//   - Solutions: bundled Terraform plays (RDS, EKS, DynamoDB, GCP VM, …)
//     priced per provision.
package marketplace

import (
	"context"
	"net/url"
	"time"

	"github.com/prodxcloud/vxcloud/transport"
)

// Item is the common shape returned by Agents.List() and Models.List().
type Item struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Available   bool   `json:"available"`
	Description string `json:"description,omitempty"`
}

// Solution is a Terraform marketplace solution.
type Solution struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	CloudProvider string             `json:"cloud_provider"`
	Category      string             `json:"category"`
	Available     bool               `json:"available"`
	Description   string             `json:"description,omitempty"`
	Price         string             `json:"price,omitempty"`
	Variables     []SolutionVariable `json:"variables,omitempty"`
}

// SolutionVariable describes one Terraform variable on a Solution.
type SolutionVariable struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// DeployResult is what /agents/deploy and /models/deploy return.
type DeployResult struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status,omitempty"`
	Hostname  string `json:"hostname,omitempty"`
	AccessURL string `json:"access_url,omitempty"`
}

// ProvisionResult is what /provision returns for terraform solutions.
type ProvisionResult struct {
	SessionID        string                 `json:"session_id"`
	Status           string                 `json:"status,omitempty"`
	ExecutionTime    string                 `json:"execution_time,omitempty"`
	StatePath        string                 `json:"state_path,omitempty"`
	TerraformOutputs map[string]interface{} `json:"terraform_outputs,omitempty"`
	CreatedAt        time.Time              `json:"created_at,omitempty"`
}

// agentsEnvelope, modelsEnvelope, templatesEnvelope: marketplace responses
// don't share the cicd-style {data, count} shape — each endpoint wraps its
// list under a domain-specific key.
type agentsEnvelope struct {
	Agents []Item `json:"agents"`
}
type modelsEnvelope struct {
	Models []Item `json:"models"`
}
type templatesEnvelope struct {
	Templates []Solution `json:"templates"`
}

// Client is the marketplace facade.
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// Agents returns the agents sub-client.
func (c *Client) Agents() *Agents { return &Agents{c: c} }

// Models returns the models sub-client.
func (c *Client) Models() *Models { return &Models{c: c} }

// Solutions returns the solutions sub-client.
func (c *Client) Solutions() *Solutions { return &Solutions{c: c} }

// ── Agents ────────────────────────────────────────────────────────

// Agents wraps /api/v2/marketplace/agents.
type Agents struct{ c *Client }

// List returns every available agent.
func (a *Agents) List(ctx context.Context) ([]Item, error) {
	u := transport.JoinURL(a.c.NodeURL, "/api/v2/marketplace/agents")
	var out agentsEnvelope
	if err := a.c.T.JSON(ctx, "marketplace.Agents.List", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return out.Agents, nil
}

// Show returns metadata for one agent. The single-item endpoint returns
// the bare object without an envelope key.
func (a *Agents) Show(ctx context.Context, agentID string) (*Item, error) {
	u := transport.JoinURL(a.c.NodeURL, "/api/v2/marketplace/agents/"+url.PathEscape(agentID))
	var out Item
	if err := a.c.T.JSON(ctx, "marketplace.Agents.Show", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AgentDeployOpts describes a marketplace agent deploy.
type AgentDeployOpts struct {
	Host         string // SSH target (required)
	SSHUser      string // SSH login user (required)
	KeyPairName  string // workspace vault key entry (required)
	AgentName    string // display name (defaults to agent_id)
	HTTPPort     string // host nginx port (default "80")
	AppPort      string // container port (defaults to agent default)
	SystemPrompt string // baked into .env for prompt-driven agents
	EnvVars      string // newline-separated KEY=VALUE pairs
	Version      string // default "1.0.0"
}

// Deploy installs an agent onto a customer-owned VM.
//
// Endpoint: POST /api/v2/marketplace/agents/deploy.
func (a *Agents) Deploy(ctx context.Context, agentID string, opts AgentDeployOpts) (*DeployResult, error) {
	body := map[string]string{
		"agent_id":      agentID,
		"hostname":      opts.Host,
		"ssh_username":  opts.SSHUser,
		"key_pair_name": opts.KeyPairName,
		"username":      a.c.AuthedUsername,
	}
	if opts.AgentName != "" {
		body["agent_name"] = opts.AgentName
	}
	if opts.HTTPPort != "" {
		body["http_port"] = opts.HTTPPort
	}
	if opts.AppPort != "" {
		body["app_port"] = opts.AppPort
	}
	if opts.SystemPrompt != "" {
		body["system_prompt"] = opts.SystemPrompt
	}
	if opts.EnvVars != "" {
		body["env_vars"] = opts.EnvVars
	}
	if opts.Version != "" {
		body["version"] = opts.Version
	}
	u := transport.JoinURL(a.c.NodeURL, "/api/v2/marketplace/agents/deploy")
	var out DeployResult
	if err := a.c.T.JSON(ctx, "marketplace.Agents.Deploy", "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Models ────────────────────────────────────────────────────────

// Models wraps /api/v2/marketplace/models.
type Models struct{ c *Client }

// List returns every available model.
func (m *Models) List(ctx context.Context) ([]Item, error) {
	u := transport.JoinURL(m.c.NodeURL, "/api/v2/marketplace/models")
	var out modelsEnvelope
	if err := m.c.T.JSON(ctx, "marketplace.Models.List", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// Show returns metadata for one model.
func (m *Models) Show(ctx context.Context, modelID string) (*Item, error) {
	u := transport.JoinURL(m.c.NodeURL, "/api/v2/marketplace/models/"+url.PathEscape(modelID))
	var out Item
	if err := m.c.T.JSON(ctx, "marketplace.Models.Show", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Deploy installs a model onto a customer-owned VM.
//
// Endpoint: POST /api/v2/marketplace/models/deploy. Same opts shape as
// agent deploys.
func (m *Models) Deploy(ctx context.Context, modelID string, opts AgentDeployOpts) (*DeployResult, error) {
	body := map[string]string{
		"model_id":      modelID,
		"hostname":      opts.Host,
		"ssh_username":  opts.SSHUser,
		"key_pair_name": opts.KeyPairName,
		"username":      m.c.AuthedUsername,
	}
	if opts.AgentName != "" {
		body["model_name"] = opts.AgentName
	}
	if opts.HTTPPort != "" {
		body["http_port"] = opts.HTTPPort
	}
	if opts.AppPort != "" {
		body["app_port"] = opts.AppPort
	}
	if opts.EnvVars != "" {
		body["env_vars"] = opts.EnvVars
	}
	if opts.Version != "" {
		body["version"] = opts.Version
	}
	u := transport.JoinURL(m.c.NodeURL, "/api/v2/marketplace/models/deploy")
	var out DeployResult
	if err := m.c.T.JSON(ctx, "marketplace.Models.Deploy", "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Solutions (Terraform marketplace) ─────────────────────────────

// Solutions wraps /api/v2/marketplace/templates and /api/v2/marketplace/provision.
type Solutions struct{ c *Client }

// List returns every available solution.
func (s *Solutions) List(ctx context.Context) ([]Solution, error) {
	u := transport.JoinURL(s.c.NodeURL, "/api/v2/marketplace/templates")
	var out templatesEnvelope
	if err := s.c.T.JSON(ctx, "marketplace.Solutions.List", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

// Show returns metadata for one solution, including its variables.
func (s *Solutions) Show(ctx context.Context, solutionID string) (*Solution, error) {
	u := transport.JoinURL(s.c.NodeURL, "/api/v2/marketplace/templates/"+url.PathEscape(solutionID))
	var out Solution
	if err := s.c.T.JSON(ctx, "marketplace.Solutions.Show", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ProvisionOpts describes a Terraform-solution provision.
type ProvisionOpts struct {
	TemplateID    string // marketplace solution ID (required)
	ResourceName  string // user-chosen resource prefix (required)
	CloudProvider string // aws | gcp | azure | linode | …
	Region        string
	Environment   string                 // "development" | "staging" | "production"
	Variables     map[string]interface{} // Terraform input variables
}

// Provision runs a marketplace solution end-to-end (terraform init+plan+apply).
//
// Endpoint: POST /api/v2/marketplace/provision.
func (s *Solutions) Provision(ctx context.Context, opts ProvisionOpts) (*ProvisionResult, error) {
	if opts.Environment == "" {
		opts.Environment = "development"
	}
	body := map[string]interface{}{
		"template_name":  opts.TemplateID,
		"resource_name":  opts.ResourceName,
		"cloud_provider": opts.CloudProvider,
		"region":         opts.Region,
		"environment":    opts.Environment,
		"variables":      opts.Variables,
		"username":       s.c.AuthedUsername,
	}
	u := transport.JoinURL(s.c.NodeURL, "/api/v2/marketplace/provision")
	var out ProvisionResult
	if err := s.c.T.JSON(ctx, "marketplace.Solutions.Provision", "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
