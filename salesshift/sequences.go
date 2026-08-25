package salesshift

// Sequences — multi-step outbound with delays, A/B variants and stop rules.
//
//	GET    /api/v1/salesshift/sequences
//	POST   /api/v1/salesshift/sequences
//	GET    /api/v1/salesshift/sequences/{id}
//	PUT    /api/v1/salesshift/sequences/{id}
//	DELETE /api/v1/salesshift/sequences/{id}
//	POST   /api/v1/salesshift/sequences/{id}/activate|pause|archive|duplicate
//	POST   /api/v1/salesshift/sequences/{id}/steps
//	POST   /api/v1/salesshift/sequences/{id}/enroll
//	GET    /api/v1/salesshift/sequences/{id}/enrollments
//	GET    /api/v1/salesshift/sequences/{id}/analytics
//	POST   /api/v1/salesshift/sequences/dispatch-now
//
// A sequence differs from a campaign: a campaign is one blast to a fixed
// audience, a sequence walks each contact through steps over days and stops
// early when they reply.

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// SequenceStep is one touch. DelayDays/DelayHours are measured from the
// previous step, not from enrollment.
type SequenceStep struct {
	ID           string `json:"id"`
	SequenceID   string `json:"sequence_id"`
	StepNumber   int    `json:"step_number"`
	StepType     string `json:"step_type"` // email | task | condition
	Name         string `json:"name"`
	Subject      string `json:"subject"`
	BodyHTML     string `json:"body_html"`
	DelayDays    int    `json:"delay_days"`
	DelayHours   int    `json:"delay_hours"`
	TaskTitle    string `json:"task_title"`
	TaskNotes    string `json:"task_notes"`
	TaskPriority string `json:"task_priority"`
	IsActive     bool   `json:"is_active"`
}

// Sequence is one outbound play with its rollups. The counters are measured
// against ss_email_tracking rather than incremented in place, so they cannot
// drift away from what was actually sent.
type Sequence struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Status      string   `json:"status"` // draft | active | paused | archived
	SendAsReply bool     `json:"send_as_reply"`
	StopOnReply bool     `json:"stop_on_reply"`
	StopOnClick bool     `json:"stop_on_click"`
	SendDays    []string `json:"send_days"`
	SendTZ      string   `json:"send_timezone"`
	DailyCap    *int     `json:"daily_cap"`

	StepsCount int            `json:"steps_count"`
	Steps      []SequenceStep `json:"steps"`
	Enrolled   int            `json:"enrolled"`
	Active     int            `json:"active"`
	Paused     int            `json:"paused"`
	Completed  int            `json:"completed"`
	Sent       int            `json:"sent"`
	Opened     int            `json:"opened"`
	Clicked    int            `json:"clicked"`
	Replied    int            `json:"replied"`
	Bounced    int            `json:"bounced"`
	OpenRate   float64        `json:"open_rate"`
	ReplyRate  float64        `json:"reply_rate"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SequenceTotals is the roll-up across every sequence in the list.
type SequenceTotals struct {
	Sequences int     `json:"sequences"`
	Active    int     `json:"active"`
	Enrolled  int     `json:"enrolled"`
	Sent      int     `json:"sent"`
	Opened    int     `json:"opened"`
	Replied   int     `json:"replied"`
	OpenRate  float64 `json:"open_rate"`
	ReplyRate float64 `json:"reply_rate"`
}

// ListSequences returns sequences and the workspace totals. Archived rows are
// excluded unless includeArchived is set.
func (c *Client) ListSequences(ctx context.Context, q, status string, includeArchived bool) ([]Sequence, SequenceTotals, error) {
	v := neturl.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if status != "" {
		v.Set("status", status)
	}
	if includeArchived {
		v.Set("include_archived", "true")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences")
	if len(v) > 0 {
		url += "?" + v.Encode()
	}
	var raw struct {
		Data   []Sequence     `json:"data"`
		Totals SequenceTotals `json:"totals"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListSequences", "GET", url, nil, &raw); err != nil {
		return nil, SequenceTotals{}, fmt.Errorf("salesshift.ListSequences: %w", err)
	}
	return raw.Data, raw.Totals, nil
}

// GetSequence fetches one sequence with its step timeline. List rows carry
// StepsCount but no Steps — only this call hydrates them.
func (c *Client) GetSequence(ctx context.Context, id string) (*Sequence, error) {
	if id == "" {
		return nil, errors.New("salesshift.GetSequence: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id)
	var raw struct {
		Data Sequence `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetSequence", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetSequence: %w", err)
	}
	return &raw.Data, nil
}

// NewStep is one step in a create payload.
type NewStep struct {
	StepNumber int    `json:"step_number,omitempty"`
	StepType   string `json:"step_type"`
	Name       string `json:"name,omitempty"`
	Subject    string `json:"subject,omitempty"`
	BodyHTML   string `json:"body_html,omitempty"`
	DelayDays  int    `json:"delay_days"`
	DelayHours int    `json:"delay_hours,omitempty"`
	TaskTitle  string `json:"task_title,omitempty"`
	TaskNotes  string `json:"task_notes,omitempty"`
}

// NewSequence is the create payload. Steps may be supplied inline; a sequence
// with none is created as a draft and cannot be activated.
type NewSequence struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	SendAsReply bool      `json:"send_as_reply,omitempty"`
	StopOnReply bool      `json:"stop_on_reply"`
	StopOnClick bool      `json:"stop_on_click,omitempty"`
	SendDays    []string  `json:"send_days,omitempty"`
	SendTZ      string    `json:"send_timezone,omitempty"`
	DailyCap    int       `json:"daily_cap,omitempty"`
	Steps       []NewStep `json:"steps,omitempty"`
}

// CreateSequence adds a sequence, optionally with its steps in one call.
func (c *Client) CreateSequence(ctx context.Context, in NewSequence) (*Sequence, error) {
	if in.Name == "" {
		return nil, errors.New("salesshift.CreateSequence: Name is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences")
	var raw struct {
		Data Sequence `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.CreateSequence", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.CreateSequence: %w", err)
	}
	return &raw.Data, nil
}

// AddStep appends a step to an existing sequence.
func (c *Client) AddStep(ctx context.Context, sequenceID string, step NewStep) (*SequenceStep, error) {
	if sequenceID == "" {
		return nil, errors.New("salesshift.AddStep: sequenceID is required")
	}
	if step.StepType == "" {
		step.StepType = "email"
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+sequenceID+"/steps")
	var raw struct {
		Data SequenceStep `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.AddStep", "POST", url, step, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.AddStep: %w", err)
	}
	return &raw.Data, nil
}

// DeleteSequence removes a sequence.
func (c *Client) DeleteSequence(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("salesshift.DeleteSequence: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id)
	if err := c.T.JSON(ctx, "salesshift.DeleteSequence", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DeleteSequence: %w", err)
	}
	return nil
}

func (c *Client) sequenceAction(ctx context.Context, op, id, action string) (*Sequence, error) {
	if id == "" {
		return nil, fmt.Errorf("salesshift.%s: id is required", op)
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id+"/"+action)
	var raw struct {
		Data Sequence `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift."+op, "POST", url, map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.%s: %w", op, err)
	}
	return &raw.Data, nil
}

// ActivateSequence starts dispatching due steps.
func (c *Client) ActivateSequence(ctx context.Context, id string) (*Sequence, error) {
	return c.sequenceAction(ctx, "ActivateSequence", id, "activate")
}

// PauseSequence holds all sends. Enrollments keep their position.
func (c *Client) PauseSequence(ctx context.Context, id string) (*Sequence, error) {
	return c.sequenceAction(ctx, "PauseSequence", id, "pause")
}

// ArchiveSequence hides a sequence from the default list.
func (c *Client) ArchiveSequence(ctx context.Context, id string) (*Sequence, error) {
	return c.sequenceAction(ctx, "ArchiveSequence", id, "archive")
}

// DuplicateSequence copies a sequence and its steps as a new draft.
func (c *Client) DuplicateSequence(ctx context.Context, id string) (*Sequence, error) {
	return c.sequenceAction(ctx, "DuplicateSequence", id, "duplicate")
}

// SkipDetail explains why one contact was not enrolled. Suppressed,
// unsubscribed, address-less and already-enrolled contacts are all skipped,
// and the reason matters: three of those are permanent, one is not.
//
// The route sends contact_id and reason only -- there is no email field, so
// one was removed here rather than left to decode as "" forever.
type SkipDetail struct {
	ContactID string `json:"contact_id"`
	Reason    string `json:"reason"`
}

// SequenceEnrollResult reports the outcome of an enrollment.
//
// Skipped is a LIST, not a count. Declaring it `int` made encoding/json fail
// with an UnmarshalTypeError on EVERY call -- including fully successful
// enrollments -- which transport turns into "decode response", so the method
// returned (nil, err) and the enrolled contacts were invisible to the caller.
// workflows.go carries a comment about this exact mistake being fixed there;
// this file was missed at the time.
type SequenceEnrollResult struct {
	Enrolled   int          `json:"enrolled"`
	Skipped    []SkipDetail `json:"skipped"`
	Candidates int          `json:"candidates"`
	Capped     bool         `json:"capped"`
	Cap        int          `json:"cap"`

	// Enrolling into a draft or paused sequence is legitimate -- you build the
	// audience first and activate later -- so the route says which it is.
	SequenceStatus string `json:"sequence_status"`
	Dispatching    bool   `json:"dispatching"`

	// Flat breakdown of the skip reasons, as the route actually emits them.
	SkippedExisting     int `json:"skipped_existing"`
	SkippedSuppressed   int `json:"skipped_suppressed"`
	SkippedNoEmail      int `json:"skipped_no_email"`
	SkippedUnsubscribed int `json:"skipped_unsubscribed"`
}

// EnrollInSequence enrolls contacts by id. The suppression list is a hard
// gate: a suppressed contact is skipped, never queued.
func (c *Client) EnrollInSequence(ctx context.Context, id string, contactIDs []string) (*SequenceEnrollResult, error) {
	if id == "" {
		return nil, errors.New("salesshift.EnrollInSequence: id is required")
	}
	if len(contactIDs) == 0 {
		return nil, errors.New("salesshift.EnrollInSequence: contactIDs is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id+"/enroll")
	var out SequenceEnrollResult
	body := map[string]any{"contact_ids": contactIDs}
	if err := c.T.JSON(ctx, "salesshift.EnrollInSequence", "POST", url, body, &out); err != nil {
		return nil, fmt.Errorf("salesshift.EnrollInSequence: %w", err)
	}
	return &out, nil
}

// Enrollment is one contact's position in a sequence.
//
// Field names follow _enrollment_out in sequences_router.py. Three tags here
// named keys the route has never emitted -- next_send_at, stopped_at and
// stop_reason (the last two behind a `StoppedeAt` typo) -- so those values
// decoded as "" on every call and a stopped enrolment looked indistinguishable
// from a running one.
type Enrollment struct {
	ID             string         `json:"id"`
	SequenceID     string         `json:"sequence_id"`
	ContactID      string         `json:"contact_id"`
	ContactName    string         `json:"contact_name"`
	ContactEmail   string         `json:"contact_email"`
	ContactCompany string         `json:"contact_company"`
	Status         string         `json:"status"` // active | paused | completed | stopped
	CurrentStep    int            `json:"current_step"`
	NextActionAt   string         `json:"next_action_at"`
	LastStepAt     string         `json:"last_step_at"`
	PausedAt       string         `json:"paused_at"`
	CompletedAt    string         `json:"completed_at"`
	LastError      string         `json:"last_error"`
	VariantPicks   map[string]any `json:"variant_picks"`
	EnrolledAt     string         `json:"enrolled_at"`
	CreatedAt      string         `json:"created_at"`
}

// EnrollmentFilter narrows the enrollment list. Zero values are omitted, in
// which case the route applies its own defaults (page 1, limit 50).
type EnrollmentFilter struct {
	Status string // active | paused | completed | stopped
	Q      string // matches name, email or company
	// ErrorOnly keeps only enrollments carrying a last_error — the ops view
	// for deferred sends and dead-lettered addresses.
	ErrorOnly bool
	Page      int
	// 1..200. The route DECLARES ge=1,le=200, so FastAPI rejects anything
	// outside that with 422 -- it does not clamp. Passing 500 is an error,
	// not 200 rows. (opportunities.go is the one that really clamps.)
	Limit int
}

func (f EnrollmentFilter) query() string {
	v := neturl.Values{}
	if f.Status != "" {
		v.Set("status", f.Status)
	}
	if f.Q != "" {
		v.Set("q", f.Q)
	}
	if f.ErrorOnly {
		v.Set("error_only", "true")
	}
	if f.Page > 0 {
		v.Set("page", strconv.Itoa(f.Page))
	}
	if f.Limit > 0 {
		v.Set("limit", strconv.Itoa(f.Limit))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// SequenceEnrollments lists who is enrolled and where they are.
//
// The route paginates at 50 rows. This used to send no query string and drop
// the `pagination` envelope, so a 400-enrollment sequence returned exactly 50
// rows with a nil error and no way to reach the rest — read Pagination.Total
// and page through it.
func (c *Client) SequenceEnrollments(ctx context.Context, id string, f EnrollmentFilter) ([]Enrollment, Pagination, error) {
	if id == "" {
		return nil, Pagination{}, errors.New("salesshift.SequenceEnrollments: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id+"/enrollments") + f.query()
	var raw struct {
		Data       []Enrollment `json:"data"`
		Pagination Pagination   `json:"pagination"`
	}
	if err := c.T.JSON(ctx, "salesshift.SequenceEnrollments", "GET", url, nil, &raw); err != nil {
		return nil, Pagination{}, fmt.Errorf("salesshift.SequenceEnrollments: %w", err)
	}
	return raw.Data, raw.Pagination, nil
}

// StepAnalytics is the funnel for one step.
type StepAnalytics struct {
	StepID     string `json:"step_id"`
	StepNumber int    `json:"step_number"`
	// The funnel labels each step with `name` (the step name, falling back to
	// "Step N"). This was tagged `subject`, a key the analytics route has never
	// emitted, so it decoded as "" on every step.
	Name      string  `json:"name"`
	Sent      int     `json:"sent"`
	Opened    int     `json:"opened"`
	Clicked   int     `json:"clicked"`
	Replied   int     `json:"replied"`
	OpenRate  float64 `json:"open_rate"`
	ReplyRate float64 `json:"reply_rate"`
}

// FunnelTotals are the whole-sequence counters. They arrive nested under
// `totals`, NOT beside `enrolled` — decoding them at the top level returned
// zero for every counter on a sequence that had really sent mail.
type FunnelTotals struct {
	Sent         int     `json:"sent"`
	Opened       int     `json:"opened"`
	Clicked      int     `json:"clicked"`
	Replied      int     `json:"replied"`
	Bounced      int     `json:"bounced"`
	Failed       int     `json:"failed"`
	TasksCreated int     `json:"tasks_created"`
	OpenRate     float64 `json:"open_rate"`
	ReplyRate    float64 `json:"reply_rate"`
	ClickRate    float64 `json:"click_rate"`
	BounceRate   float64 `json:"bounce_rate"`
}

// SequenceAnalytics is the whole-sequence funnel plus a per-step breakdown.
type SequenceAnalytics struct {
	SequenceID string          `json:"sequence_id"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Enrolled   int             `json:"enrolled"`
	Totals     FunnelTotals    `json:"totals"`
	Steps      []StepAnalytics `json:"steps"`
}

// Analytics returns the per-step funnel for a sequence.
func (c *Client) Analytics(ctx context.Context, id string) (*SequenceAnalytics, error) {
	if id == "" {
		return nil, errors.New("salesshift.Analytics: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/"+id+"/analytics")
	var raw struct {
		Data SequenceAnalytics `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.Analytics", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.Analytics: %w", err)
	}
	return &raw.Data, nil
}

// DispatchResult reports what one forced dispatch pass did.
//
// The route nests these under `summary` and has never sent a `due` count, so
// decoding them from the top level yielded a zeroed struct on every call --
// a pass that sent fifty emails reported 0 sent, 0 failed.
type DispatchResult struct {
	Processed int `json:"processed"`
	Sent      int `json:"sent"`
	Failed    int `json:"failed"`
	Completed int `json:"completed"`
	Skipped   int `json:"skipped"`
	Tasks     int `json:"tasks"`
	Deferred  int `json:"deferred"`
	Stopped   int `json:"stopped"`
}

// DispatchNow forces this tenant's due steps to run immediately instead of
// waiting for the scheduler. Useful in tests and after a paused sequence is
// resumed; the scheduler still owns the normal cadence.
func (c *Client) DispatchNow(ctx context.Context) (*DispatchResult, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/sequences/dispatch-now")
	var raw struct {
		Summary DispatchResult `json:"summary"`
	}
	if err := c.T.JSON(ctx, "salesshift.DispatchNow", "POST", url, map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.DispatchNow: %w", err)
	}
	return &raw.Summary, nil
}
