/**
 * SalesShift — the sales email service. Tracked sends through the org's
 * BYOK providers (tenant-node Go email worker preferred), suppression
 * gating, daily caps + warmup ramp, open/click tracking, and the
 * SendGrid-style Kafka event stream (salesshift.email.events).
 *
 * Endpoints (vxcloud control plane):
 *   POST /api/v1/salesshift/email/send
 *   GET  /api/v1/salesshift/emails
 *   GET  /api/v1/salesshift/stats
 * Endpoint (tenant node):
 *   GET  /api/v2/salesshift/email/health
 * Lead Verifier (vxcloud control plane):
 *   POST /api/v1/salesshift/verifier/check
 *   GET  /api/v1/salesshift/verifier/status
 */

import type { Transport } from './transport.js';

export interface SendEmailInput {
  toEmail: string;
  subject: string;
  /** HTML body; merge tags like {{first_name}} resolve against the contact. */
  bodyHtml: string;
  firstName?: string;
  lastName?: string;
}

export interface SendEmailResult {
  success: boolean;
  status: string;      // sent | failed
  trackingId: string;
  provider: string;    // node-smtp | smtp | sendgrid | mailgun | platform | sink
  contactId: string;
  error?: string;
}

export interface TrackedEmail {
  id: string;
  toEmail: string;
  subject: string | null;
  status: string;
  provider: string | null;
  openCount: number;
  clickCount: number;
  sentAt: string | null;
}

export interface WorkerHealth {
  status: string;
  service: string;
  providers: string[];
  redisConnected: boolean;
  rateLimitDomain: number;
}

// ── Lead Verifier ─────────────────────────────────────────────────────────

/** `verified` | `risky` | `not_verified` | `inconclusive`. */
export type VerifyVerdict = 'verified' | 'risky' | 'not_verified' | 'inconclusive';
/** Outcome of one pipeline step. */
export type VerifyStepStatus = 'pass' | 'warn' | 'fail' | 'info' | 'skip';

/** Server-enforced verifier limits. A refusal is HTTP 429 with a `detail`. */
export const VERIFIER_PER_USER_PER_MINUTE = 12;
export const VERIFIER_PER_WORKSPACE_PER_HOUR = 150;

export interface VerifyStep {
  /** syntax | domain | mx | provider | hygiene | auth | smtp | catch_all */
  key: string;
  label: string;
  status: VerifyStepStatus;
  summary: string;
  /** Evidence, in order: e.g. `{ k: 'RCPT TO (this address)', v: '250 2.1.5 Recipient OK' }`. */
  facts: { k: string; v: string }[];
  ms: number;
}

export interface VerifyReport {
  email: string;
  normalized: string | null;
  domain: string | null;
  /** null when the pipeline stopped before the MX lookup (e.g. bad syntax). */
  provider: { name: string; kind: string; mx: string } | null;
  freeProvider: boolean;
  role: boolean;
  disposable: boolean;
  /** null when the catch-all canary got no answer either way. */
  catchAll: boolean | null;
  /** DMARC is the published policy (none | quarantine | reject), null when absent. */
  auth: { spf: boolean; dmarc: string | null } | null;
  smtp: { host: string | null; ms: number; via: string | null; egressIp: string | null } | null;
  /** valid | invalid | risky | unknown */
  status: string;
  /** mailbox_accepted | mailbox_not_found | accept_all_domain | blocked_by_policy | ... */
  detail: string;
  verdict: VerifyVerdict;
  /** 0-100 */
  score: number;
  /** A-F */
  grade: string;
  headline: string;
  reasons: string[];
  steps: VerifyStep[];
  durationMs: number;
  checkedAt: string;
}

export interface VerifierCheck {
  /** dns | google | microsoft */
  key: string;
  label: string;
  ok: boolean;
  detail: string;
  ms: number;
  /** The probe IP is on a blocklist at that provider. */
  blocked: boolean;
}

export interface VerifierStatus {
  probeFrom: string;
  egress: boolean;
  /** False means some providers will answer `inconclusive`, not wrong. */
  healthy: boolean;
  checks: VerifierCheck[];
  checkedAt: string;
  limits: { perUserPerMinute: number; perWorkspacePerHour: number };
  /** The server answered from its 60 s cache. */
  cached: boolean;
}

type Json = Record<string, unknown>;
const obj = (v: unknown): Json | null => (v && typeof v === 'object' ? (v as Json) : null);
const strOrNull = (v: unknown): string | null => (v == null ? null : String(v));

function toVerifyReport(r: Json): VerifyReport {
  const provider = obj(r.provider);
  const auth = obj(r.auth);
  const smtp = obj(r.smtp);
  return {
    email: String(r.email ?? ''),
    normalized: strOrNull(r.normalized),
    domain: strOrNull(r.domain),
    provider: provider
      ? { name: String(provider.name ?? ''), kind: String(provider.kind ?? ''), mx: String(provider.mx ?? '') }
      : null,
    freeProvider: Boolean(r.free_provider),
    role: Boolean(r.role),
    disposable: Boolean(r.disposable),
    catchAll: r.catch_all == null ? null : Boolean(r.catch_all),
    auth: auth ? { spf: Boolean(auth.spf), dmarc: strOrNull(auth.dmarc) } : null,
    smtp: smtp
      ? {
          host: strOrNull(smtp.host),
          ms: Number(smtp.ms ?? 0),
          via: strOrNull(smtp.via),
          egressIp: strOrNull(smtp.egress_ip),
        }
      : null,
    status: String(r.status ?? ''),
    detail: String(r.detail ?? ''),
    verdict: String(r.verdict ?? 'inconclusive') as VerifyVerdict,
    score: Number(r.score ?? 0),
    grade: String(r.grade ?? ''),
    headline: String(r.headline ?? ''),
    reasons: Array.isArray(r.reasons) ? r.reasons.map(String) : [],
    steps: (Array.isArray(r.steps) ? r.steps : []).map((x) => {
      const st = obj(x) ?? {};
      return {
        key: String(st.key ?? ''),
        label: String(st.label ?? ''),
        status: String(st.status ?? 'info') as VerifyStepStatus,
        summary: String(st.summary ?? ''),
        facts: (Array.isArray(st.facts) ? st.facts : []).map((f) => {
          const fo = obj(f) ?? {};
          return { k: String(fo.k ?? ''), v: String(fo.v ?? '') };
        }),
        ms: Number(st.ms ?? 0),
      };
    }),
    durationMs: Number(r.duration_ms ?? 0),
    checkedAt: String(r.checked_at ?? ''),
  };
}

/** True for `verified` and `risky`. `inconclusive` is unproven, not good. */
export function isDeliverable(r: Pick<VerifyReport, 'verdict'>): boolean {
  return r.verdict === 'verified' || r.verdict === 'risky';
}

export class SalesShift {
  /**
   * @param t          transport
   * @param ensureNode resolves the tenant node URL on demand. Optional so a
   *   caller can still construct `new SalesShift(transport)` directly (tests,
   *   or driving one resource without the full client); when it is absent the
   *   node-scoped call below falls back to whatever base the transport has.
   */
  constructor(private t: Transport, private ensureNode?: () => Promise<string>) {}

  /** Send one tracked email. Suppressed/unsubscribed recipients are rejected. */
  async sendEmail(input: SendEmailInput): Promise<SendEmailResult> {
    if (!input.toEmail || !input.subject || !input.bodyHtml) {
      throw new Error('salesshift.sendEmail: toEmail, subject and bodyHtml are required');
    }
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/email/send', {
      to_email: input.toEmail,
      subject: input.subject,
      body_html: input.bodyHtml,
      first_name: input.firstName,
      last_name: input.lastName,
    });
    const r = res.body ?? {};
    return {
      success: Boolean(r.success),
      status: String(r.status ?? ''),
      trackingId: String(r.tracking_id ?? ''),
      provider: String(r.provider ?? ''),
      contactId: String(r.contact_id ?? ''),
      error: r.error ? String(r.error) : undefined,
    };
  }

  /** List tracked emails, optionally filtered by status. */
  async listEmails(status?: string): Promise<TrackedEmail[]> {
    const path = `/api/v1/salesshift/emails${status ? `?status=${encodeURIComponent(status)}` : ''}`;
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(path);
    return (res.body?.data ?? []).map((m) => ({
      id: String(m.id ?? ''),
      toEmail: String(m.to_email ?? ''),
      subject: (m.subject as string) ?? null,
      status: String(m.status ?? ''),
      provider: (m.provider as string) ?? null,
      openCount: Number(m.open_count ?? 0),
      clickCount: Number(m.click_count ?? 0),
      sentAt: (m.sent_at as string) ?? null,
    }));
  }

  /** Live dashboard stats for the org. */
  async getStats(): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/stats');
    return res.body ?? {};
  }

  /** Health of the tenant-node Go email worker. */
  async getWorkerHealth(): Promise<WorkerHealth> {
    // The worker runs on the tenant node, not the control plane. Without this
    // the request goes to vxcloudURL (the transport's default base) and 404s,
    // which reads as "worker is down" when it is actually healthy. Python's
    // client.ensure_node_url() does the same thing.
    if (this.ensureNode) await this.ensureNode();
    const res = await this.t.get<Record<string, unknown>>('/api/v2/salesshift/email/health');
    const r = res.body ?? {};
    return {
      status: String(r.status ?? ''),
      service: String(r.service ?? ''),
      providers: (r.providers as string[]) ?? [],
      redisConnected: Boolean(r.redis_connected),
      rateLimitDomain: Number(r.rate_limit_domain ?? 0),
    };
  }

  /**
   * Verify one address with the Lead Verifier: syntax, domain, MX, provider,
   * disposable/role, SPF/DMARC, a live SMTP dialogue that stops at RCPT TO
   * (no message is created) and a catch-all canary. Takes 2-20 s and spends
   * one metered check (12 per user per minute, 150 per workspace per hour;
   * a refusal is a 429 whose `detail` names the full window).
   */
  async verifyEmail(email: string): Promise<VerifyReport> {
    const e = (email ?? '').trim();
    if (!e) throw new Error('salesshift.verifyEmail: email is required');
    const res = await this.t.postJSON<{ data?: Json }>('/api/v1/salesshift/verifier/check', { email: e });
    const data = res.body?.data;
    if (!data) throw new Error('salesshift.verifyEmail: the server answered without a report');
    return toVerifyReport(data);
  }

  /**
   * Can the verification engine give real answers right now? DNS plus
   * port-25 probes to Google and Microsoft from the probing network. Cached
   * 60 s server-side; a fresh run takes up to ~15 s.
   */
  async verifierStatus(): Promise<VerifierStatus> {
    const res = await this.t.get<{ data?: Json; cached?: boolean }>('/api/v1/salesshift/verifier/status');
    const d: Json = res.body?.data ?? {};
    const limits = obj(d.limits) ?? {};
    return {
      probeFrom: String(d.probe_from ?? ''),
      egress: Boolean(d.egress),
      healthy: Boolean(d.healthy),
      checks: (Array.isArray(d.checks) ? d.checks : []).map((x) => {
        const c = obj(x) ?? {};
        return {
          key: String(c.key ?? ''),
          label: String(c.label ?? ''),
          ok: Boolean(c.ok),
          detail: String(c.detail ?? ''),
          ms: Number(c.ms ?? 0),
          blocked: Boolean(c.blocked),
        };
      }),
      checkedAt: String(d.checked_at ?? ''),
      limits: {
        perUserPerMinute: Number(limits.per_user_per_minute ?? VERIFIER_PER_USER_PER_MINUTE),
        perWorkspacePerHour: Number(limits.per_workspace_per_hour ?? VERIFIER_PER_WORKSPACE_PER_HOUR),
      },
      cached: Boolean(res.body?.cached),
    };
  }
}
