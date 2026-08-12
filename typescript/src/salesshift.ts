/**
 * SalesShift — the sales email service. Tracked sends through the org's
 * BYOK providers (tenant-node Go email worker preferred), suppression
 * gating, daily caps + warmup ramp, open/click tracking, and the
 * SendGrid-style Kafka event stream (salesshift.email.events).
 *
 * Endpoints (infinity control plane):
 *   POST /api/v1/salesshift/email/send
 *   GET  /api/v1/salesshift/emails
 *   GET  /api/v1/salesshift/stats
 * Endpoint (tenant node):
 *   GET  /api/v2/salesshift/email/health
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

export class SalesShift {
  constructor(private t: Transport) {}

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
}
