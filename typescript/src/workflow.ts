/**
 * Workflow — the node-local visual workflow orchestration engine.
 *
 * An n8n-style engine that executes ReactFlow DAGs in parallel waves. A
 * workflow is a node graph (definition); an execution is one run of it.
 *
 * Endpoints (all on the per-tenant node):
 *   GET    /api/v2/workflow/workflows
 *   POST   /api/v2/workflow/workflows
 *   GET    /api/v2/workflow/workflows/{id}
 *   DELETE /api/v2/workflow/workflows/{id}
 *   POST   /api/v2/workflow/save
 *   POST   /api/v2/workflow/publish
 *   POST   /api/v2/workflow/validate
 *   POST   /api/v2/workflow/execute
 *   POST   /api/v2/workflow/test-node
 *   GET    /api/v2/workflow/executions
 *   GET    /api/v2/workflow/executions/{id}
 *   POST   /api/v2/workflow/executions/{id}/cancel
 *   DELETE /api/v2/workflow/executions/{id}
 *   POST   /api/v2/workflow/export/{json|yaml}
 *   GET    /api/v2/workflow/health
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

export class Workflow {
  constructor(private t: Transport) {}

  // ── workflow definitions (CRUD) ──

  /** List all saved workflows. */
  async list(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/workflow/workflows')).body ?? {};
  }

  /** One workflow definition. */
  async get(workflowId: string): Promise<Result> {
    if (!workflowId) throw new Error('workflow.get: workflowId is required');
    return (await this.t.get<Result>(`/api/v2/workflow/workflows/${workflowId}`)).body ?? {};
  }

  /** Create a new workflow from a node-graph definition. */
  async create(definition: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/workflows', definition)).body ?? {};
  }

  /** Delete a workflow definition. */
  async delete(workflowId: string): Promise<Result> {
    if (!workflowId) throw new Error('workflow.delete: workflowId is required');
    return (await this.t.delete<Result>(`/api/v2/workflow/workflows/${workflowId}`)).body ?? {};
  }

  /** Upsert a workflow definition. */
  async save(definition: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/save', definition)).body ?? {};
  }

  /** Publish a workflow. */
  async publish(definition: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/publish', definition)).body ?? {};
  }

  // ── validation / execution ──

  /** Validate a workflow graph without running it. */
  async validate(definition: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/validate', definition)).body ?? {};
  }

  /** Execute a workflow. payload is a full definition or {workflow_id: "…"}. */
  async execute(payload: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/execute', payload)).body ?? {};
  }

  /** Run a single node in isolation. */
  async testNode(payload: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/workflow/test-node', payload)).body ?? {};
  }

  // ── executions ──

  /** List all workflow executions. */
  async listExecutions(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/workflow/executions')).body ?? {};
  }

  /** One execution record. */
  async getExecution(executionId: string): Promise<Result> {
    if (!executionId) throw new Error('workflow.getExecution: executionId is required');
    return (await this.t.get<Result>(`/api/v2/workflow/executions/${executionId}`)).body ?? {};
  }

  /** Cancel a running execution. */
  async cancelExecution(executionId: string): Promise<Result> {
    if (!executionId) throw new Error('workflow.cancelExecution: executionId is required');
    return (await this.t.postJSON<Result>(`/api/v2/workflow/executions/${executionId}/cancel`, {})).body ?? {};
  }

  /** Delete an execution record. */
  async deleteExecution(executionId: string): Promise<Result> {
    if (!executionId) throw new Error('workflow.deleteExecution: executionId is required');
    return (await this.t.delete<Result>(`/api/v2/workflow/executions/${executionId}`)).body ?? {};
  }

  // ── export / health ──

  /** Serialize a workflow as "json" or "yaml". */
  async export(definition: Record<string, unknown>, format: 'json' | 'yaml' = 'json'): Promise<Result> {
    if (format !== 'json' && format !== 'yaml') {
      throw new Error("workflow.export: format must be 'json' or 'yaml'");
    }
    return (await this.t.postJSON<Result>(`/api/v2/workflow/export/${format}`, definition)).body ?? {};
  }

  /** Liveness probe for the workflow service. */
  async health(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/workflow/health')).body ?? {};
  }
}
