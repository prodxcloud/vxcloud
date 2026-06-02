// agentcontrol_verbs.go — verbs missing from the original agentcontrol.go
// sub-resources, plus a handful of agent-related routes (Get, Update, Delete,
// ProxyExecute, RuntimeMetrics) and dataset routes (Download, Delete) that
// don't fit either the "new sub-resource" file or the trimmed original.
//
// Method receivers reuse the existing sub-resource types from agentcontrol.go
// so the public API stays one struct per resource. Routes mirror what the
// UI's agentcontrol_service.ts calls — see [c:/tmp/agentcontrol_ui_actions.md].
package agentcontrol

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// ── FineTuning extras ────────────────────────────────────────────────

// Delete removes one fine-tuning job (row + on-disk artifacts).
func (r *FineTuning) Delete(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.FineTuning.Delete: jobID is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.FineTuning.Delete", r.c.acURL(r.path, jobID))
}

// DeleteAll wipes every fine-tuning job for the tenant.
func (r *FineTuning) DeleteAll(ctx context.Context) (Result, error) {
	u := trimCollectionSlash(r.c.acURL(r.path, "")) + "?confirm=true"
	return r.c.doDelete(ctx, "agentcontrol.FineTuning.DeleteAll", u)
}

// ── Training extras ──────────────────────────────────────────────────

// Update patches a training job's mutable fields (status, accuracy, etc.).
func (r *Training) Update(ctx context.Context, jobID string, patch map[string]interface{}) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Update: jobID is required")
	}
	if patch == nil {
		return nil, errors.New("agentcontrol.Training.Update: patch is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Update", "PUT", r.c.acURL(r.path, jobID), patch)
}

// Delete removes one training job.
func (r *Training) Delete(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Delete: jobID is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Training.Delete", r.c.acURL(r.path, jobID))
}

// DeleteAll removes every training job for the tenant. typeFilter, if
// non-empty, scopes to that type (e.g. "pipeline" to mirror the UI's
// Pipelines-tab "Delete All").
func (r *Training) DeleteAll(ctx context.Context, typeFilter string) (Result, error) {
	u := trimCollectionSlash(r.c.acURL(r.path, "")) + "?confirm=true"
	if typeFilter != "" {
		u += "&type=" + url.QueryEscape(typeFilter)
	}
	return r.c.doDelete(ctx, "agentcontrol.Training.DeleteAll", u)
}

// Clone duplicates a training job for a fresh run.
func (r *Training) Clone(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Clone: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Clone", "POST",
		r.c.acURL(r.path, jobID+"/clone"), map[string]interface{}{})
}

// Restart re-runs a training job from scratch.
func (r *Training) Restart(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Restart: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Restart", "POST",
		r.c.acURL(r.path, jobID+"/restart"), map[string]interface{}{})
}

// RunTests runs the test suite against the training job's produced model.
func (r *Training) RunTests(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.RunTests: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.RunTests", "POST",
		r.c.acURL(r.path, jobID+"/tests"), map[string]interface{}{})
}

// RunQA runs the LLM-graded QA suite against the training job.
func (r *Training) RunQA(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.RunQA: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.RunQA", "POST",
		r.c.acURL(r.path, jobID+"/qa"), map[string]interface{}{})
}

// Export bundles the trained model + manifest for download.
func (r *Training) Export(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Export: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Export", "POST",
		r.c.acURL(r.path, jobID+"/export"), map[string]interface{}{})
}

// Logs returns the most recent log lines for a training job.
func (r *Training) Logs(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Logs: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Logs", "GET",
		r.c.acURL(r.path, jobID+"/logs"), nil)
}

// Metrics returns epoch-by-epoch loss/accuracy for a training job.
func (r *Training) Metrics(ctx context.Context, jobID string) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Metrics: jobID is required")
	}
	return r.c.do(ctx, "agentcontrol.Training.Metrics", "GET",
		r.c.acURL(r.path, jobID+"/metrics"), nil)
}

// ChatRequest is the body of a Training.Chat call.
type TrainingChatRequest struct {
	Message   string // required
	SessionID string // optional
	ModelID   string // optional, picks which trained model to chat with
}

// Chat opens a chat session against the trained model.
func (r *Training) Chat(ctx context.Context, jobID string, in TrainingChatRequest) (Result, error) {
	if jobID == "" {
		return nil, errors.New("agentcontrol.Training.Chat: jobID is required")
	}
	if in.Message == "" {
		return nil, errors.New("agentcontrol.Training.Chat: Message is required")
	}
	body := map[string]interface{}{"message": in.Message}
	if in.SessionID != "" {
		body["session_id"] = in.SessionID
	}
	if in.ModelID != "" {
		body["model_id"] = in.ModelID
	}
	return r.c.do(ctx, "agentcontrol.Training.Chat", "POST",
		r.c.acURL(r.path, jobID+"/chat"), body)
}

// ── Knowledge extras ─────────────────────────────────────────────────

// Delete removes one knowledge base.
func (r *Knowledge) Delete(ctx context.Context, kbID string) (Result, error) {
	if kbID == "" {
		return nil, errors.New("agentcontrol.Knowledge.Delete: kbID is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Knowledge.Delete", r.c.acURL(r.path, kbID))
}

// DeleteAll wipes every knowledge base for the tenant.
func (r *Knowledge) DeleteAll(ctx context.Context) (Result, error) {
	u := trimCollectionSlash(r.c.acURL(r.path, "")) + "?confirm=true"
	return r.c.doDelete(ctx, "agentcontrol.Knowledge.DeleteAll", u)
}

// ── Datasets extras ──────────────────────────────────────────────────

// Download returns the raw dataset bytes. Callers handle the filename
// (typically derived from a Content-Disposition header on the HTTP response —
// for that, use BytesWithHeaders directly).
func (r *Datasets) Download(ctx context.Context, id string) ([]byte, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Datasets.Download: id is required")
	}
	return r.c.doBytes(ctx, "agentcontrol.Datasets.Download",
		r.c.acURL(r.path, id+"/download"))
}

// Delete removes one dataset (row + on-disk file).
func (r *Datasets) Delete(ctx context.Context, id string) (Result, error) {
	if id == "" {
		return nil, errors.New("agentcontrol.Datasets.Delete: id is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Datasets.Delete", r.c.acURL(r.path, id))
}

// Create (metadata-only) adds a dataset row without uploading content.
// Distinct from Upload, which posts the file body via multipart.
func (r *Datasets) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Datasets.Create", spec, "name"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Datasets.Create", "POST",
		r.c.acURL(r.path, ""), spec)
}

// ── Agents (server-side) extras ──────────────────────────────────────

// Get returns one server-side agent.
func (r *Agents) Get(ctx context.Context, agentID string) (Result, error) {
	if agentID == "" {
		return nil, errors.New("agentcontrol.Agents.Get: agentID is required")
	}
	return r.c.do(ctx, "agentcontrol.Agents.Get", "GET", r.c.acURL(r.path, agentID), nil)
}

// Create registers a new server-side agent. spec must include at least name
// and model_id.
func (r *Agents) Create(ctx context.Context, spec map[string]interface{}) (Result, error) {
	if err := requireKeys("agentcontrol.Agents.Create", spec, "name", "model_id"); err != nil {
		return nil, err
	}
	return r.c.do(ctx, "agentcontrol.Agents.Create", "POST", r.c.acURL(r.path, ""), spec)
}

// Update patches one server-side agent (PUT, mirrors UI).
func (r *Agents) Update(ctx context.Context, agentID string, patch map[string]interface{}) (Result, error) {
	if agentID == "" {
		return nil, errors.New("agentcontrol.Agents.Update: agentID is required")
	}
	if patch == nil {
		return nil, errors.New("agentcontrol.Agents.Update: patch is required")
	}
	return r.c.do(ctx, "agentcontrol.Agents.Update", "PUT", r.c.acURL(r.path, agentID), patch)
}

// Delete removes one server-side agent.
func (r *Agents) Delete(ctx context.Context, agentID string) (Result, error) {
	if agentID == "" {
		return nil, errors.New("agentcontrol.Agents.Delete: agentID is required")
	}
	return r.c.doDelete(ctx, "agentcontrol.Agents.Delete", r.c.acURL(r.path, agentID))
}

// ProxyExecuteInput describes a node-mediated execute call against a
// marketplace agent running on the tenant VM. The UI uses this for mixed-
// content reasons (HTTPS browser cannot directly call HTTP endpoints).
type ProxyExecuteInput struct {
	Endpoint    string // required — the agent's HTTP base URL
	Message     string // required
	SessionID   string // optional
	Path        string // optional — append to endpoint (e.g. "/chat")
	PayloadMode string // optional — "auto"|"message"|"prompt"|"query"|"input"|"common"
}

// ProxyExecute relays an execution call through the node so HTTPS callers
// can reach plain-HTTP agent endpoints on the VM. POST /agents/proxy-execute.
func (r *Agents) ProxyExecute(ctx context.Context, in ProxyExecuteInput) (Result, error) {
	if in.Endpoint == "" {
		return nil, errors.New("agentcontrol.Agents.ProxyExecute: Endpoint is required")
	}
	if in.Message == "" {
		return nil, errors.New("agentcontrol.Agents.ProxyExecute: Message is required")
	}
	body := map[string]interface{}{"endpoint": in.Endpoint, "message": in.Message}
	if in.SessionID != "" {
		body["session_id"] = in.SessionID
	}
	if in.Path != "" {
		body["path"] = in.Path
	}
	if in.PayloadMode != "" {
		body["payload_mode"] = in.PayloadMode
	}
	return r.c.do(ctx, "agentcontrol.Agents.ProxyExecute", "POST",
		r.c.acURL(r.path, "proxy-execute"), body)
}

// RuntimeMetrics proxies a metrics-endpoint scrape through the node. endpoint
// is the agent's metrics URL (e.g. "http://1.2.3.4:8000/metrics"). Used by
// the My Agents tab to show requests/errors/latency without CORS issues.
func (c *Client) RuntimeMetrics(ctx context.Context, endpoint string) (Result, error) {
	if endpoint == "" {
		return nil, errors.New("agentcontrol.Client.RuntimeMetrics: endpoint is required")
	}
	u := fmt.Sprintf("%s?endpoint=%s",
		transportJoinURL(c.NodeURL, "/api/v2/agentcontrol/runtime/metrics"),
		url.QueryEscape(endpoint))
	return c.do(ctx, "agentcontrol.Client.RuntimeMetrics", "GET", u, nil)
}

// ── GitHub extras ────────────────────────────────────────────────────

// GitHubContentsInput describes a GitHub repo-contents fetch.
type GitHubContentsInput struct {
	Owner string // required
	Repo  string // required
	Path  string // optional — directory or file path within the repo
	Ref   string // optional — branch/sha; defaults to main on the server
}

// RepoContents returns either one file object (with base64 `content`) or an
// array when the path points to a directory. Used by the Programming tab's
// "Import from GitHub" button.
func (r *GitHub) RepoContents(ctx context.Context, in GitHubContentsInput) (Result, error) {
	if in.Owner == "" {
		return nil, errors.New("agentcontrol.GitHub.RepoContents: Owner is required")
	}
	if in.Repo == "" {
		return nil, errors.New("agentcontrol.GitHub.RepoContents: Repo is required")
	}
	base := r.c.acURL(r.path, "repos/"+url.PathEscape(in.Owner)+"/"+url.PathEscape(in.Repo)+"/contents")
	q := url.Values{}
	if in.Path != "" {
		q.Set("path", in.Path)
	}
	if in.Ref != "" {
		q.Set("ref", in.Ref)
	}
	u := base
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	return r.c.do(ctx, "agentcontrol.GitHub.RepoContents", "GET", u, nil)
}

// ── tiny shim so we don't need to import transport here ──────────────

// transportJoinURL exists so this file doesn't pull the transport package
// just for one constant call. The base URL/path joiner is duplicated to
// keep the dependency graph the same as the original agentcontrol.go.
func transportJoinURL(base, path string) string {
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if len(path) == 0 || path[0] != '/' {
		return base + "/" + path
	}
	return base + path
}
