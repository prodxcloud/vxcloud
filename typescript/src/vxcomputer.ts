/**
 * VxComputer — the node-local, policy-governed agent runtime.
 *
 * Drives the Plan→Act→Reflect agent loop, the risk policy gate, signed
 * approvals, and the tamper-evident hash-chained audit ledger.
 *
 * Endpoints (all on the per-tenant node):
 *   GET  /api/v2/vxcomputer/info
 *   GET  /api/v2/vxcomputer/health
 *   GET  /api/v2/vxcomputer/policy/classify?command=…
 *   POST /api/v2/vxcomputer/run
 *   POST /api/v2/vxcomputer/approval/resolve
 *   GET  /api/v2/vxcomputer/audit/verify
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. Control-plane shapes are dynamic. */
export type Result = Record<string, unknown>;

export interface VxComputerRunInput {
  objective: string;
  /** chat | cloudshell | studio | desktop. Default: chat. */
  channel?: string;
  /** Optional session id for cross-call continuity. */
  sessionId?: string;
}

export interface VxComputerApprovalInput {
  runId: string;
  stepId?: string;
  command: string;
  /** approve | deny. Default: approve. */
  decision?: 'approve' | 'deny';
  ttlSeconds?: number;
  approver?: string;
}

export class VxComputer {
  constructor(private t: Transport) {}

  /** Capabilities and version. */
  async info(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/vxcomputer/info')).body ?? {};
  }

  /** Liveness probe. */
  async health(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/vxcomputer/health')).body ?? {};
  }

  /** Risk-classify a shell command (low|medium|high|hard-blocked) WITHOUT running it. */
  async classify(command: string): Promise<Result> {
    if (!command) throw new Error('vxcomputer.classify: command is required');
    const q = new URLSearchParams({ command });
    return (await this.t.get<Result>(`/api/v2/vxcomputer/policy/classify?${q}`)).body ?? {};
  }

  /** Drive the Plan→Act→Reflect loop. status may be "awaiting_approval". */
  async run(input: VxComputerRunInput): Promise<Result> {
    if (!input.objective) throw new Error('vxcomputer.run: objective is required');
    return (await this.t.postJSON<Result>('/api/v2/vxcomputer/run', {
      objective: input.objective,
      channel: input.channel ?? 'chat',
      session_id: input.sessionId ?? '',
    })).body ?? {};
  }

  /** Approve or deny a pending medium/high-risk command. */
  async resolveApproval(input: VxComputerApprovalInput): Promise<Result> {
    if (!input.runId || !input.command) {
      throw new Error('vxcomputer.resolveApproval: runId and command are required');
    }
    return (await this.t.postJSON<Result>('/api/v2/vxcomputer/approval/resolve', {
      run_id: input.runId,
      step_id: input.stepId ?? '',
      command: input.command,
      decision: input.decision ?? 'approve',
      ttl_seconds: input.ttlSeconds ?? 900,
      approver: input.approver ?? '',
    })).body ?? {};
  }

  /** Replay the tamper-evident hash-chained audit ledger; reports tampering. */
  async auditVerify(): Promise<Result> {
    return (await this.t.get<Result>('/api/v2/vxcomputer/audit/verify')).body ?? {};
  }
}
