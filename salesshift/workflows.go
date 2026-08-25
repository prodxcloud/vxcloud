package salesshift

// Workflows — the /automations canvas as an API.
//
//	GET    /api/v1/salesshift/workflows
//	POST   /api/v1/salesshift/workflows
//	GET    /api/v1/salesshift/workflows/{id}
//	PUT    /api/v1/salesshift/workflows/{id}
//	DELETE /api/v1/salesshift/workflows/{id}
//	POST   /api/v1/salesshift/workflows/{id}/activate|pause|duplicate
//	POST   /api/v1/salesshift/workflows/{id}/validate
//	POST   /api/v1/salesshift/workflows/{id}/test-run
//	POST   /api/v1/salesshift/workflows/{id}/enroll
//	GET    /api/v1/salesshift/workflows/{id}/runs

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"

	"github.com/prodxcloud/vxcloud/transport"
)

// WorkflowNode is one box on the canvas. Data is left as a free map because
// each node type carries its own settings and pinning them to a struct would
// silently drop fields the server added.
type WorkflowNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Position map[string]any `json:"position,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// WorkflowEdge connects two nodes. SourceHandle distinguishes the branches of
// a condition node ("true" / "false").
type WorkflowEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	Target       string `json:"target"`
	SourceHandle string `json:"sourceHandle,omitempty"`
}

// WorkflowGraph is the canvas contents.
type WorkflowGraph struct {
	Nodes []WorkflowNode `json:"nodes"`
	Edges []WorkflowEdge `json:"edges"`
}

// Workflow is one automation.
type Workflow struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Status        string         `json:"status"` // draft | active | paused
	TriggerType   string         `json:"trigger_type"`
	TriggerConfig map[string]any `json:"trigger_config"`
	Graph         WorkflowGraph  `json:"graph"`
	Stats         map[string]any `json:"stats"`
	RunsCount     int            `json:"runs_count"`
	CreatedAt     string         `json:"created_at"`
	UpdatedAt     string         `json:"updated_at"`
}

// ListWorkflows returns every workflow, optionally filtered by status.
func (c *Client) ListWorkflows(ctx context.Context, status string) ([]Workflow, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows")
	if status != "" {
		url += "?" + neturl.Values{"status": {status}}.Encode()
	}
	var raw struct {
		Data []Workflow `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListWorkflows", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListWorkflows: %w", err)
	}
	return raw.Data, nil
}

// GetWorkflow fetches one workflow including its full graph. The list
// endpoint returns the graph too, but only the detail call is guaranteed to
// have every node's settings hydrated.
func (c *Client) GetWorkflow(ctx context.Context, id string) (*Workflow, error) {
	if id == "" {
		return nil, errors.New("salesshift.GetWorkflow: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id)
	var raw struct {
		Data Workflow `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetWorkflow", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetWorkflow: %w", err)
	}
	return &raw.Data, nil
}

// NewWorkflow is the create payload.
type NewWorkflow struct {
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	TriggerType   string         `json:"trigger_type,omitempty"`
	TriggerConfig map[string]any `json:"trigger_config,omitempty"`
	Graph         *WorkflowGraph `json:"graph,omitempty"`
}

// CreateWorkflow adds a workflow. A workflow with an empty graph is created as
// a draft and cannot be activated until it has nodes.
func (c *Client) CreateWorkflow(ctx context.Context, in NewWorkflow) (*Workflow, error) {
	if in.Name == "" {
		return nil, errors.New("salesshift.CreateWorkflow: Name is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows")
	var raw struct {
		Data Workflow `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.CreateWorkflow", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.CreateWorkflow: %w", err)
	}
	return &raw.Data, nil
}

// WorkflowPatch is a partial update. Graph replaces the whole canvas when set.
type WorkflowPatch struct {
	Name          *string        `json:"name,omitempty"`
	Description   *string        `json:"description,omitempty"`
	TriggerType   *string        `json:"trigger_type,omitempty"`
	TriggerConfig map[string]any `json:"trigger_config,omitempty"`
	Graph         *WorkflowGraph `json:"graph,omitempty"`
}

// UpdateWorkflow applies a partial update.
func (c *Client) UpdateWorkflow(ctx context.Context, id string, p WorkflowPatch) (*Workflow, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateWorkflow: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id)
	var raw struct {
		Data Workflow `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.UpdateWorkflow", "PUT", url, p, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateWorkflow: %w", err)
	}
	return &raw.Data, nil
}

// DeleteWorkflow removes a workflow along with its runs and step history.
func (c *Client) DeleteWorkflow(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("salesshift.DeleteWorkflow: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id)
	if err := c.T.JSON(ctx, "salesshift.DeleteWorkflow", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DeleteWorkflow: %w", err)
	}
	return nil
}

func (c *Client) workflowAction(ctx context.Context, op, id, action string) (*Workflow, error) {
	if id == "" {
		return nil, fmt.Errorf("salesshift.%s: id is required", op)
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/"+action)
	var raw struct {
		Data Workflow `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift."+op, "POST", url, map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.%s: %w", op, err)
	}
	return &raw.Data, nil
}

// ActivateWorkflow makes a workflow eligible for enrollments. The server
// re-validates the graph first and refuses if it is broken, so a 400 here is
// a lint failure, not a transport problem.
func (c *Client) ActivateWorkflow(ctx context.Context, id string) (*Workflow, error) {
	return c.workflowAction(ctx, "ActivateWorkflow", id, "activate")
}

// PauseWorkflow stops new enrollments. Runs already in flight are unaffected.
func (c *Client) PauseWorkflow(ctx context.Context, id string) (*Workflow, error) {
	return c.workflowAction(ctx, "PauseWorkflow", id, "pause")
}

// DuplicateWorkflow copies a workflow, graph included, as a new draft.
func (c *Client) DuplicateWorkflow(ctx context.Context, id string) (*Workflow, error) {
	return c.workflowAction(ctx, "DuplicateWorkflow", id, "duplicate")
}

// GraphIssue is one lint result against the canvas.
type GraphIssue struct {
	NodeID  string `json:"node_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidationResult reports whether a graph can run. Warnings do not block
// activation; Errors do.
type ValidationResult struct {
	Valid    bool         `json:"valid"`
	Errors   []GraphIssue `json:"errors"`
	Warnings []GraphIssue `json:"warnings"`
}

// ValidateWorkflow lints the graph without running it.
func (c *Client) ValidateWorkflow(ctx context.Context, id string) (*ValidationResult, error) {
	if id == "" {
		return nil, errors.New("salesshift.ValidateWorkflow: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/validate")
	var out ValidationResult
	if err := c.T.JSON(ctx, "salesshift.ValidateWorkflow", "POST", url, map[string]any{}, &out); err != nil {
		return nil, fmt.Errorf("salesshift.ValidateWorkflow: %w", err)
	}
	return &out, nil
}

// RunStep is one executed node in a run trace.
type RunStep struct {
	NodeID   string         `json:"node_id"`
	NodeType string         `json:"node_type"`
	Status   string         `json:"status"` // completed | skipped | failed
	Summary  string         `json:"summary"`
	Output   map[string]any `json:"output"`
	Error    string         `json:"error"`
}

// WorkflowRun is one execution of a graph against one contact.
type WorkflowRun struct {
	ID          string    `json:"id"`
	WorkflowID  string    `json:"workflow_id"`
	ContactID   string    `json:"contact_id"`
	ContactName string    `json:"contact_name"`
	Status      string    `json:"status"`
	IsSample    bool      `json:"is_sample"`
	StartedAt   string    `json:"started_at"`
	FinishedAt  string    `json:"finished_at"`
	Steps       []RunStep `json:"steps"`
}

// TestRun executes the graph once and returns the full trace.
//
// dryRun true (the default you almost always want) simulates side effects, so
// a test does not actually mail anyone. contactID may be empty, in which case
// a transient sample contact is used and the run is flagged IsSample.
func (c *Client) TestRun(ctx context.Context, id, contactID string, dryRun bool) (*WorkflowRun, error) {
	if id == "" {
		return nil, errors.New("salesshift.TestRun: id is required")
	}
	body := map[string]any{"dry_run": dryRun}
	if contactID != "" {
		body["contact_id"] = contactID
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/test-run")
	var raw struct {
		Run   WorkflowRun `json:"run"`
		Data  WorkflowRun `json:"data"`
		Steps []RunStep   `json:"steps"`
		// is_sample is a SIBLING of `run`, not a field inside it — the run
		// object the route serialises has never carried the flag, so reading
		// it from the run left IsSample false even on a sample run.
		IsSample bool `json:"is_sample"`
	}
	if err := c.T.JSON(ctx, "salesshift.TestRun", "POST", url, body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.TestRun: %w", err)
	}
	run := raw.Run
	if run.ID == "" {
		run = raw.Data
	}
	if len(run.Steps) == 0 {
		run.Steps = raw.Steps
	}
	run.IsSample = raw.IsSample
	return &run, nil
}

// EnrollResult reports how many contacts entered a workflow.
//
// Skipped is a LIST of contact ids, not a count — the server returns the ids
// so the caller can say WHICH contacts did not enrol. Decoding it as an int
// silently produced 0 for every request, including ones that skipped
// everybody.
type EnrollResult struct {
	Enrolled int          `json:"enrolled"`
	Skipped  []SkipReason `json:"skipped"`
	RunIDs   []string     `json:"run_ids"`
}

// SkipReason names one contact that did not enrol and why.
type SkipReason struct {
	ContactID string `json:"contact_id"`
	Reason    string `json:"reason"`
}

// EnrollContacts starts one run per contact. The workflow must be active — an
// enrollment into a draft is refused rather than queued.
func (c *Client) EnrollContacts(ctx context.Context, id string, contactIDs []string) (*EnrollResult, error) {
	if id == "" {
		return nil, errors.New("salesshift.EnrollContacts: id is required")
	}
	if len(contactIDs) == 0 {
		return nil, errors.New("salesshift.EnrollContacts: contactIDs is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/enroll")
	var out EnrollResult
	body := map[string]any{"contact_ids": contactIDs}
	if err := c.T.JSON(ctx, "salesshift.EnrollContacts", "POST", url, body, &out); err != nil {
		return nil, fmt.Errorf("salesshift.EnrollContacts: %w", err)
	}
	return &out, nil
}

// WorkflowRuns returns recent runs for a workflow.
func (c *Client) WorkflowRuns(ctx context.Context, id string) ([]WorkflowRun, error) {
	if id == "" {
		return nil, errors.New("salesshift.WorkflowRuns: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/runs")
	var raw struct {
		Data []WorkflowRun `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.WorkflowRuns", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.WorkflowRuns: %w", err)
	}
	return raw.Data, nil
}
