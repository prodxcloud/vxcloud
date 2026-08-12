// Package cicd is the resource module for CI/CD pipelines and builds.
package cicd

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/prodxcloud/vxcloud/transport"
)

// Pipeline is a CI/CD pipeline row.
//
// Field tags match the wire format produced by /api/v2/cicd/pipelines.
type Pipeline struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	Provider      string    `json:"provider"`
	RepositoryURL string    `json:"repository_url"`
	Branch        string    `json:"branch,omitempty"`
	Stack         string    `json:"stack,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// Build is one execution of a Pipeline.
type Build struct {
	ID          string    `json:"id"`
	PipelineID  string    `json:"pipeline_id"`
	Status      string    `json:"status"`
	Branch      string    `json:"branch"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	TriggeredBy string    `json:"triggered_by,omitempty"`
	LogsURL     string    `json:"logs_url,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	StartedAt   time.Time `json:"build_started_at,omitempty"`
	CompletedAt time.Time `json:"build_completed_at,omitempty"`
	Error       string    `json:"error_message,omitempty"`
}

// listEnvelope mirrors the standard FastAPI list response: {"data": [...], "count": N}.
type listEnvelope[T any] struct {
	Data  []T `json:"data"`
	Count int `json:"count"`
}

// itemEnvelope mirrors the standard single-item response: {"data": {...}}.
type itemEnvelope[T any] struct {
	Data T `json:"data"`
}

// Client is the cicd facade.
type Client struct {
	T       *transport.Transport
	NodeURL string
}

// Pipelines returns the pipelines sub-client.
func (c *Client) Pipelines() *Pipelines { return &Pipelines{c: c} }

// Builds returns the builds sub-client.
func (c *Client) Builds() *Builds { return &Builds{c: c} }

// Pipelines is the sub-client for /api/v2/cicd/pipelines.
type Pipelines struct{ c *Client }

// List returns all pipelines visible to the authenticated workspace.
func (p *Pipelines) List(ctx context.Context) ([]Pipeline, error) {
	u := transport.JoinURL(p.c.NodeURL, "/api/v2/cicd/pipelines")
	var out listEnvelope[Pipeline]
	if err := p.c.T.JSON(ctx, "cicd.Pipelines.List", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Show returns one pipeline by ID.
func (p *Pipelines) Show(ctx context.Context, pipelineID string) (*Pipeline, error) {
	u := transport.JoinURL(p.c.NodeURL, "/api/v2/cicd/pipelines/"+url.PathEscape(pipelineID))
	var out itemEnvelope[Pipeline]
	if err := p.c.T.JSON(ctx, "cicd.Pipelines.Show", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// CreateInput describes a pipeline to create. CIEngine selects the CI system:
// "github-actions", "gitlab-ci", "bitbucket-pipelines", "jenkins", "gitea",
// "forgejo". For the server-based engines (jenkins/gitea/forgejo) set CIServerMode
// to "managed" (VxCloud-hosted) or "self_hosted"/"custom" and supply
// CIServerURL/User/Token — the runner uses these to reach your server.
type CreateInput struct {
	Name            string
	RepositoryURL   string
	Branch          string // defaults to "main"
	Provider        string // github | gitlab | bitbucket | gitea | forgejo | git
	Stack           string // python | nodejs | react | golang | …
	CIEngine        string // defaults to "github-actions"
	WorkflowRelPath string // defaults per engine
	WorkflowYAML    string // optional inline workflow content
	CIServerMode    string // managed | self_hosted | custom (server engines)
	CIServerURL     string
	CIServerUser    string
	CIServerToken   string
	AppPort         string // optional, defaults "8000"
}

// defaultWorkflowRelPath mirrors the CLI/backend per-engine default.
func defaultWorkflowRelPath(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "jenkins":
		return "Jenkinsfile"
	case "gitlab-ci", "gitlab_ci", "gitlab":
		return ".gitlab-ci.yml"
	case "bitbucket-pipelines", "bitbucket":
		return "bitbucket-pipelines.yml"
	case "gitea":
		return ".gitea/workflows/ci.yml"
	case "forgejo":
		return ".forgejo/workflows/ci.yml"
	default:
		return ".github/workflows/normal.yml"
	}
}

func engineNeedsCIServer(engine string) bool {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "jenkins", "gitea", "forgejo":
		return true
	}
	return false
}

// Create provisions a new pipeline. The returned Pipeline carries the new ID,
// which you can pass to Trigger.
func (p *Pipelines) Create(ctx context.Context, in CreateInput) (*Pipeline, error) {
	engine := in.CIEngine
	if strings.TrimSpace(engine) == "" {
		engine = "github-actions"
	}
	provider := in.Provider
	if strings.TrimSpace(provider) == "" {
		provider = "github"
	}
	branch := in.Branch
	if strings.TrimSpace(branch) == "" {
		branch = "main"
	}
	wfPath := in.WorkflowRelPath
	if strings.TrimSpace(wfPath) == "" {
		wfPath = defaultWorkflowRelPath(engine)
	}
	appPort := in.AppPort
	if strings.TrimSpace(appPort) == "" {
		appPort = "8000"
	}

	config := map[string]any{
		"git_provider":      provider,
		"app_type":          "fullstack",
		"port":              appPort,
		"template_id":       in.Stack,
		"ci_engine":         engine,
		"workflow_rel_path": wfPath,
	}
	if strings.TrimSpace(in.WorkflowYAML) != "" {
		config["workflow_yaml"] = in.WorkflowYAML
	}
	if engineNeedsCIServer(engine) {
		mode := in.CIServerMode
		if strings.TrimSpace(mode) == "" {
			mode = "managed"
		}
		config["ci_server_mode"] = mode
		if mode != "managed" {
			config["ci_server_url"] = in.CIServerURL
			config["ci_server_user"] = in.CIServerUser
			config["ci_server_token"] = in.CIServerToken
		}
	}

	body := map[string]any{
		"name":           in.Name,
		"provider":       provider,
		"repository_url": in.RepositoryURL,
		"branch":         branch,
		"stack":          in.Stack,
		"framework":      in.Stack,
		"config":         config,
	}

	u := transport.JoinURL(p.c.NodeURL, "/api/v2/cicd/pipelines")
	var out itemEnvelope[Pipeline]
	if err := p.c.T.JSON(ctx, "cicd.Pipelines.Create", "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// Trigger fires a build for a pipeline. Returns the queued Build.
func (p *Pipelines) Trigger(ctx context.Context, pipelineID, branch string) (*Build, error) {
	u := transport.JoinURL(p.c.NodeURL, "/api/v2/cicd/pipelines/"+url.PathEscape(pipelineID)+"/trigger")
	body := map[string]string{"branch": branch}
	var out itemEnvelope[Build]
	if err := p.c.T.JSON(ctx, "cicd.Pipelines.Trigger", "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// Builds is the sub-client for /api/v2/cicd/builds.
type Builds struct{ c *Client }

// Show returns a single build by ID.
func (b *Builds) Show(ctx context.Context, buildID string) (*Build, error) {
	u := transport.JoinURL(b.c.NodeURL, "/api/v2/cicd/builds/"+url.PathEscape(buildID))
	var out itemEnvelope[Build]
	if err := b.c.T.JSON(ctx, "cicd.Builds.Show", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}
