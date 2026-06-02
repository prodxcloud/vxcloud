// agentcontrol_new.go — sub-resources added to reach UI parity with the
// /api/v2/agentcontrol/* surface. The original agentcontrol.go covers
// FineTuning, Training, Knowledge, Datasets, Agents, GitHub and Summary;
// this file adds Embeddings, Tools, MCP, Evals, Code, Models, Deployments,
// WebAssets, Benchmarks, Catalog, Health, Events, LLM, DeployTargets,
// Workflows-shim and Infra. Conventions match the existing file: every
// sub-resource is a {*Client, path} struct; all I/O routes through Client.do
// / doList / doDelete / doBytes; validation uses requireKeys.
package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// ── Embeddings ───────────────────────────────────────────────────────

// Embeddings is the vector-artifacts sub-resource. Backs the Embeddings tab.
type Embeddings struct {
	c    *Client
	path string
}

// List returns all embedding artifacts for the tenant. The collection URL
// uses no trailing slash on this route (unlike the others) — matches
// /api/v2/agentcontrol/embeddings.
func (r *Embeddings) List(ctx context.Context) ([]Result, error) {
	body, err := r.c.do(ctx, "agentcontrol.Embeddings.List", "GET",
		r.c.acURL(r.path, ""), nil)
	if err != nil {
		return nil, err
	}
	return asResultSlice(body["items"]), nil
}

// Query runs a semantic query against an artifact and returns the top-K hits.
// question is the natural-language query; topK defaults to 5 when <= 0.
func (r *Embeddings) Query(ctx context.Context, id, question string, topK int) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Embeddings.Query: id is required")
	}
	if question == "" {
		return nil, errors.New("agentcontrol.Embeddings.Query: question is required")
	}
	if topK <= 0 {
		topK = 5
	}
	return r.c.do(ctx, "agentcontrol.Embeddings.Query", "POST",
		r.c.acURL(r.path, id+"/query"),
		map[string]interface{}{"question": question, "top_k": topK})
}

// Download fetches a raw zip part of the embedding bundle. `part` is one of
// "faiss" or "chromadb" (other parts may be added server-side later).
func (r *Embeddings) Download(ctx context.Context, id, part string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Embeddings.Download: id is required")
	}
	if part == "" {
		return nil, errors.New("agentcontrol.Embeddings.Download: part is required")
	}
	u := r.c.acURL(r.path, id+"/download") + "?part=" + url.QueryEscape(part)
	return r.c.doBytes(ctx, "agentcontrol.Embeddings.Download", u)
}

// Visualize returns 2D PCA coordinates for the artifact's vectors. maxPoints
// defaults to 400 when <= 0.
func (r *Embeddings) Visualize(ctx context.Context, id string, maxPoints int) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Embeddings.Visualize: id is required")
	}
	if maxPoints <= 0 {
		maxPoints = 400
	}
	u := fmt.Sprintf("%s?max_points=%d", r.c.acURL(r.path, id+"/visualize"), maxPoints)
	return r.c.do(ctx, "agentcontrol.Embeddings.Visualize", "GET", u, nil)
}

// Promote registers the embedding artifact as a production model so it shows
// up in Models & Endpoints. Returns the new model_id.
func (r *Embeddings) Promote(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Embeddings.Promote: id is required")
	}
	return r.c.do(ctx, "agentcontrol.Embeddings.Promote", "POST",
		r.c.acURL(r.path, id+"/promote"), map[string]interface{}{})
}

// Upload accepts a pre-built embedding bundle as base64-encoded zip bytes.
// The SDK never reads from disk — caller supplies the bytes.
func (r *Embeddings) Upload(ctx context.Context, filename, contentBase64 string) (Result, error) {
	if filename == "" {
		return nil, errors.New("agentcontrol.Embeddings.Upload: filename is required")
	}
	if contentBase64 == "" {
		return nil, errors.New("agentcontrol.Embeddings.Upload: contentBase64 is required")
	}
	return r.c.do(ctx, "agentcontrol.Embeddings.Upload", "POST",
		r.c.acURL(r.path, "upload"),
		map[string]interface{}{"filename": filename, "content_base64": contentBase64})
}

// Delete removes a single embedding artifact (rows + on-disk bytes).
func (r *Embeddings) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Embeddings.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Embeddings.Delete", r.c.acURL(r.path, id))
}

// DeleteAll wipes every embedding artifact for the tenant. Requires the
// caller to opt in via confirm=true (also reflected in the UI's red button).
func (r *Embeddings) DeleteAll(ctx context.Context) (Result, error) {
	u := r.c.acURL(r.path, "") + "?confirm=true"
	// Trim the trailing slash from the collection URL — DELETE on the
	// list endpoint uses the un-slashed form (matches UI: DELETE /embeddings?confirm=true).
	u = trimCollectionSlash(u)
	return r.c.doDelete(ctx, "agentcontrol.Embeddings.DeleteAll", u)
}

// ── Tools ────────────────────────────────────────────────────────────

// Tools is the tools & actions sub-resource.
type Tools struct {
	c    *Client
	path string
}

// List returns all tools for the tenant.
func (r *Tools) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Tools.List", r.path)
}

// Create adds a tool. spec must include at least name.
func (r *Tools) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Tools.Create", spec, "name"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Tools.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Update patches a tool (toggle enabled, rename, etc.). PATCH semantics.
func (r *Tools) Update(ctx context.Context, id string, patch map[string]interface{}) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Tools.Update: id is required")
	}
	if patch == nil {
		return nil, errors.New("agentcontrol.Tools.Update: patch is required")
	}
	return r.c.do(ctx, "agentcontrol.Tools.Update", "PATCH", r.c.acURL(r.path, id), patch)
}

// Delete removes a tool.
func (r *Tools) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Tools.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Tools.Delete", r.c.acURL(r.path, id))
}

// ── MCP servers ──────────────────────────────────────────────────────

// MCP is the MCP-servers sub-resource.
type MCP struct {
	c    *Client
	path string
}

// List returns all MCP servers registered for the tenant.
func (r *MCP) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.MCP.List", r.path)
}

// Create registers an MCP server. spec must include at least name and url.
func (r *MCP) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.MCP.Create", spec, "name", "url"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.MCP.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Refresh re-probes the server (HEAD on HTTP/SSE/WS, 3-second timeout) and
// returns the row with an updated `status` field.
func (r *MCP) Refresh(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.MCP.Refresh: id is required")
	}
	return r.c.do(ctx, "agentcontrol.MCP.Refresh", "POST",
		r.c.acURL(r.path, id+"/refresh"), map[string]interface{}{})
}

// Delete removes an MCP-server registration.
func (r *MCP) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.MCP.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.MCP.Delete", r.c.acURL(r.path, id))
}

// ── Evals ────────────────────────────────────────────────────────────

// Evals is the evaluation-runs sub-resource (Benchmarks tab).
type Evals struct {
	c    *Client
	path string
}

// ListRuns returns all eval runs for the tenant. Mirrors the UI's
// /evals/runs/ listing.
func (r *Evals) ListRuns(ctx context.Context) ([]Result, error) {
	body, err := r.c.do(ctx, "agentcontrol.Evals.ListRuns", "GET",
		r.c.acURL(r.path, "runs/"), nil)
	if err != nil {
		return nil, err
	}
	return asResultSlice(body["items"]), nil
}

// CreateRun starts an eval run. spec must include at least name. The SDK
// auto-injects tenant_id into the body — the v3 route requires it there
// in addition to the X-Tenant-ID header.
func (r *Evals) CreateRun(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Evals.CreateRun", spec, "name"); err != nil {
		return nil, err
	}
	body := map[string]interface{}{"tenant_id": r.c.TenantID}
	for k, v := range spec {
		body[k] = v
	}
	return r.c.do(ctx, "agentcontrol.Evals.CreateRun", "POST",
		r.c.acURL(r.path, "runs/"), body)
}

// DeleteRun removes one eval run.
func (r *Evals) DeleteRun(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Evals.DeleteRun: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Evals.DeleteRun", r.c.acURL(r.path, "runs/"+id))
}

// SubmitFeedback records human feedback against a request_id.
func (r *Evals) SubmitFeedback(ctx context.Context, requestID, feedback, comment string) (Result, error) {
	if requestID == "" {
		return nil, errors.New("agentcontrol.Evals.SubmitFeedback: requestID is required")
	}
	if feedback == "" {
		return nil, errors.New("agentcontrol.Evals.SubmitFeedback: feedback is required")
	}
	body := map[string]interface{}{"request_id": requestID, "feedback": feedback}
	if comment != "" {
		body["comment"] = comment
	}
	return r.c.do(ctx, "agentcontrol.Evals.SubmitFeedback", "POST",
		r.c.acURL(r.path, "feedback"), body)
}

// FeedbackStats returns aggregate feedback statistics for the tenant.
func (r *Evals) FeedbackStats(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Evals.FeedbackStats", "GET",
		r.c.acURL(r.path, "stats"), nil)
}

// ── Code (Programming tab) ───────────────────────────────────────────

// Code is the Programming-tab sub-resource — runs editor content and
// persists saved snippets on the tenant node via the Go shim's /code/* routes.
type Code struct {
	c    *Client
	path string
}

// RunOptions describes a Code.Run request.
type RunOptions struct {
	Filename    string            // optional
	Language    string            // "python"|"py"|"bash"|"sh"|"shell" (required)
	Content     string            // source body (required)
	Env         map[string]string // optional env vars
	TimeoutSecs int               // optional, 0 = server default
	Args        []string          // optional argv after the script
}

// Run executes a one-shot code snippet on the tenant node and returns
// stdout / stderr / exit_code.
func (r *Code) Run(ctx context.Context, opts RunOptions) (Result, error) {
	if opts.Language == "" {
		return nil, errors.New("agentcontrol.Code.Run: Language is required")
	}
	if opts.Content == "" {
		return nil, errors.New("agentcontrol.Code.Run: Content is required")
	}
	body := map[string]interface{}{"language": opts.Language, "content": opts.Content}
	if opts.Filename != "" {
		body["filename"] = opts.Filename
	}
	if len(opts.Env) > 0 {
		body["env"] = opts.Env
	}
	if opts.TimeoutSecs > 0 {
		body["timeout_secs"] = opts.TimeoutSecs
	}
	if len(opts.Args) > 0 {
		body["args"] = opts.Args
	}
	return r.c.do(ctx, "agentcontrol.Code.Run", "POST", r.c.acURL(r.path, "run"), body)
}

// Save persists a snippet to the tenant's editor storage.
func (r *Code) Save(ctx context.Context, filename, language, content string) (Result, error) {
	if language == "" {
		return nil, errors.New("agentcontrol.Code.Save: language is required")
	}
	if content == "" {
		return nil, errors.New("agentcontrol.Code.Save: content is required")
	}
	body := map[string]interface{}{"language": language, "content": content}
	if filename != "" {
		body["filename"] = filename
	}
	return r.c.do(ctx, "agentcontrol.Code.Save", "POST", r.c.acURL(r.path, "save"), body)
}

// ListSaved returns metadata for every saved snippet.
func (r *Code) ListSaved(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Code.ListSaved", "GET",
		r.c.acURL(r.path, "saved"), nil)
}

// GetSaved fetches the contents of one saved snippet.
func (r *Code) GetSaved(ctx context.Context, filename string) (Result, error) {
	if filename == "" {
		return nil, errors.New("agentcontrol.Code.GetSaved: filename is required")
	}
	return r.c.do(ctx, "agentcontrol.Code.GetSaved", "GET",
		r.c.acURL(r.path, "saved/"+url.PathEscape(filename)), nil)
}

// DeleteSaved removes one saved snippet.
func (r *Code) DeleteSaved(ctx context.Context, filename string) (Result, error) {
	if filename == "" {
		return nil, errors.New("agentcontrol.Code.DeleteSaved: filename is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Code.DeleteSaved",
		r.c.acURL(r.path, "saved/"+url.PathEscape(filename)))
}

// ── Models (agentcontrol-side, distinct from marketplace catalog) ────

// Models is the agentcontrol Models sub-resource — what the UI's
// "Upload Custom" + "Soft-Delete All" buttons hit.
type Models struct {
	c    *Client
	path string
}

// List returns all models. state, if non-empty, filters server-side
// (UI uses values like "running", "stopped").
func (r *Models) List(ctx context.Context, state string) ([]Result, error) {
	u := r.c.acURL(r.path, "")
	if state != "" {
		u += "?state=" + url.QueryEscape(state)
	}
	body, err := r.c.do(ctx, "agentcontrol.Models.List", "GET", u, nil)
	if err != nil {
		return nil, err
	}
	return asResultSlice(body["items"]), nil
}

// Get returns one model record.
func (r *Models) Get(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Models.Get: id is required")
	}
	return r.c.do(ctx, "agentcontrol.Models.Get", "GET", r.c.acURL(r.path, id), nil)
}

// Create uploads a custom model row. spec must include at least name.
func (r *Models) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Models.Create", spec, "name"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Models.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Delete soft-deletes one model row (audit row stays).
func (r *Models) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Models.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Models.Delete", r.c.acURL(r.path, id))
}

// DeleteAll soft-deletes every model row for the tenant.
func (r *Models) DeleteAll(ctx context.Context) (Result, error) {
	u := trimCollectionSlash(r.c.acURL(r.path, "")) + "?confirm=true"
	return r.c.doDelete(ctx, "agentcontrol.Models.DeleteAll", u)
}

// SetState changes a model's state ("running", "stopped", etc.). PATCH.
func (r *Models) SetState(ctx context.Context, id, state string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Models.SetState: id is required")
	}
	if state == "" {
		return nil, errors.New("agentcontrol.Models.SetState: state is required")
	}
	return r.c.do(ctx, "agentcontrol.Models.SetState", "PATCH",
		r.c.acURL(r.path, id+"/state"), map[string]interface{}{"state": state})
}

// ExportTrainingData exports the model's training-data bundle.
func (r *Models) ExportTrainingData(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Models.ExportTrainingData: id is required")
	}
	return r.c.do(ctx, "agentcontrol.Models.ExportTrainingData", "GET",
		r.c.acURL(r.path, id+"/export"), nil)
}

// ── Deployments ──────────────────────────────────────────────────────

// Deployments is the model-endpoints sub-resource (Models tab > My Deployments).
type Deployments struct {
	c    *Client
	path string
}

// List returns all deployment rows.
func (r *Deployments) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Deployments.List", r.path)
}

// Summary returns deployment-summary metrics.
func (r *Deployments) Summary(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Deployments.Summary", "GET",
		r.c.acURL(r.path, "summary"), nil)
}

// Create starts a deployment. spec must include at least name and model_id.
func (r *Deployments) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Deployments.Create", spec, "name", "model_id"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Deployments.Create", "POST",
		r.c.acURL(r.path, ""), spec)
}

// Sync refreshes a deployment's runtime status from the VM.
func (r *Deployments) Sync(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Deployments.Sync: id is required")
	}
	return r.c.do(ctx, "agentcontrol.Deployments.Sync", "POST",
		r.c.acURL(r.path, id+"/sync"), map[string]interface{}{})
}

// SetStatus patches the deployment's status field.
func (r *Deployments) SetStatus(ctx context.Context, id, status string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Deployments.SetStatus: id is required")
	}
	if status == "" {
		return nil, errors.New("agentcontrol.Deployments.SetStatus: status is required")
	}
	return r.c.do(ctx, "agentcontrol.Deployments.SetStatus", "PATCH",
		r.c.acURL(r.path, id+"/status"), map[string]interface{}{"status": status})
}

// Delete removes one deployment row.
func (r *Deployments) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Deployments.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Deployments.Delete", r.c.acURL(r.path, id))
}

// DeleteAll removes every deployment row for the tenant.
func (r *Deployments) DeleteAll(ctx context.Context) (Result, error) {
	u := trimCollectionSlash(r.c.acURL(r.path, "")) + "?confirm=true"
	return r.c.doDelete(ctx, "agentcontrol.Deployments.DeleteAll", u)
}

// ── WebAssets ────────────────────────────────────────────────────────

// WebAssets is the Web-Assets sub-resource.
type WebAssets struct {
	c    *Client
	path string
}

// List returns all web-asset rows.
func (r *WebAssets) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.WebAssets.List", r.path)
}

// Create adds a web asset. spec must include at least name.
func (r *WebAssets) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.WebAssets.Create", spec, "name"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.WebAssets.Create", "POST",
		r.c.acURL(r.path, ""), spec)
}

// Delete removes a web asset.
func (r *WebAssets) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.WebAssets.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.WebAssets.Delete", r.c.acURL(r.path, id))
}

// ── Benchmarks ───────────────────────────────────────────────────────

// Benchmarks is the benchmarks sub-resource. (Distinct from Evals — Evals
// is the v3 /evals/runs surface; this is the legacy /benchmarks/ surface.)
type Benchmarks struct {
	c    *Client
	path string
}

// List returns all benchmarks for the tenant.
func (r *Benchmarks) List(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Benchmarks.List", "GET",
		r.c.acURL(r.path, ""), nil)
}

// Create posts a new benchmark. spec is opaque to the SDK.
func (r *Benchmarks) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if spec == nil {
		return nil, errors.New("agentcontrol.Benchmarks.Create: spec is required")
	}
	return r.c.do(ctx, "agentcontrol.Benchmarks.Create", "POST",
		r.c.acURL(r.path, ""), spec)
}

// ── Catalog ──────────────────────────────────────────────────────────

// Catalog is the model-catalog sub-resource (Browse Models in the UI).
type Catalog struct {
	c    *Client
	path string
}

// List returns the full catalog.
func (r *Catalog) List(ctx context.Context) ([]Result, error) {
	return r.c.doList(ctx, "agentcontrol.Catalog.List", r.path)
}

// Summary returns the catalog summary metrics.
func (r *Catalog) Summary(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Catalog.Summary", "GET",
		r.c.acURL(r.path, "summary"), nil)
}

// ── Health ───────────────────────────────────────────────────────────

// Health is the model-health sub-resource.
type Health struct {
	c    *Client
	path string
}

// AllModels returns the rollup health status for every model.
func (r *Health) AllModels(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Health.AllModels", "GET",
		r.c.acURL(r.path, "models/status"), nil)
}

// Model returns the health status for one model.
func (r *Health) Model(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Health.Model: id is required")
	}
	return r.c.do(ctx, "agentcontrol.Health.Model", "GET",
		r.c.acURL(r.path, "models/"+id+"/status"), nil)
}

// ── Events (Kafka/Redis/Celery) ──────────────────────────────────────

// Events is the event-bus sub-resource.
type Events struct {
	c    *Client
	path string
}

// Status returns Kafka/Redis/Celery connectivity + active task counts.
func (r *Events) Status(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Events.Status", "GET",
		r.c.acURL(r.path, "status"), nil)
}

// Publish posts an event to a topic. payload is opaque.
func (r *Events) Publish(ctx context.Context, topic, eventType string, payload interface{}) (Result, error) {
	if topic == "" {
		return nil, errors.New("agentcontrol.Events.Publish: topic is required")
	}
	if eventType == "" {
		return nil, errors.New("agentcontrol.Events.Publish: eventType is required")
	}
	body := map[string]interface{}{
		"topic":      topic,
		"event_type": eventType,
		"payload":    payload,
	}
	return r.c.do(ctx, "agentcontrol.Events.Publish", "POST",
		r.c.acURL(r.path, "publish"), body)
}

// ── LLM (in-node agent-execution chat) ───────────────────────────────

// LLM is the in-node LLM-chat sub-resource. Distinct from the standalone
// vxsdk Chat client — this one runs through agentcontrol's agent dispatcher.
type LLM struct {
	c    *Client
	path string
}

// ChatRequest describes one LLM chat call.
type ChatRequest struct {
	Provider  string // required (e.g. "anthropic", "openai", "ollama")
	Model     string // required
	Message   string // required
	AgentType string // optional ("coding", "devops", "git", ...)
	SessionID string // optional — continues a prior session
}

// Chat sends one message and returns reply + session_id.
func (r *LLM) Chat(ctx context.Context, in ChatRequest) (Result, error) {
	if in.Provider == "" {
		return nil, errors.New("agentcontrol.LLM.Chat: Provider is required")
	}
	if in.Model == "" {
		return nil, errors.New("agentcontrol.LLM.Chat: Model is required")
	}
	if in.Message == "" {
		return nil, errors.New("agentcontrol.LLM.Chat: Message is required")
	}
	body := map[string]interface{}{
		"provider": in.Provider, "model": in.Model, "message": in.Message,
	}
	if in.AgentType != "" {
		body["agent_type"] = in.AgentType
	}
	if in.SessionID != "" {
		body["session_id"] = in.SessionID
	}
	return r.c.do(ctx, "agentcontrol.LLM.Chat", "POST",
		r.c.acURL(r.path, "chat"), body)
}

// Providers returns the list of configured LLM providers and their default
// models.
func (r *LLM) Providers(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.LLM.Providers", "GET",
		r.c.acURL(r.path, "providers"), nil)
}

// ── DeployTargets ────────────────────────────────────────────────────

// DeployTargets is the deploy-target provisioning sub-resource.
type DeployTargets struct {
	c    *Client
	path string
}

// List returns all deploy targets registered for the tenant.
func (r *DeployTargets) List(ctx context.Context) ([]Result, error) {
	body, err := r.c.do(ctx, "agentcontrol.DeployTargets.List", "GET",
		r.c.acURL(r.path, ""), nil)
	if err != nil {
		return nil, err
	}
	return asResultSlice(body["items"]), nil
}

// ProvisionInput describes a deploy-target provisioning request.
type ProvisionInput struct {
	CloudProvider string // e.g. "aws", "vxcloud"
	Region        string
	InstanceType  string
	OS            string
	InstanceName  string
}

// Provision starts a new deploy-target VM. Returns a session_id the caller
// uses with ProvisionStatus to poll.
func (r *DeployTargets) Provision(ctx context.Context, in ProvisionInput) (Result, error) {
	body := map[string]interface{}{}
	if in.CloudProvider != "" {
		body["cloud_provider"] = in.CloudProvider
	}
	if in.Region != "" {
		body["region"] = in.Region
	}
	if in.InstanceType != "" {
		body["instance_type"] = in.InstanceType
	}
	if in.OS != "" {
		body["os"] = in.OS
	}
	if in.InstanceName != "" {
		body["instance_name"] = in.InstanceName
	}
	return r.c.do(ctx, "agentcontrol.DeployTargets.Provision", "POST",
		r.c.acURL(r.path, "provision"), body)
}

// ProvisionStatus polls a provisioning session.
func (r *DeployTargets) ProvisionStatus(ctx context.Context, sessionID, username string) (Result, error) {
	if sessionID == "" {
		return nil, errors.New("agentcontrol.DeployTargets.ProvisionStatus: sessionID is required")
	}
	if username == "" {
		return nil, errors.New("agentcontrol.DeployTargets.ProvisionStatus: username is required")
	}
	u := r.c.acURL(r.path, "provision/"+sessionID) + "?username=" + url.QueryEscape(username)
	return r.c.do(ctx, "agentcontrol.DeployTargets.ProvisionStatus", "GET", u, nil)
}

// ── Workflows (shim) ─────────────────────────────────────────────────

// Workflows is the agentcontrol workflow-shim sub-resource. The full
// workflow surface (definitions, executions, etc.) lives on the workflow
// service, not on agentcontrol — this just exposes the two routes the UI
// hits to list and trigger from inside the agentcontrol context.
type Workflows struct {
	c    *Client
	path string
}

// List returns the workflows visible to the tenant via the agentcontrol shim.
func (r *Workflows) List(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Workflows.List", "GET",
		r.c.acURL(r.path, ""), nil)
}

// Trigger fires a workflow by id with optional input.
func (r *Workflows) Trigger(ctx context.Context, workflowID string, input interface{}) (Result, error) {
	if workflowID == "" {
		return nil, errors.New("agentcontrol.Workflows.Trigger: workflowID is required")
	}
	body := map[string]interface{}{"workflow_id": workflowID}
	if input != nil {
		body["input"] = input
	}
	return r.c.do(ctx, "agentcontrol.Workflows.Trigger", "POST",
		r.c.acURL(r.path, "trigger"), body)
}

// ── Infra discovery ──────────────────────────────────────────────────

// Infra is the infrastructure-discovery sub-resource — returns the list of
// reachable agentcontrol endpoints on the tenant node.
type Infra struct {
	c    *Client
	path string
}

// Endpoints returns the discovered endpoints (typically used by the UI's
// auto-config flow).
func (r *Infra) Endpoints(ctx context.Context) (Result, error) {
	return r.c.do(ctx, "agentcontrol.Infra.Endpoints", "GET",
		r.c.acURL(r.path, "endpoints"), nil)
}

// ── tiny helper ──────────────────────────────────────────────────────

// trimCollectionSlash converts ".../<resource>/" → ".../<resource>" so a
// "?confirm=true" suffix joins cleanly. Used by DeleteAll endpoints.
func trimCollectionSlash(u string) string {
	if len(u) > 0 && u[len(u)-1] == '/' {
		return u[:len(u)-1]
	}
	return u
}
