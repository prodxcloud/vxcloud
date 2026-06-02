// Package sessions is the resource module for tenant deployment sessions.
package sessions

import (
	"context"
	"time"

	vxerrors "github.com/prodxcloud/vxcloud/errors"
	"github.com/prodxcloud/vxcloud/transport"
)

// Session is a deployment / install session record.
//
// Field names mirror the wire format (snake_case) so the SDK is a faithful
// view of what FastAPI returns. Callers in Go-land receive the exported
// CamelCase identifiers normally.
type Session struct {
	ID         string    `json:"session_id"`
	Status     string    `json:"status"`
	Hostname   string    `json:"hostname,omitempty"`
	Username   string    `json:"username,omitempty"`
	Script     string    `json:"script,omitempty"`
	ResourceID string    `json:"resource_id,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
}

// Client is the resource module. Build it via vxsdk.Client.Sessions().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

// List returns recent deployment sessions for the authenticated tenant.
//
// Maps to GET {NodeURL}/api/v3/sessions/list (the same endpoint vxcli's
// "sessions list" command exercises today).
func (c *Client) List(ctx context.Context) ([]Session, error) {
	url := transport.JoinURL(c.NodeURL, "/api/v3/sessions/list")
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	if err := c.T.JSON(ctx, "sessions.List", "GET", url, nil, &out); err != nil {
		return nil, err
	}
	return out.Sessions, nil
}

// ApplyInput describes a sessions.apply request.
type ApplyInput struct {
	// SessionID is the session to re-apply. Required.
	SessionID string
}

// PullInput describes a sessions.pull request.
type PullInput struct {
	// SessionID is the session whose terraform state + generated artifacts
	// should be fetched. Required.
	SessionID string
}

// DeleteInput describes a sessions.delete request.
type DeleteInput struct {
	// SessionID is the session to tear down. Required.
	SessionID string
	// Force, if true, bypasses interactive confirmation server-side.
	Force bool
}

// Show returns the full detail / staged files / entrypoint output for a
// single session.
//
// Maps to GET {NodeURL}/api/v2/tenant/sessions/{sessionID}.
func (c *Client) Show(ctx context.Context, sessionID string) (map[string]any, error) {
	if sessionID == "" {
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "sessions.Show", Message: "sessionID is required",
		}}
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/sessions/"+sessionID)
	var out map[string]any
	if err := c.T.JSON(ctx, "sessions.Show", "GET", url, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Apply re-runs a previously planned deploy from a saved session (typically:
// promote a --dry-run to apply).
//
// Maps to POST {NodeURL}/api/v2/tenant/sessions/apply.
func (c *Client) Apply(ctx context.Context, input ApplyInput) (map[string]any, error) {
	if input.SessionID == "" {
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "sessions.Apply", Message: "SessionID is required",
		}}
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/sessions/apply")
	body := map[string]any{"session_id": input.SessionID}
	var out map[string]any
	if err := c.T.JSON(ctx, "sessions.Apply", "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Pull fetches terraform state and generated artifacts for a session as a
// JSON payload.
//
// Maps to POST {NodeURL}/api/v2/tenant/sessions/pull.
func (c *Client) Pull(ctx context.Context, input PullInput) (map[string]any, error) {
	if input.SessionID == "" {
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "sessions.Pull", Message: "SessionID is required",
		}}
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/sessions/pull")
	body := map[string]any{"session_id": input.SessionID}
	var out map[string]any
	if err := c.T.JSON(ctx, "sessions.Pull", "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Delete tears down a previously-provisioned resource by session id.
//
// Maps to POST {NodeURL}/api/v2/tenant/sessions/delete. The convenience
// form takes just the session id; for force-delete construct a DeleteInput
// and call DeleteWith.
func (c *Client) Delete(ctx context.Context, sessionID string) error {
	return c.DeleteWith(ctx, DeleteInput{SessionID: sessionID})
}

// DeleteWith is Delete with a full input struct (lets the caller request
// force=true).
func (c *Client) DeleteWith(ctx context.Context, input DeleteInput) error {
	if input.SessionID == "" {
		return &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "sessions.Delete", Message: "SessionID is required",
		}}
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/sessions/delete")
	body := map[string]any{
		"session_id": input.SessionID,
		"force":      input.Force,
	}
	var out map[string]any
	if err := c.T.JSON(ctx, "sessions.Delete", "POST", url, body, &out); err != nil {
		return err
	}
	return nil
}
