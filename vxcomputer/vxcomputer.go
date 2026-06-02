// Package vxcomputer is the resource module for VXCOMPUTER — the
// node-local, policy-governed agent runtime. It drives the
// Plan→Act→Reflect agent loop, the risk policy gate, signed approvals,
// and the tamper-evident hash-chained audit ledger.
//
// Endpoints (all on the per-tenant node):
//
//	GET  /api/v2/vxcomputer/info
//	GET  /api/v2/vxcomputer/health
//	GET  /api/v2/vxcomputer/policy/classify?command=…
//	POST /api/v2/vxcomputer/run
//	POST /api/v2/vxcomputer/approval/resolve
//	GET  /api/v2/vxcomputer/audit/verify
package vxcomputer

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response. The control-plane endpoints
// return dynamic shapes, so callers index Result directly — the same
// contract as vxsdk-py's dict return.
type Result = map[string]interface{}

// Client is the entry point. Construct via c.VxComputer().
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

func (c *Client) do(ctx context.Context, op, method, path string, body interface{}) (Result, error) {
	var out Result
	u := transport.JoinURL(c.NodeURL, path)
	if err := c.T.JSON(ctx, op, method, u, body, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// Info returns VXCOMPUTER capabilities and version.
func (c *Client) Info(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxcomputer.Info", "GET", "/api/v2/vxcomputer/info", nil)
}

// Health is a liveness probe for the node agent runtime.
func (c *Client) Health(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxcomputer.Health", "GET", "/api/v2/vxcomputer/health", nil)
}

// Classify risk-classifies a shell command (low|medium|high|hard-blocked)
// WITHOUT running it.
func (c *Client) Classify(ctx context.Context, command string) (Result, error) {
	if command == "" {
		return nil, errors.New("vxcomputer.Classify: command is required")
	}
	q := url.Values{"command": {command}}
	return c.do(ctx, "vxcomputer.Classify", "GET",
		"/api/v2/vxcomputer/policy/classify?"+q.Encode(), nil)
}

// RunInput is the body for Run.
type RunInput struct {
	Objective string `json:"objective"`
	Channel   string `json:"channel,omitempty"`    // chat|cloudshell|studio|desktop
	SessionID string `json:"session_id,omitempty"` // optional continuity
}

// Run drives the Plan→Act→Reflect loop for an objective. The returned
// Result carries the full timeline; status may be "awaiting_approval".
func (c *Client) Run(ctx context.Context, in RunInput) (Result, error) {
	if in.Objective == "" {
		return nil, errors.New("vxcomputer.Run: Objective is required")
	}
	if in.Channel == "" {
		in.Channel = "chat"
	}
	return c.do(ctx, "vxcomputer.Run", "POST", "/api/v2/vxcomputer/run", in)
}

// ApprovalInput is the body for ResolveApproval.
type ApprovalInput struct {
	RunID      string `json:"run_id"`
	StepID     string `json:"step_id,omitempty"`
	Command    string `json:"command"`
	Decision   string `json:"decision,omitempty"` // approve|deny (default approve)
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Approver   string `json:"approver,omitempty"`
}

// ResolveApproval approves or denies a pending medium/high-risk command.
// On approve, the Result carries a signed, single-use, command-bound
// approval token.
func (c *Client) ResolveApproval(ctx context.Context, in ApprovalInput) (Result, error) {
	if in.RunID == "" || in.Command == "" {
		return nil, errors.New("vxcomputer.ResolveApproval: RunID and Command are required")
	}
	if in.Decision == "" {
		in.Decision = "approve"
	}
	if in.TTLSeconds == 0 {
		in.TTLSeconds = 900
	}
	if in.Approver == "" {
		in.Approver = c.AuthedUsername
	}
	return c.do(ctx, "vxcomputer.ResolveApproval", "POST",
		"/api/v2/vxcomputer/approval/resolve", in)
}

// AuditVerify replays the node's tamper-evident hash-chained audit
// ledger and reports any tampering.
func (c *Client) AuditVerify(ctx context.Context) (Result, error) {
	return c.do(ctx, "vxcomputer.AuditVerify", "GET", "/api/v2/vxcomputer/audit/verify", nil)
}
