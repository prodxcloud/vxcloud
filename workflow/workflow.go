// Package workflow is the resource module for the node-local Workflow
// orchestration service — the n8n-style visual workflow engine that
// executes ReactFlow DAGs in parallel waves.
//
// A workflow is a node graph (definition); an execution is one run of it.
//
// Endpoints (all on the per-tenant node):
//
//	GET    /api/v2/workflow/workflows
//	POST   /api/v2/workflow/workflows
//	GET    /api/v2/workflow/workflows/{id}
//	DELETE /api/v2/workflow/workflows/{id}
//	POST   /api/v2/workflow/save
//	POST   /api/v2/workflow/publish
//	POST   /api/v2/workflow/validate
//	POST   /api/v2/workflow/execute
//	POST   /api/v2/workflow/test-node
//	GET    /api/v2/workflow/executions
//	GET    /api/v2/workflow/executions/{id}
//	POST   /api/v2/workflow/executions/{id}/cancel
//	DELETE /api/v2/workflow/executions/{id}
//	POST   /api/v2/workflow/export/{json|yaml}
//	GET    /api/v2/workflow/health
package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// Client is the entry point. Construct via c.Workflow().
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

// ── workflow definitions (CRUD) ──────────────────────────────────────

// List returns all saved workflows.
func (c *Client) List(ctx context.Context) (Result, error) {
	return c.do(ctx, "workflow.List", "GET", "/api/v2/workflow/workflows", nil)
}

// Get returns one workflow definition.
func (c *Client) Get(ctx context.Context, workflowID string) (Result, error) {
	if workflowID == "" {
		return nil, errors.New("workflow.Get: workflowID is required")
	}
	return c.do(ctx, "workflow.Get", "GET", "/api/v2/workflow/workflows/"+workflowID, nil)
}

// Create creates a new workflow from a node-graph definition.
func (c *Client) Create(ctx context.Context, definition map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.Create", "POST", "/api/v2/workflow/workflows", definition)
}

// Delete deletes a workflow definition.
func (c *Client) Delete(ctx context.Context, workflowID string) (Result, error) {
	if workflowID == "" {
		return nil, errors.New("workflow.Delete: workflowID is required")
	}
	return c.do(ctx, "workflow.Delete", "DELETE", "/api/v2/workflow/workflows/"+workflowID, nil)
}

// Save upserts a workflow definition.
func (c *Client) Save(ctx context.Context, definition map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.Save", "POST", "/api/v2/workflow/save", definition)
}

// Publish publishes a workflow.
func (c *Client) Publish(ctx context.Context, definition map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.Publish", "POST", "/api/v2/workflow/publish", definition)
}

// ── validation / execution ───────────────────────────────────────────

// Validate checks a workflow graph without running it.
func (c *Client) Validate(ctx context.Context, definition map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.Validate", "POST", "/api/v2/workflow/validate", definition)
}

// Execute runs a workflow. payload is either a full definition or
// {"workflow_id": "…"} to run a saved workflow.
func (c *Client) Execute(ctx context.Context, payload map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.Execute", "POST", "/api/v2/workflow/execute", payload)
}

// TestNode runs a single node in isolation.
func (c *Client) TestNode(ctx context.Context, payload map[string]interface{}) (Result, error) {
	return c.do(ctx, "workflow.TestNode", "POST", "/api/v2/workflow/test-node", payload)
}

// ── executions ───────────────────────────────────────────────────────

// ListExecutions returns all workflow executions.
func (c *Client) ListExecutions(ctx context.Context) (Result, error) {
	return c.do(ctx, "workflow.ListExecutions", "GET", "/api/v2/workflow/executions", nil)
}

// GetExecution returns one execution record.
func (c *Client) GetExecution(ctx context.Context, executionID string) (Result, error) {
	if executionID == "" {
		return nil, errors.New("workflow.GetExecution: executionID is required")
	}
	return c.do(ctx, "workflow.GetExecution", "GET", "/api/v2/workflow/executions/"+executionID, nil)
}

// CancelExecution cancels a running execution.
func (c *Client) CancelExecution(ctx context.Context, executionID string) (Result, error) {
	if executionID == "" {
		return nil, errors.New("workflow.CancelExecution: executionID is required")
	}
	return c.do(ctx, "workflow.CancelExecution", "POST",
		"/api/v2/workflow/executions/"+executionID+"/cancel", map[string]interface{}{})
}

// DeleteExecution deletes an execution record.
func (c *Client) DeleteExecution(ctx context.Context, executionID string) (Result, error) {
	if executionID == "" {
		return nil, errors.New("workflow.DeleteExecution: executionID is required")
	}
	return c.do(ctx, "workflow.DeleteExecution", "DELETE",
		"/api/v2/workflow/executions/"+executionID, nil)
}

// ── export / health ──────────────────────────────────────────────────

// Export serializes a workflow as "json" or "yaml".
func (c *Client) Export(ctx context.Context, definition map[string]interface{}, format string) (Result, error) {
	if format != "json" && format != "yaml" {
		return nil, errors.New("workflow.Export: format must be 'json' or 'yaml'")
	}
	return c.do(ctx, "workflow.Export", "POST", "/api/v2/workflow/export/"+format, definition)
}

// Health is a liveness probe for the workflow service.
func (c *Client) Health(ctx context.Context) (Result, error) {
	return c.do(ctx, "workflow.Health", "GET", "/api/v2/workflow/health", nil)
}
