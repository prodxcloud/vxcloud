package salesshift

// Tasks and campaigns.
//
//	GET    /api/v1/salesshift/tasks
//	POST   /api/v1/salesshift/tasks
//	PUT    /api/v1/salesshift/tasks/{id}
//	DELETE /api/v1/salesshift/tasks/{id}
//	GET    /api/v1/salesshift/campaigns
//	GET    /api/v1/salesshift/campaigns/{id}
//	POST   /api/v1/salesshift/campaigns/{id}/send

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// Task carries the four things that make a task actionable: what to do
// (Title/Description), when (DueAt), who (Assignee*), and what "done" means
// (Goal). Progress is 0–100 and is forced to 100 when the task is closed.
type Task struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	TaskType       string `json:"task_type"` // todo | call | email | meeting
	Status         string `json:"status"`    // open | done | cancelled
	Priority       string `json:"priority"`  // low | medium | high
	DueAt          string `json:"due_at"`
	CompletedAt    string `json:"completed_at"`
	Goal           string `json:"goal"`
	Progress       int    `json:"progress"`
	AssigneeID     *int   `json:"assignee_id"`
	AssigneeName   string `json:"assignee_name"`
	AssigneeEmail  string `json:"assignee_email"`
	ContactID      string `json:"contact_id"`
	CompanyID      string `json:"company_id"`
	DealID         string `json:"deal_id"`
	CreatedAt      string `json:"created_at"`
}

// TaskAssignee is a workspace member who can own a task.
type TaskAssignee struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// TaskFilter narrows the list. Q matches title, description or goal.
type TaskFilter struct {
	Status     string
	TaskType   string
	Priority   string
	AssigneeID int
	Q          string
	Limit      int
}

func (f TaskFilter) query() string {
	v := neturl.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("status", f.Status)
	set("task_type", f.TaskType)
	set("priority", f.Priority)
	set("q", f.Q)
	if f.AssigneeID > 0 {
		v.Set("assignee_id", strconv.Itoa(f.AssigneeID))
	}
	if f.Limit > 0 {
		v.Set("limit", strconv.Itoa(f.Limit))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// ListTasks returns tasks plus the workspace roster for the owner picker.
// `total` is the count before the limit, so a caller can tell a truncated
// page from a complete one.
func (c *Client) ListTasks(ctx context.Context, f TaskFilter) (tasks []Task, assignees []TaskAssignee, total int, err error) {
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/tasks") + f.query()
	var raw struct {
		Data      []Task         `json:"data"`
		Assignees []TaskAssignee `json:"assignees"`
		Total     int            `json:"total"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListTasks", "GET", url, nil, &raw); err != nil {
		return nil, nil, 0, fmt.Errorf("salesshift.ListTasks: %w", err)
	}
	return raw.Data, raw.Assignees, raw.Total, nil
}

// NewTask is the create payload. Goal is optional but the board is markedly
// less useful without it — a title says what to do, a goal says what has to
// exist afterwards.
type NewTask struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	TaskType    string `json:"task_type,omitempty"`
	Priority    string `json:"priority,omitempty"`
	DueAt       string `json:"due_at,omitempty"`
	Goal        string `json:"goal,omitempty"`
	Progress    int    `json:"progress,omitempty"`
	AssigneeID  int    `json:"assignee_id,omitempty"`
	ContactID   string `json:"contact_id,omitempty"`
	CompanyID   string `json:"company_id,omitempty"`
	DealID      string `json:"deal_id,omitempty"`
}

// CreateTask adds a task. Defaults to the caller as owner.
func (c *Client) CreateTask(ctx context.Context, in NewTask) (*Task, error) {
	if in.Title == "" {
		return nil, errors.New("salesshift.CreateTask: Title is required")
	}
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/tasks")
	var out Task
	if err := c.T.JSON(ctx, "salesshift.CreateTask", "POST", url, in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.CreateTask: %w", err)
	}
	return &out, nil
}

// TaskPatch is a partial update — only non-nil fields are sent, so a caller
// changing progress cannot accidentally blank the goal.
type TaskPatch struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	TaskType    *string `json:"task_type,omitempty"`
	Status      *string `json:"status,omitempty"`
	Priority    *string `json:"priority,omitempty"`
	DueAt       *string `json:"due_at,omitempty"`
	Goal        *string `json:"goal,omitempty"`
	Progress    *int    `json:"progress,omitempty"`
	AssigneeID  *int    `json:"assignee_id,omitempty"`
}

// UpdateTask applies a partial update. Setting Status to "done" stamps the
// completion time and bumps Progress to 100 server-side.
func (c *Client) UpdateTask(ctx context.Context, id string, patch TaskPatch) (*Task, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateTask: id is required")
	}
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/tasks/"+id)
	var out Task
	if err := c.T.JSON(ctx, "salesshift.UpdateTask", "PUT", url, patch, &out); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateTask: %w", err)
	}
	return &out, nil
}

// DeleteTask removes a task permanently. `status: "cancelled"` is the softer
// statement and is usually what you want.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("salesshift.DeleteTask: id is required")
	}
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/tasks/"+id)
	if err := c.T.JSON(ctx, "salesshift.DeleteTask", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DeleteTask: %w", err)
	}
	return nil
}

/* ── campaigns ─────────────────────────────────────────────────────────── */

// Campaign is a one-to-many blast.
type Campaign struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Status          string `json:"status"` // draft|scheduled|sending|sent|failed
	Subject         string `json:"subject"`
	FromLabel       string `json:"from_label"`
	SendAt          string `json:"send_at"`
	SentAt          string `json:"sent_at"`
	TotalRecipients int    `json:"total_recipients"`
	SentCount       int    `json:"sent_count"`
	FailedCount     int    `json:"failed_count"`
	SuppressedCount int    `json:"suppressed_count"`
	Opened          int    `json:"opened"`
	Clicked         int    `json:"clicked"`
	Replied         int    `json:"replied"`
	Error           string `json:"error"`
	CreatedAt       string `json:"created_at"`
}

// CampaignRecipient is one tracked send. ContactID resolves the person in the
// CRM, which is what turns "someone opened this" into an action.
type CampaignRecipient struct {
	ToEmail     string `json:"to_email"`
	Status      string `json:"status"`
	ContactID   string `json:"contact_id"`
	ContactName string `json:"contact_name"`
	Company     string `json:"company"`
	Opened      bool   `json:"opened"`
	Clicked     bool   `json:"clicked"`
	Replied     bool   `json:"replied"`
	Error       string `json:"error"`
	Subject     string `json:"subject"`
	SentAt      string `json:"sent_at"`
	OpenedAt    string `json:"opened_at"`
	ClickedAt   string `json:"clicked_at"`
	RepliedAt   string `json:"replied_at"`
}

// CampaignTimelinePoint buckets engagement by hour — a blast lands in minutes
// while opens trickle in for days, so a daily bucket hides the shape.
type CampaignTimelinePoint struct {
	At      string `json:"at"`
	Sent    int    `json:"sent"`
	Opened  int    `json:"opened"`
	Clicked int    `json:"clicked"`
	Replied int    `json:"replied"`
}

// ListCampaigns returns the workspace's campaigns.
func (c *Client) ListCampaigns(ctx context.Context) ([]Campaign, error) {
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/campaigns")
	var raw struct {
		Data []Campaign `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListCampaigns", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListCampaigns: %w", err)
	}
	return raw.Data, nil
}

// GetCampaign returns one campaign with every tracked recipient and the
// hourly engagement timeline.
func (c *Client) GetCampaign(ctx context.Context, id string) (*Campaign, []CampaignRecipient, []CampaignTimelinePoint, error) {
	if id == "" {
		return nil, nil, nil, errors.New("salesshift.GetCampaign: id is required")
	}
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/campaigns/"+id)
	var raw struct {
		Data       Campaign                `json:"data"`
		Recipients []CampaignRecipient     `json:"recipients"`
		Timeline   []CampaignTimelinePoint `json:"timeline"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetCampaign", "GET", url, nil, &raw); err != nil {
		return nil, nil, nil, fmt.Errorf("salesshift.GetCampaign: %w", err)
	}
	return &raw.Data, raw.Recipients, raw.Timeline, nil
}

// SendCampaign sends now, or schedules when sendAt (RFC3339) is given.
func (c *Client) SendCampaign(ctx context.Context, id, sendAt string) (*Campaign, error) {
	if id == "" {
		return nil, errors.New("salesshift.SendCampaign: id is required")
	}
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/campaigns/"+id+"/send")
	body := map[string]any{}
	if sendAt != "" {
		body["send_at"] = sendAt
	}
	var raw struct {
		Data Campaign `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.SendCampaign", "POST", url, body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.SendCampaign: %w", err)
	}
	return &raw.Data, nil
}

/* ── leads quota ───────────────────────────────────────────────────────── */

// RevealQuota reports monthly reveal usage. Unlimited is true when the
// workspace has no cap — Allowance is -1 there and Remaining is a large finite
// number, so callers can keep doing integer comparisons.
type RevealQuota struct {
	Used      int    `json:"used"`
	Allowance int    `json:"allowance"`
	Unlimited bool   `json:"unlimited"`
	Remaining int    `json:"remaining"`
	Display   string `json:"display"`
}

// GetRevealQuota returns this month's reveal usage.
func (c *Client) GetRevealQuota(ctx context.Context) (*RevealQuota, error) {
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/leads/quota")
	var raw struct {
		Data RevealQuota `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetRevealQuota", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetRevealQuota: %w", err)
	}
	return &raw.Data, nil
}
