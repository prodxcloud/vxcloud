/**
 * Sessions resource. List / show / apply / pull / delete sessions
 * (the units of work created by deploy / install).
 */

import type { Transport } from './transport.js';

export class Sessions {
  constructor(private t: Transport) {}

  /** List recent sessions for the active tenant. */
  async list(opts?: { limit?: number; status?: 'running' | 'completed' | 'failed' }): Promise<unknown[]> {
    const qs = new URLSearchParams();
    if (opts?.limit) qs.set('limit', String(opts.limit));
    if (opts?.status) qs.set('status', opts.status);
    const path = `/api/v2/tenant/sessions${qs.toString() ? `?${qs}` : ''}`;
    const res = await this.t.get<{ sessions?: unknown[] } | unknown[]>(path);
    if (Array.isArray(res.body)) return res.body;
    return (res.body as { sessions?: unknown[] }).sessions ?? [];
  }

  /** Show details / staged files / entrypoint output for one session. */
  async show(sessionId: string): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/tenant/sessions/${encodeURIComponent(sessionId)}`,
    );
    return res.body ?? {};
  }

  /** Re-run a planned session (typically: promote a --dry-run to apply). */
  async apply(sessionId: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/sessions/apply',
      { session_id: sessionId },
    );
    return res.body ?? {};
  }

  /** Fetch terraform state + artifacts for a session as a JSON payload. */
  async pull(sessionId: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/sessions/pull',
      { session_id: sessionId },
    );
    return res.body ?? {};
  }

  /** Tear down a previously-provisioned resource by session. */
  async delete(sessionId: string, force = false): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/sessions/delete',
      { session_id: sessionId, force },
    );
    return res.body ?? {};
  }
}
