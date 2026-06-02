// Package agentcontrol is the resource module for the AgentControl
// surface — fine-tuning jobs, model training, knowledge bases, datasets,
// server-side agents, and GitHub dataset import.
//
// Every AgentControl request carries an X-Tenant-ID header. Set the
// tenant id once (via vxsdk.WithTenantID, LoadFromVxcli, or the Client's
// TenantID field) and it applies to every sub-resource call.
//
// Endpoints (all on the per-tenant node):
//
//	GET  /api/v2/agentcontrol/summary
//	GET  /api/v2/agentcontrol/{fine-tuning,training,knowledge,datasets,agents}/
//	GET  /api/v2/agentcontrol/{...}/{id}
//	POST /api/v2/agentcontrol/{fine-tuning,training,knowledge}/
//	GET  /api/v2/agentcontrol/datasets/{id}/preview
//	POST /api/v2/agentcontrol/datasets/upload                 (multipart)
//	POST /api/v2/agentcontrol/agents/{id}/execute
//	GET  /api/v2/agentcontrol/github/repos
//	POST /api/v2/agentcontrol/github/import-dataset
package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// Client is the AgentControl facade. Construct via c.AgentControl().
type Client struct {
	T       *transport.Transport
	NodeURL string
	// TenantID is sent as X-Tenant-ID on every call. Required.
	TenantID string
}

// FineTuning returns the fine-tuning-jobs sub-resource.
func (c *Client) FineTuning() *FineTuning { return &FineTuning{c, "fine-tuning"} }

// Training returns the model-training-jobs sub-resource.
func (c *Client) Training() *Training { return &Training{c, "training"} }

// Knowledge returns the knowledge-bases sub-resource.
func (c *Client) Knowledge() *Knowledge { return &Knowledge{c, "knowledge"} }

// Datasets returns the datasets sub-resource.
func (c *Client) Datasets() *Datasets { return &Datasets{c, "datasets"} }

// Agents returns the server-side agents sub-resource. These are distinct
// from c.Agents() (client-side AI orchestration) — they persist in the
// agentcontrol DB and run via /agents/{id}/execute.
func (c *Client) Agents() *Agents { return &Agents{c, "agents"} }

// GitHub returns the GitHub dataset-import sub-resource.
func (c *Client) GitHub() *GitHub { return &GitHub{c, "github"} }

// Embeddings returns the vector-artifacts sub-resource (FAISS / ChromaDB).
func (c *Client) Embeddings() *Embeddings { return &Embeddings{c, "embeddings"} }

// Tools returns the tools & actions sub-resource.
func (c *Client) Tools() *Tools { return &Tools{c, "tools"} }

// MCP returns the MCP-servers sub-resource. Note: per project-mcp-split-brain
// memory, the MCP route is Cloudflare bot-blocked for non-browser callers on
// some environments; expect HTTP 403 "error code: 1010" if so. Use a browser
// User-Agent header on the Transport to work around it.
func (c *Client) MCP() *MCP { return &MCP{c, "mcp-servers"} }

// Evals returns the evaluation-runs sub-resource (Benchmarks tab).
func (c *Client) Evals() *Evals { return &Evals{c, "evals"} }

// Code returns the Programming-tab sub-resource — run + persist editor content.
func (c *Client) Code() *Code { return &Code{c, "code"} }

// Models returns the agentcontrol-side Models sub-resource (UI uploads,
// soft-delete, state changes). Distinct from the marketplace catalog.
func (c *Client) Models() *Models { return &Models{c, "models"} }

// Deployments returns the agentcontrol Deployments sub-resource (model
// endpoints — create, sync, status, delete).
func (c *Client) Deployments() *Deployments { return &Deployments{c, "deployments"} }

// WebAssets returns the Web-Assets sub-resource.
func (c *Client) WebAssets() *WebAssets { return &WebAssets{c, "web-assets"} }

// Benchmarks returns the benchmarks sub-resource (separate from Evals).
func (c *Client) Benchmarks() *Benchmarks { return &Benchmarks{c, "benchmarks"} }

// Catalog returns the model-catalog sub-resource (Browse Models in the UI).
func (c *Client) Catalog() *Catalog { return &Catalog{c, "catalog"} }

// Health returns the model-health sub-resource.
func (c *Client) Health() *Health { return &Health{c, "health"} }

// Events returns the event-bus sub-resource (Kafka/Redis/Celery status).
func (c *Client) Events() *Events { return &Events{c, "events"} }

// LLM returns the LLM-chat + providers sub-resource.
func (c *Client) LLM() *LLM { return &LLM{c, "llm"} }

// DeployTargets returns the deploy-target provisioning sub-resource.
func (c *Client) DeployTargets() *DeployTargets { return &DeployTargets{c, "deploy-targets"} }

// Workflows returns the workflow-shim sub-resource (list + trigger; the
// fuller workflow surface lives on the workflow service, not agentcontrol).
func (c *Client) Workflows() *Workflows { return &Workflows{c, "workflows"} }

// Infra returns the infrastructure-discovery sub-resource (endpoint list).
func (c *Client) Infra() *Infra { return &Infra{c, "infra"} }

// Summary returns the AgentControl dashboard summary for the tenant.
func (c *Client) Summary(ctx context.Context) (Result, error) {
	h, err := c.headers()
	if err != nil {
		return nil, err
	}
	var out Result
	u := transport.JoinURL(c.NodeURL, "/api/v2/agentcontrol/summary")
	if err := c.T.JSONWithHeaders(ctx, "agentcontrol.Summary", "GET", u, nil, &out, h); err != nil {
		return nil, fmt.Errorf("agentcontrol.Summary: %w", err)
	}
	return out, nil
}

// ── internal helpers ─────────────────────────────────────────────────

func (c *Client) headers() (map[string]string, error) {
	if c.TenantID == "" {
		return nil, errors.New("agentcontrol: TenantID is required — set vxsdk.WithTenantID, " +
			"use LoadFromVxcli, or set the AgentControl client's TenantID field")
	}
	return map[string]string{"X-Tenant-ID": c.TenantID}, nil
}

// acURL builds /api/v2/agentcontrol/<path>/<suffix>. With an empty suffix
// it returns the collection URL with a trailing slash (list/create want it).
func (c *Client) acURL(path, suffix string) string {
	base := transport.JoinURL(c.NodeURL, "/api/v2/agentcontrol/"+path)
	if suffix == "" {
		return base + "/"
	}
	return base + "/" + strings.TrimPrefix(suffix, "/")
}

func (c *Client) do(ctx context.Context, op, method, url string, body interface{}) (Result, error) {
	h, err := c.headers()
	if err != nil {
		return nil, err
	}
	var out Result
	if err := c.T.JSONWithHeaders(ctx, op, method, url, body, &out, h); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

func (c *Client) doList(ctx context.Context, op, path string) ([]Result, error) {
	body, err := c.do(ctx, op, "GET", c.acURL(path, ""), nil)
	if err != nil {
		return nil, err
	}
	return asResultSlice(body["items"]), nil
}

// doDelete is a convenience for endpoints that return either a typed JSON
// body or nothing. Body is read but the caller doesn't usually need it.
func (c *Client) doDelete(ctx context.Context, op, url string) (Result, error) {
	return c.do(ctx, op, "DELETE", url, nil)
}

// doBytes fetches a binary body (used for /datasets/{id}/download and
// /embeddings/{id}/download). Uses the new Transport.BytesWithHeaders helper.
func (c *Client) doBytes(ctx context.Context, op, url string) ([]byte, error) {
	h, err := c.headers()
	if err != nil {
		return nil, err
	}
	out, err := c.T.BytesWithHeaders(ctx, op, "GET", url, h)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

func asResultSlice(v interface{}) []Result {
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]Result, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

// ── long-running-job poller ──────────────────────────────────────────

// terminalStatuses covers fine-tuning, training, and knowledge-base rows.
var terminalStatuses = map[string]bool{
	"succeeded": true, "failed": true, "cancelled": true,
	"ready": true, "error": true,
}

// WaitOptions tunes the job poller. Zero values fall back to the defaults.
type WaitOptions struct {
	Timeout  time.Duration // default 30m
	Interval time.Duration // default 5s
	// OnTick, if set, is called with the latest job Result after each poll.
	OnTick func(Result)
}

// waitForJob polls getURL until the job's status is terminal or the
// timeout elapses. Shared by FineTuning / Training / Knowledge.
func (c *Client) waitForJob(ctx context.Context, op, getURL string, opts WaitOptions) (Result, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		job, err := c.do(ctx, op, "GET", getURL, nil)
		if err != nil {
			return nil, err
		}
		if opts.OnTick != nil {
			opts.OnTick(job)
		}
		status, _ := job["status"].(string)
		if terminalStatuses[strings.ToLower(status)] {
			return job, nil
		}
		if time.Now().After(deadline) {
			return job, fmt.Errorf("%s: timed out after %s; last status=%q", op, timeout, status)
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(interval):
		}
	}
}

// ── FineTuning ───────────────────────────────────────────────────────

// FineTuning is the fine-tuning-jobs sub-resource.
type FineTuning struct {
	c    *Client
	path string
}

// List returns all fine-tuning jobs for the tenant.
func (r *FineTuning) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.FineTuning.List", r.path)
}

// Get returns one fine-tuning job.
func (r *FineTuning) Get(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.FineTuning.Get: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.FineTuning.Get", "GET", r.c.acURL(r.path, jobID), nil)
}

// Create starts a fine-tuning job. spec must include at least name,
// base_model and training_file.
func (r *FineTuning) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.FineTuning.Create", spec, "name", "base_model", "training_file"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.FineTuning.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Wait polls a fine-tuning job until its status is terminal.
func (r *FineTuning) Wait(ctx context.Context, jobID string, opts WaitOptions) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.FineTuning.Wait: jobID is required")
	}
	return r.c.waitForJob(ctx, "agentcontrol.FineTuning.Wait", r.c.acURL(r.path, jobID), opts)
}

// ── Training ─────────────────────────────────────────────────────────

// Training is the model-training-jobs sub-resource.
type Training struct {
	c    *Client
	path string
}

// List returns all training jobs for the tenant.
func (r *Training) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Training.List", r.path)
}

// Get returns one training job.
func (r *Training) Get(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Get: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Get", "GET", r.c.acURL(r.path, jobID), nil)
}

// Create starts a training job. spec must include at least name,
// base_model and dataset_id.
func (r *Training) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Training.Create", spec, "name", "base_model", "dataset_id"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Training.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Wait polls a training job until its status is terminal.
func (r *Training) Wait(ctx context.Context, jobID string, opts WaitOptions) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Wait: jobID is required")
	}
	return r.c.waitForJob(ctx, "agentcontrol.Training.Wait", r.c.acURL(r.path, jobID), opts)
}

// ── Knowledge ────────────────────────────────────────────────────────

// Knowledge is the knowledge-bases sub-resource.
type Knowledge struct {
	c    *Client
	path string
}

// List returns all knowledge bases for the tenant.
func (r *Knowledge) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Knowledge.List", r.path)
}

// Get returns one knowledge base.
func (r *Knowledge) Get(ctx context.Context, kbID string) (Result, error) {
	if kbID == "" {
		return nil, errors.New("agentcontrol.Knowledge.Get: kbID is required")
	}
	return r.c.do(ctx, "agentcontrol.Knowledge.Get", "GET", r.c.acURL(r.path, kbID), nil)
}

// Create builds a knowledge base. spec must include at least name.
func (r *Knowledge) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Knowledge.Create", spec, "name"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Knowledge.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Wait polls a knowledge base until its status is terminal.
func (r *Knowledge) Wait(ctx context.Context, kbID string, opts WaitOptions) (Result, error) {
	if kbID == "" {
		return nil, errors.New("agentcontrol.Knowledge.Wait: kbID is required")
	}
	return r.c.waitForJob(ctx, "agentcontrol.Knowledge.Wait", r.c.acURL(r.path, kbID), opts)
}

// ── Datasets ─────────────────────────────────────────────────────────

// Datasets is the datasets sub-resource.
type Datasets struct {
	c    *Client
	path string
}

// List returns all datasets for the tenant.
func (r *Datasets) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Datasets.List", r.path)
}

// Get returns one dataset.
func (r *Datasets) Get(ctx context.Context, datasetID string) (Result, error) {
	if datasetID == "" {
		return nil, errors.New("agentcontrol.Datasets.Get: datasetID is required")
	}
	return r.c.do(ctx, "agentcontrol.Datasets.Get", "GET", r.c.acURL(r.path, datasetID), nil)
}

// Preview returns a sample of a dataset's rows.
func (r *Datasets) Preview(ctx context.Context, datasetID string) (Result, error) {
	if datasetID == "" {
		return nil, errors.New("agentcontrol.Datasets.Preview: datasetID is required")
	}
	return r.c.do(ctx, "agentcontrol.Datasets.Preview", "GET", r.c.acURL(r.path, datasetID+"/preview"), nil)
}

// UploadOptions describes a dataset upload.
type UploadOptions struct {
	Name   string // dataset name (required)
	Type   string // dataset type; default "training"
	Format string // file format; default "csv"
}

// Upload creates a dataset from in-memory file content. The SDK never
// reads from disk — the caller supplies the bytes and a filename.
func (r *Datasets) Upload(ctx context.Context, content []byte, filename string, opts UploadOptions) (Result, error) {
	if opts.Name == "" {
		return nil, errors.New("agentcontrol.Datasets.Upload: Name is required")
	}
	if filename == "" {
		return nil, errors.New("agentcontrol.Datasets.Upload: filename is required")
	}
	h, err := r.c.headers()
	if err != nil {
		return nil, err
	}
	fields := map[string]string{
		"name":   opts.Name,
		"type":   orElse(opts.Type, "training"),
		"format": orElse(opts.Format, "csv"),
	}
	files := []transport.FilePart{{
		Field: "file", Filename: filename, Content: content, ContentType: "text/csv",
	}}
	var out Result
	u := r.c.acURL(r.path, "upload")
	if err := r.c.T.MultipartWithHeaders(ctx, "agentcontrol.Datasets.Upload", u, fields, files, &out, h); err != nil {
		return nil, fmt.Errorf("agentcontrol.Datasets.Upload: %w", err)
	}
	return out, nil
}

// ── Agents (server-side) ─────────────────────────────────────────────

// Agents is the server-side agents sub-resource.
type Agents struct {
	c    *Client
	path string
}

// List returns all server-side agents for the tenant.
func (r *Agents) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Agents.List", r.path)
}

// Execute runs a server-side agent against a task. extra fields, if any,
// are merged into the request body.
func (r *Agents) Execute(ctx context.Context, agentID, task string, extra map[string]interface{}) (Result, error) {
	if agentID == "" {
		return nil, errors.New("agentcontrol.Agents.Execute: agentID is required")
	}
	body := map[string]interface{}{"task": task}
	for k, v := range extra {
		body[k] = v
	}
	return r.c.do(ctx, "agentcontrol.Agents.Execute", "POST", r.c.acURL(r.path, agentID+"/execute"), body)
}

// ── GitHub ───────────────────────────────────────────────────────────

// GitHub is the GitHub dataset-import sub-resource.
type GitHub struct {
	c    *Client
	path string
}

// ListRepos lists the GitHub repositories visible to the tenant.
func (r *GitHub) ListRepos(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.GitHub.ListRepos", "GET", r.c.acURL(r.path, "repos"), nil)
}

// ImportDatasetInput describes a GitHub dataset import.
type ImportDatasetInput struct {
	Repo   string // owner/name (required)
	Branch string // default "main"
	Path   string // path within the repo
	Name   string // dataset name; defaults to the repo basename
}

// ImportDataset imports a dataset from a GitHub repository.
func (r *GitHub) ImportDataset(ctx context.Context, in ImportDatasetInput) (Result, error) {
	if in.Repo == "" {
		return nil, errors.New("agentcontrol.GitHub.ImportDataset: Repo is required")
	}
	name := in.Name
	if name == "" {
		parts := strings.Split(in.Repo, "/")
		name = parts[len(parts)-1]
	}
	body := map[string]interface{}{
		"repo": in.Repo, "branch": orElse(in.Branch, "main"),
		"path": in.Path, "name": name,
	}
	return r.c.do(ctx, "agentcontrol.GitHub.ImportDataset", "POST", r.c.acURL(r.path, "import-dataset"), body)
}

// ── shared small helpers ─────────────────────────────────────────────

func requireKeys(op string, spec map[string]interface{}, keys ...string) error {
	if spec == nil {
		return fmt.Errorf("%s: spec is required", op)
	}
	for _, k := range keys {
		v, ok := spec[k]
		if !ok || v == nil || v == "" {
			return fmt.Errorf("%s: spec[%q] is required", op, k)
		}
	}
	return nil
}

func orElse(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
