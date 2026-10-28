/**
 * SalesShift sending settings — the email footer (postal address + opt-out
 * text) at workspace, mailbox and integration level, and the non-secret edit
 * of a BYOK integration.
 *
 *   GET/PUT  /api/v1/salesshift/settings/mailing-address
 *   GET      /api/v1/salesshift/settings/email-footer/preview
 *   GET/POST /api/v1/salesshift/settings/mailboxes
 *   PATCH    /api/v1/salesshift/settings/mailboxes/{id}
 *   PATCH    /api/v1/salesshift/settings/integrations/{id}
 *
 * Footer semantics, identical for a mailbox and an integration: `null`
 * inherits the workspace default, `''` for the address means "no address for
 * this sender", and `undefined` leaves a field untouched. In the text,
 * `{unsubscribe}` becomes the opt-out link; the server appends it when a
 * configured text leaves it out, so the link always exists.
 *
 * Wire format is snake_case; this module speaks camelCase and maps at the
 * boundary, matching every other module in the SDK.
 */

import type { Transport } from './transport.js';

/** The server's footer text when nothing is configured. */
export const BUILTIN_FOOTER_TEXT = "Don't want these emails? {unsubscribe}";
/** The server's limit on a footer text. */
export const FOOTER_TEXT_MAX = 200;

export interface EmailFooterSettings {
  mailingAddress: string;
  /** The workspace's own text; '' means the built-in default applies. */
  footerText: string;
  defaultFooterText: string;
  /** Postal address length limit. */
  maxLength: number;
  canEdit: boolean;
}

export type FooterAddressSource = 'mailbox' | 'integration' | 'org' | 'none';
export type FooterTextSource = 'mailbox' | 'integration' | 'org' | 'default';

export interface FooterPreview {
  /** Exactly what send appends, with the unsubscribe href set to '#'. */
  html: string;
  text: string;
  address: string;
  addressSource: FooterAddressSource;
  footerText: string;
  footerTextSource: FooterTextSource;
}

export interface Mailbox {
  id: string;
  fromEmail: string;
  fromName: string | null;
  dailyCap: number;
  sentToday: number;
  warmupEnabled: boolean;
  warmupStage: number;
  healthStatus: string;
  /** null inherits; '' is "no address". */
  footerAddress: string | null;
  /** null inherits. */
  footerText: string | null;
}

export interface FooterFields {
  /** null → inherit; '' → no address; undefined → unchanged. */
  footerAddress?: string | null;
  /** null → inherit; undefined → unchanged. */
  footerText?: string | null;
}

export interface Integration {
  id: string;
  integrationType: string;
  provider: string;
  metadata: Record<string, unknown>;
  isActive: boolean;
  isDefault: boolean;
  lastTestedAt: string | null;
  lastTestStatus: string | null;
  lastTestError: string | null;
  createdAt: string | null;
}

const s = (v: unknown): string => (typeof v === 'string' ? v : '');
const n = (v: unknown): number => (typeof v === 'number' ? v : 0);
const optS = (v: unknown): string | null => (typeof v === 'string' ? v : null);

/** Unwrap a `{data: …}` envelope when the route sends one. */
function unwrap(body: unknown): Record<string, unknown> {
  const b = (body ?? {}) as Record<string, unknown>;
  const d = b.data;
  return d && typeof d === 'object' && !Array.isArray(d) ? (d as Record<string, unknown>) : b;
}

function footerBody(f: FooterFields, body: Record<string, unknown>): void {
  if (f.footerAddress !== undefined) body.footer_address = f.footerAddress;
  if (f.footerText !== undefined) body.footer_text = f.footerText;
}

function toMailbox(m: Record<string, unknown>): Mailbox {
  return {
    id: s(m.id),
    fromEmail: s(m.from_email),
    fromName: optS(m.from_name),
    dailyCap: n(m.daily_cap),
    sentToday: n(m.sent_today),
    warmupEnabled: Boolean(m.warmup_enabled),
    warmupStage: n(m.warmup_stage),
    healthStatus: s(m.health_status),
    footerAddress: optS(m.footer_address),
    footerText: optS(m.footer_text),
  };
}

function toIntegration(i: Record<string, unknown>): Integration {
  return {
    id: s(i.id),
    integrationType: s(i.integration_type),
    provider: s(i.provider),
    metadata: (i.metadata_json as Record<string, unknown>) ?? {},
    isActive: Boolean(i.is_active),
    isDefault: Boolean(i.is_default),
    lastTestedAt: optS(i.last_tested_at),
    lastTestStatus: optS(i.last_test_status),
    lastTestError: optS(i.last_test_error),
    createdAt: optS(i.created_at),
  };
}

export class SalesShiftSettings {
  constructor(private t: Transport) {}

  /** The workspace default footer. */
  async getFooter(): Promise<EmailFooterSettings> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/settings/mailing-address');
    const b = res.body ?? {};
    return {
      mailingAddress: s(b.mailing_address),
      footerText: s(b.footer_text),
      defaultFooterText: s(b.default_footer_text) || BUILTIN_FOOTER_TEXT,
      maxLength: n(b.max_length) || 300,
      canEdit: Boolean(b.can_edit),
    };
  }

  /** Change either or both workspace defaults (admins). '' clears a field. */
  async setFooter(input: { mailingAddress?: string; footerText?: string }): Promise<void> {
    if (input.mailingAddress === undefined && input.footerText === undefined) {
      throw new Error('salesshift.settings.setFooter: pass mailingAddress and/or footerText');
    }
    if (input.footerText !== undefined && input.footerText.trim().length > FOOTER_TEXT_MAX) {
      throw new Error(`salesshift.settings.setFooter: footerText is longer than ${FOOTER_TEXT_MAX}`);
    }
    const body: Record<string, unknown> = {};
    if (input.mailingAddress !== undefined) body.mailing_address = input.mailingAddress;
    if (input.footerText !== undefined) body.footer_text = input.footerText;
    await this.t.putJSON('/api/v1/salesshift/settings/mailing-address', body);
  }

  /** The footer one sender gets right now. Any one selector is enough; none
   *  previews the workspace default. */
  async previewFooter(
    q: { mailboxId?: string; integrationId?: string; fromEmail?: string } = {},
  ): Promise<FooterPreview> {
    const p = new URLSearchParams();
    if (q.mailboxId) p.set('mailbox_id', q.mailboxId);
    if (q.integrationId) p.set('integration_id', q.integrationId);
    if (q.fromEmail) p.set('from_email', q.fromEmail);
    const qs = p.toString();
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/settings/email-footer/preview${qs ? `?${qs}` : ''}`,
    );
    const b = unwrap(res.body);
    return {
      html: s(b.html),
      text: s(b.text),
      address: s(b.address),
      addressSource: (s(b.address_source) || 'none') as FooterAddressSource,
      footerText: s(b.footer_text),
      footerTextSource: (s(b.footer_text_source) || 'default') as FooterTextSource,
    };
  }

  /** Sending identities (ss_mailboxes) with their caps and footers. */
  async listMailboxes(): Promise<Mailbox[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(
      '/api/v1/salesshift/settings/mailboxes',
    );
    return (Array.isArray(res.body?.data) ? res.body!.data : []).map(toMailbox);
  }

  /**
   * Add a sending identity. Its daily cap (server default 50) is enforced on
   * every send from the address — pass `dailyCap` when the row exists mainly
   * to carry a footer for a mailbox the sending pool already caps.
   */
  async createMailbox(
    input: { fromEmail: string; fromName?: string; dailyCap?: number; warmupEnabled?: boolean } & FooterFields,
  ): Promise<{ id: string }> {
    const body: Record<string, unknown> = { from_email: input.fromEmail };
    if (input.fromName !== undefined) body.from_name = input.fromName;
    if (input.dailyCap !== undefined) body.daily_cap = input.dailyCap;
    if (input.warmupEnabled !== undefined) body.warmup_enabled = input.warmupEnabled;
    footerBody(input, body);
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/settings/mailboxes', body);
    return { id: s(res.body?.id) };
  }

  /** Change a mailbox's From name and/or footer. */
  async updateMailbox(id: string, patch: { fromName?: string } & FooterFields): Promise<Mailbox> {
    const body: Record<string, unknown> = {};
    if (patch.fromName !== undefined) body.from_name = patch.fromName;
    footerBody(patch, body);
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v1/salesshift/settings/mailboxes/${id}`,
      body,
    );
    return toMailbox(unwrap(res.body));
  }

  /** Edit an integration's non-secret fields. Metadata only: Vault
   *  credentials are never needed or touched. */
  async updateIntegration(
    id: string,
    patch: { name?: string; fromEmail?: string; fromName?: string; replyTo?: string } & FooterFields,
  ): Promise<Integration> {
    const body: Record<string, unknown> = {};
    if (patch.name !== undefined) body.name = patch.name;
    if (patch.fromEmail !== undefined) body.from_email = patch.fromEmail;
    if (patch.fromName !== undefined) body.from_name = patch.fromName;
    if (patch.replyTo !== undefined) body.reply_to = patch.replyTo;
    footerBody(patch, body);
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v1/salesshift/settings/integrations/${id}`,
      body,
    );
    return toIntegration(unwrap(res.body));
  }
}
