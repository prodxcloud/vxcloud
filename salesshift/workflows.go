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
//	POST   /api/v1/salesshift/workflows/{id}/run-now
//	…/workflows/recipes/*  (Recipes — see below)

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

// ---------------------------------------------------------------------------
// Run now — one set run of an active manual bulk workflow (WP-21/22)
// ---------------------------------------------------------------------------

// RunNow starts one run (no contact) of an active manual workflow with bulk
// nodes — a recipe's "Run again". The server answers 409 already_running
// while a run of the same workflow is live, 409 not_active for a draft, and
// 400 not_manual for a per-contact workflow (use EnrollContacts there).
func (c *Client) RunNow(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", errors.New("salesshift.RunNow: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/"+id+"/run-now")
	var raw struct {
		Data struct {
			RunID string `json:"run_id"`
		} `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.RunNow", "POST", url, map[string]any{}, &raw); err != nil {
		return "", fmt.Errorf("salesshift.RunNow: %w", err)
	}
	return raw.Data.RunID, nil
}

// ---------------------------------------------------------------------------
// Recipes — /api/v1/salesshift/workflows/recipes (plan §5.5.2)
//
//	GET  /workflows/recipes/catalog
//	GET  /workflows/recipes
//	POST /workflows/recipes/preview
//	POST /workflows/recipes                    (draft, or activate:true = launch)
//	GET  /workflows/recipes/{id}
//	PUT  /workflows/recipes/{id}
//	GET  /workflows/recipes/{id}/people
//	POST /workflows/recipes/{id}/pause|resume|cancel-run|request-approval|approve|decline
//
// A recipe is a workflow the SERVER compiles from a form; clients never send
// a graph. A launch re-runs the preview and refuses on any entry in Blocks
// (400 with that code), 409 estimate_changed when the audience grew > 20 %,
// 403 admin_required for members, 422 for countries other than US/CA or a
// Canadian audience without the CASL acknowledgement.
// ---------------------------------------------------------------------------

// RecipeCatalogEntry is one card in the recipe gallery.
type RecipeCatalogEntry struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Category          string   `json:"category"`
	Description       string   `json:"description"`
	Steps             []string `json:"steps"`
	UsesReveals       bool     `json:"uses_reveals"`
	Trigger           string   `json:"trigger"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailable_reason"`
}

// RecipeAudience is who a recipe emails. Source "lead_pool" uses the pool
// filters (Countries required, US/CA; Cities exact), "list" uses ListIDs,
// "contacts" uses the contact filters (cities, countries, tags,
// lifecycle_stage, exclude_lifecycle_stages, last_emailed_before_days).
type RecipeAudience struct {
	Source           string         `json:"source"`
	Filters          map[string]any `json:"filters,omitempty"`
	ListIDs          []string       `json:"list_ids,omitempty"`
	VerifiedOnly     *bool          `json:"verified_only,omitempty"`
	VerifyUnverified bool           `json:"verify_unverified,omitempty"`
	Exclusions       map[string]any `json:"exclusions,omitempty"`
	MaxMembers       int            `json:"max_members"`
}

// RecipeMessage is the email. TemplateID is org-scoped; Override lets Subject
// and BodyHTML replace the template's.
type RecipeMessage struct {
	TemplateID string `json:"template_id,omitempty"`
	Subject    string `json:"subject"`
	BodyHTML   string `json:"body_html"`
	Override   bool   `json:"override"`
}

// RecipeSender picks the sending mailbox or integration (ids only).
type RecipeSender struct {
	SenderIntegrationID string `json:"sender_integration_id,omitempty"`
	SenderAccountID     string `json:"sender_account_id,omitempty"`
	FromName            string `json:"from_name,omitempty"`
	FromEmail           string `json:"from_email,omitempty"`
	ReplyTo             string `json:"reply_to,omitempty"`
}

// RecipeConfirm is what the human confirmed: the policy box, the numbers they
// saw in the preview, and the typed word ("SEND") above 200 recipients.
type RecipeConfirm struct {
	AcceptedPolicy     bool   `json:"accepted_policy"`
	RevealsMax         int    `json:"reveals_max"`
	RecipientsEstimate int    `json:"recipients_estimate"`
	Typed              string `json:"typed,omitempty"`
}

// RecipeInput is the preview / create / update body.
type RecipeInput struct {
	RecipeKey      string         `json:"recipe_key"`
	Name           string         `json:"name,omitempty"`
	Audience       RecipeAudience `json:"audience"`
	Message        RecipeMessage  `json:"message"`
	Sender         RecipeSender   `json:"sender"`
	Timing         map[string]any `json:"timing,omitempty"`
	Limits         map[string]any `json:"limits,omitempty"`
	Options        map[string]any `json:"options,omitempty"`
	LawfulBasisAck bool           `json:"lawful_basis_ack,omitempty"`
	Activate       bool           `json:"activate"`
	RunNow         bool           `json:"run_now"`
	Confirm        *RecipeConfirm `json:"confirm,omitempty"`
}

// RecipePreview is the live count. Blocks lists everything that would refuse
// a launch (lint, no_sender, no_mailing_address, no_reveals, node_outdated,
// lawful_basis_required, recipes_disabled, sending_not_entitled).
type RecipePreview struct {
	Matched        int              `json:"matched"`
	Estimated      bool             `json:"estimated"`
	Sampled        int              `json:"sampled"`
	Excluded       map[string]int   `json:"excluded"`
	WillReceive    int              `json:"will_receive"`
	Sample         []map[string]any `json:"sample"`
	Reveals        map[string]any   `json:"reveals"`
	Sending        map[string]any   `json:"sending"`
	Lint           map[string]any   `json:"lint"`
	Engine         string           `json:"engine"`
	CapabilitiesOK *bool            `json:"capabilities_ok"`
	CanLaunch      bool             `json:"can_launch"`
	Blocks         []string         `json:"blocks"`
}

// RecipeSaveResult is what create / update / approve return.
type RecipeSaveResult struct {
	Workflow Workflow `json:"workflow"`
	RunID    string   `json:"run_id"`
	Status   string   `json:"status"` // draft | running | scheduled | awaiting_approval
}

// Recipe is the detail page: the workflow, its form, the current run's
// stages, the audience counts, the campaign and the tracking results.
type Recipe struct {
	Workflow     Workflow         `json:"workflow"`
	RecipeKey    string           `json:"recipe_key"`
	RecipeConfig map[string]any   `json:"recipe_config"`
	Approval     map[string]any   `json:"approval"`
	CurrentRun   map[string]any   `json:"current_run"`
	Audience     map[string]any   `json:"audience"`
	Campaign     map[string]any   `json:"campaign"`
	Tracking     map[string]int   `json:"tracking"`
	RunsRecent   []map[string]any `json:"runs_recent"`
}

// RecipeListItem is one row of the recipes table.
type RecipeListItem struct {
	Workflow      Workflow       `json:"workflow"`
	RecipeKey     string         `json:"recipe_key"`
	AudienceLabel string         `json:"audience_label"`
	LastRun       map[string]any `json:"last_run"`
	Campaign      map[string]any `json:"campaign"`
}

// RecipePerson is one audience member; Email is masked unless the person is
// already one of the workspace's contacts.
type RecipePerson struct {
	DisplayName  string `json:"display_name"`
	CompanyName  string `json:"company_name"`
	Email        string `json:"email"`
	VerifyStatus string `json:"verify_status"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	SentAt       string `json:"sent_at"`
}

func (c *Client) recipesURL(path string) string {
	return transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/workflows/recipes"+path)
}

// RecipeCatalog lists the recipes this workspace can start.
func (c *Client) RecipeCatalog(ctx context.Context) ([]RecipeCatalogEntry, error) {
	var raw struct {
		Data []RecipeCatalogEntry `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.RecipeCatalog", "GET", c.recipesURL("/catalog"), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.RecipeCatalog: %w", err)
	}
	return raw.Data, nil
}

// ListRecipes returns the workspace's recipes, optionally filtered by status.
func (c *Client) ListRecipes(ctx context.Context, status string) ([]RecipeListItem, error) {
	url := c.recipesURL("")
	if status != "" {
		url += "?" + neturl.Values{"status": {status}}.Encode()
	}
	var raw struct {
		Data []RecipeListItem `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListRecipes", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListRecipes: %w", err)
	}
	return raw.Data, nil
}

// PreviewRecipe counts who would get the email. Read-only; nothing is saved.
func (c *Client) PreviewRecipe(ctx context.Context, in RecipeInput) (*RecipePreview, error) {
	if in.RecipeKey == "" {
		return nil, errors.New("salesshift.PreviewRecipe: RecipeKey is required")
	}
	var raw struct {
		Data RecipePreview `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.PreviewRecipe", "POST", c.recipesURL("/preview"), in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.PreviewRecipe: %w", err)
	}
	return &raw.Data, nil
}

// CreateRecipe saves a draft (Activate false) or launches (Activate true,
// with Confirm filled from a preview the caller showed to a human).
func (c *Client) CreateRecipe(ctx context.Context, in RecipeInput) (*RecipeSaveResult, error) {
	if in.RecipeKey == "" {
		return nil, errors.New("salesshift.CreateRecipe: RecipeKey is required")
	}
	var raw struct {
		Data RecipeSaveResult `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.CreateRecipe", "POST", c.recipesURL(""), in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.CreateRecipe: %w", err)
	}
	return &raw.Data, nil
}

// UpdateRecipe saves a draft or launches an existing one (409 run_live while
// a run is in flight, 409 edited_on_canvas once it was edited on the canvas).
func (c *Client) UpdateRecipe(ctx context.Context, id string, in RecipeInput) (*RecipeSaveResult, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateRecipe: id is required")
	}
	var raw struct {
		Data RecipeSaveResult `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.UpdateRecipe", "PUT", c.recipesURL("/"+id), in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateRecipe: %w", err)
	}
	return &raw.Data, nil
}

// GetRecipe fetches the detail page's data.
func (c *Client) GetRecipe(ctx context.Context, id string) (*Recipe, error) {
	if id == "" {
		return nil, errors.New("salesshift.GetRecipe: id is required")
	}
	var raw struct {
		Data Recipe `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetRecipe", "GET", c.recipesURL("/"+id), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetRecipe: %w", err)
	}
	return &raw.Data, nil
}

// RecipePeople pages through the latest run's audience. state is a member
// state or one of the People tabs (sent, not_sent, excluded); cursor is the
// previous page's NextCursor.
func (c *Client) RecipePeople(ctx context.Context, id, state, q, cursor string, limit int) ([]RecipePerson, string, error) {
	if id == "" {
		return nil, "", errors.New("salesshift.RecipePeople: id is required")
	}
	v := neturl.Values{}
	if state != "" {
		v.Set("state", state)
	}
	if q != "" {
		v.Set("q", q)
	}
	if cursor != "" {
		v.Set("cursor", cursor)
	}
	if limit > 0 {
		v.Set("limit", fmt.Sprint(limit))
	}
	url := c.recipesURL("/" + id + "/people")
	if len(v) > 0 {
		url += "?" + v.Encode()
	}
	var raw struct {
		Data       []RecipePerson `json:"data"`
		NextCursor string         `json:"next_cursor"`
	}
	if err := c.T.JSON(ctx, "salesshift.RecipePeople", "GET", url, nil, &raw); err != nil {
		return nil, "", fmt.Errorf("salesshift.RecipePeople: %w", err)
	}
	return raw.Data, raw.NextCursor, nil
}

func (c *Client) recipeAction(ctx context.Context, op, id, action string, body any) (map[string]any, error) {
	if id == "" {
		return nil, fmt.Errorf("salesshift.%s: id is required", op)
	}
	if body == nil {
		body = map[string]any{}
	}
	var raw struct {
		Data map[string]any `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift."+op, "POST", c.recipesURL("/"+id+"/"+action), body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.%s: %w", op, err)
	}
	return raw.Data, nil
}

// PauseRecipe pauses the recipe: live jobs stop, a scheduled campaign is held,
// a sending one stops before its next recipient.
func (c *Client) PauseRecipe(ctx context.Context, id string) (map[string]any, error) {
	return c.recipeAction(ctx, "PauseRecipe", id, "pause", nil)
}

// ResumeRecipe resumes it; after a bounce stop pass confirm "RESUME".
func (c *Client) ResumeRecipe(ctx context.Context, id, confirm string) (map[string]any, error) {
	body := map[string]any{}
	if confirm != "" {
		body["confirm"] = confirm
	}
	return c.recipeAction(ctx, "ResumeRecipe", id, "resume", body)
}

// CancelRecipeRun stops a run for good; contacts already added are kept.
func (c *Client) CancelRecipeRun(ctx context.Context, id, runID string) (map[string]any, error) {
	return c.recipeAction(ctx, "CancelRecipeRun", id, "cancel-run", map[string]any{"run_id": runID})
}

// RequestRecipeApproval asks the workspace's admins to launch a saved draft.
func (c *Client) RequestRecipeApproval(ctx context.Context, id string, confirm *RecipeConfirm) (map[string]any, error) {
	return c.recipeAction(ctx, "RequestRecipeApproval", id, "request-approval", map[string]any{"confirm": confirm})
}

// ApproveRecipe launches a recipe a member asked for (admins).
func (c *Client) ApproveRecipe(ctx context.Context, id string, confirm RecipeConfirm) (map[string]any, error) {
	return c.recipeAction(ctx, "ApproveRecipe", id, "approve", map[string]any{"confirm": confirm})
}

// DeclineRecipe sends a requested recipe back to draft (admins).
func (c *Client) DeclineRecipe(ctx context.Context, id, reason string) (map[string]any, error) {
	return c.recipeAction(ctx, "DeclineRecipe", id, "decline", map[string]any{"reason": reason})
}
