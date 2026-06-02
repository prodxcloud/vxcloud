// Package vxchrono is the resource module for VxChrono — the autonomous
// goal executor and scheduler. Create goals, attach cron/interval
// schedules, launch runs, and drive run lifecycle.
//
// Endpoints (all on the per-tenant node):
//
//	POST   /api/v2/vxchrono/init
//	GET    /api/v2/vxchrono/goals
//	POST   /api/v2/vxchrono/goals
//	GET    /api/v2/vxchrono/goals/{id}
//	PATCH  /api/v2/vxchrono/goals/{id}
//	DELETE /api/v2/vxchrono/goals/{id}
//	POST   /api/v2/vxchrono/goals/{id}/schedule
//	POST   /api/v2/vxchrono/goals/{id}/run
//	GET    /api/v2/vxchrono/runs/{id}
//	POST   /api/v2/vxchrono/runs/{id}/pause|resume|stop
//	POST   /api/v2/vxchrono/scheduler/dispatch
package vxchrono

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// Client is the entry point. Construct via c.VxChrono().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

func (c *Client) do(ctx context.Context, op, method, path string, body interface{}) (Result, error) {
	var out Result
	u := transport.JoinURL(c.NodeURL, path)
	if err := c.T.JSON(ctx, op, method, u, body, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// Init initializes VxChrono for the tenant (idempotent).
func (c *Client) Init(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxchrono.Init", "POST", "/api/v2/vxchrono/init", map[string]interface{}{})
}

// CreateGoal creates a new autonomous goal.
func (c *Client) CreateGoal(ctx context.Context, goal map[string]interface{}) (Result, error) {
	return c.do(ctx, "vxchrono.CreateGoal", "POST", "/api/v2/vxchrono/goals", goal)
}

// ListGoals returns all goals for the tenant.
func (c *Client) ListGoals(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxchrono.ListGoals", "GET", "/api/v2/vxchrono/goals", nil)
}

// GetGoal returns one goal's detail.
func (c *Client) GetGoal(ctx context.Context, goalID string) (Result, error) {
	if goalID == "" {
		return nil, errors.New("vxchrono.GetGoal: goalID is required")
	}
	return c.do(ctx, "vxchrono.GetGoal", "GET", "/api/v2/vxchrono/goals/"+goalID, nil)
}

// UpdateGoal patches a goal.
func (c *Client) UpdateGoal(ctx context.Context, goalID string, patch map[string]interface{}) (Result, error) {
	if goalID == "" {
		return nil, errors.New("vxchrono.UpdateGoal: goalID is required")
	}
	return c.do(ctx, "vxchrono.UpdateGoal", "PATCH", "/api/v2/vxchrono/goals/"+goalID, patch)
}

// DeleteGoal deletes a goal.
func (c *Client) DeleteGoal(ctx context.Context, goalID string) (Result, error) {
	if goalID == "" {
		return nil, errors.New("vxchrono.DeleteGoal: goalID is required")
	}
	return c.do(ctx, "vxchrono.DeleteGoal", "DELETE", "/api/v2/vxchrono/goals/"+goalID, nil)
}

// Schedule attaches a cron/interval schedule to a goal.
func (c *Client) Schedule(ctx context.Context, goalID string, schedule map[string]interface{}) (Result, error) {
	if goalID == "" {
		return nil, errors.New("vxchrono.Schedule: goalID is required")
	}
	return c.do(ctx, "vxchrono.Schedule", "POST",
		"/api/v2/vxchrono/goals/"+goalID+"/schedule", schedule)
}

// LaunchRun launches a run for a goal. payload may be nil.
func (c *Client) LaunchRun(ctx context.Context, goalID string, payload map[string]interface{}) (Result, error) {
	if goalID == "" {
		return nil, errors.New("vxchrono.LaunchRun: goalID is required")
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	return c.do(ctx, "vxchrono.LaunchRun", "POST",
		"/api/v2/vxchrono/goals/"+goalID+"/run", payload)
}

// GetRun returns one run's detail.
func (c *Client) GetRun(ctx context.Context, runID string) (Result, error) {
	if runID == "" {
		return nil, errors.New("vxchrono.GetRun: runID is required")
	}
	return c.do(ctx, "vxchrono.GetRun", "GET", "/api/v2/vxchrono/runs/"+runID, nil)
}

// PauseRun pauses an in-flight run.
func (c *Client) PauseRun(ctx context.Context, runID string) (Result, error) {
	if runID == "" {
		return nil, errors.New("vxchrono.PauseRun: runID is required")
	}
	return c.do(ctx, "vxchrono.PauseRun", "POST",
		"/api/v2/vxchrono/runs/"+runID+"/pause", map[string]interface{}{})
}

// ResumeRun resumes a paused run.
func (c *Client) ResumeRun(ctx context.Context, runID string) (Result, error) {
	if runID == "" {
		return nil, errors.New("vxchrono.ResumeRun: runID is required")
	}
	return c.do(ctx, "vxchrono.ResumeRun", "POST",
		"/api/v2/vxchrono/runs/"+runID+"/resume", map[string]interface{}{})
}

// StopRun stops a run.
func (c *Client) StopRun(ctx context.Context, runID string) (Result, error) {
	if runID == "" {
		return nil, errors.New("vxchrono.StopRun: runID is required")
	}
	return c.do(ctx, "vxchrono.StopRun", "POST",
		"/api/v2/vxchrono/runs/"+runID+"/stop", map[string]interface{}{})
}

// DispatchScheduler ticks the scheduler once — fires any goals whose
// next_run_at has elapsed.
func (c *Client) DispatchScheduler(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxchrono.DispatchScheduler", "POST",
		"/api/v2/vxchrono/scheduler/dispatch", map[string]interface{}{})
}
