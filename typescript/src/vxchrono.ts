/**
 * VxChrono — the autonomous goal executor and scheduler.
 *
 * Create goals, attach cron/interval schedules, launch runs, and drive
 * run lifecycle (pause / resume / stop).
 *
 * Endpoints (all on the per-tenant node):
 *   POST   /api/v2/vxchrono/init
 *   GET    /api/v2/vxchrono/goals
 *   POST   /api/v2/vxchrono/goals
 *   GET    /api/v2/vxchrono/goals/{id}
 *   PATCH  /api/v2/vxchrono/goals/{id}
 *   DELETE /api/v2/vxchrono/goals/{id}
 *   POST   /api/v2/vxchrono/goals/{id}/schedule
 *   POST   /api/v2/vxchrono/goals/{id}/run
 *   GET    /api/v2/vxchrono/runs/{id}
 *   POST   /api/v2/vxchrono/runs/{id}/pause|resume|stop
 *   POST   /api/v2/vxchrono/scheduler/dispatch
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

export class VxChrono {
  constructor(private t: Transport) {}

  /** Initialize VxChrono for the tenant (idempotent). */
  async init(): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/vxchrono/init', {})).body ?? {};
  }

  /** Create a new autonomous goal. */
  async createGoal(goal: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/vxchrono/goals', goal)).body ?? {};
  }

  /** List all goals. */
  async listGoals(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/vxchrono/goals')).body ?? {};
  }

  /** One goal's detail. */
  async getGoal(goalId: string): Promise<Result> {
    if (!goalId) throw new Error('vxchrono.getGoal: goalId is required');
    return (await this.t.get<Result>(`/api/v2/vxchrono/goals/${goalId}`)).body ?? {};
  }

  /** Patch a goal. */
  async updateGoal(goalId: string, patch: Record<string, unknown>): Promise<Result> {
    if (!goalId) throw new Error('vxchrono.updateGoal: goalId is required');
    return (await this.t.patchJSON<Result>(`/api/v2/vxchrono/goals/${goalId}`, patch)).body ?? {};
  }

  /** Delete a goal. */
  async deleteGoal(goalId: string): Promise<Result> {
    if (!goalId) throw new Error('vxchrono.deleteGoal: goalId is required');
    return (await this.t.delete<Result>(`/api/v2/vxchrono/goals/${goalId}`)).body ?? {};
  }

  /** Attach a cron/interval schedule to a goal. */
  async schedule(goalId: string, schedule: Record<string, unknown>): Promise<Result> {
    if (!goalId) throw new Error('vxchrono.schedule: goalId is required');
    return (await this.t.postJSON<Result>(`/api/v2/vxchrono/goals/${goalId}/schedule`, schedule)).body ?? {};
  }

  /** Launch a run for a goal. */
  async launchRun(goalId: string, payload: Record<string, unknown> = {}): Promise<Result> {
    if (!goalId) throw new Error('vxchrono.launchRun: goalId is required');
    return (await this.t.postJSON<Result>(`/api/v2/vxchrono/goals/${goalId}/run`, payload)).body ?? {};
  }

  /** One run's detail. */
  async getRun(runId: string): Promise<Result> {
    if (!runId) throw new Error('vxchrono.getRun: runId is required');
    return (await this.t.get<Result>(`/api/v2/vxchrono/runs/${runId}`)).body ?? {};
  }

  /** Pause an in-flight run. */
  async pauseRun(runId: string): Promise<Result> {
    if (!runId) throw new Error('vxchrono.pauseRun: runId is required');
    return (await this.t.postJSON<Result>(`/api/v2/vxchrono/runs/${runId}/pause`, {})).body ?? {};
  }

  /** Resume a paused run. */
  async resumeRun(runId: string): Promise<Result> {
    if (!runId) throw new Error('vxchrono.resumeRun: runId is required');
    return (await this.t.postJSON<Result>(`/api/v2/vxchrono/runs/${runId}/resume`, {})).body ?? {};
  }

  /** Stop a run. */
  async stopRun(runId: string): Promise<Result> {
    if (!runId) throw new Error('vxchrono.stopRun: runId is required');
    return (await this.t.postJSON<Result>(`/api/v2/vxchrono/runs/${runId}/stop`, {})).body ?? {};
  }

  /** Tick the scheduler once — fires any goals whose next_run_at has elapsed. */
  async dispatchScheduler(): Promise<Result> {
    return (await this.t.postJSON<Result>('/api/v2/vxchrono/scheduler/dispatch', {})).body ?? {};
  }
}
