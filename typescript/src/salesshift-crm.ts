/**
 * SalesShift CRM surfaces — contacts, workflows and sequences.
 *
 *   GET/POST/PUT/DELETE /api/v1/salesshift/contacts
 *   POST                /api/v1/salesshift/contacts/{id}/send-email
 *   GET/POST/PUT/DELETE /api/v1/salesshift/workflows
 *   POST                /api/v1/salesshift/workflows/{id}/test-run|enroll
 *   GET/POST            /api/v1/salesshift/sequences
 *   POST                /api/v1/salesshift/sequences/{id}/enroll
 *
 * The distinction that runs through all three: a *lead* is a masked pool
 * snapshot and cannot be mailed; a *contact* owns its address. Every send
 * path here takes a contact id, never a raw address.
 *
 * Wire format is snake_case; this module speaks camelCase and maps at the
 * boundary, matching every other module in the SDK.
 */

import type { Transport } from './transport.js';

/* ── contacts ────────────────────────────────────────────────────────── */

export interface Contact {
  id: string;
  firstName: string;
  lastName: string;
  /** Display name, falling back to the address for import-only records. */
  name: string;
  email: string;
  phone: string;
  companyId: string | null;
  companyName: string;
  jobTitle: string;
  seniority: string;
  linkedinUrl: string;
  city: string;
  country: string;
  source: string;
  status: string;
  lifecycleStage: string;
  fitScore: number;
  intentScore: number;
  totalScore: number;
  lastContacted: string | null;
  lastActivity: string | null;
  emailsSent: number;
  emailOpens: number;
  emailsReplied: number;
  createdAt: string | null;
}

/** List envelope. `total` counts every match, not the rows in `data` —
 *  conflating the two under-reports the account. */
export interface Pagination {
  total: number;
  page: number;
  limit: number;
  totalPages: number;
}

export interface Activity {
  id: string;
  activityType: string;
  title: string;
  body: string;
  createdAt: string | null;
}

export interface ContactList {
  id: string;
  name: string;
  description: string;
  memberCount: number;
}

export interface SendResult {
  success: boolean;
  status: string;
  trackingId: string;
  provider: string;
  error: string | null;
}

const s = (v: unknown): string => (typeof v === 'string' ? v : '');
const n = (v: unknown): number => (typeof v === 'number' ? v : 0);
const list = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? (v as Record<string, unknown>[]) : [];

function toContact(c: Record<string, unknown>): Contact {
  const first = s(c.first_name);
  const last = s(c.last_name);
  const email = s(c.email);
  return {
    id: s(c.id),
    firstName: first,
    lastName: last,
    name: [first, last].filter(Boolean).join(' ') || email,
    email,
    phone: s(c.phone),
    companyId: (c.company_id as string) ?? null,
    companyName: s(c.company_name),
    jobTitle: s(c.job_title),
    seniority: s(c.seniority),
    linkedinUrl: s(c.linkedin_url),
    city: s(c.city),
    country: s(c.country),
    source: s(c.source),
    status: s(c.status),
    lifecycleStage: s(c.lifecycle_stage),
    fitScore: n(c.fit_score),
    intentScore: n(c.intent_score),
    totalScore: n(c.total_score),
    lastContacted: (c.last_contacted as string) ?? null,
    lastActivity: (c.last_activity as string) ?? null,
    emailsSent: n(c.emails_sent_count),
    emailOpens: n(c.email_opens_count),
    emailsReplied: n(c.emails_replied_count),
    createdAt: (c.created_at as string) ?? null,
  };
}

export class SalesShiftContacts {
  constructor(private t: Transport) {}

  async list(
    filter: {
      search?: string;
      lifecycleStage?: string;
      companyId?: string;
      minScore?: number;
      /** Only contacts with an address — the only ones a send can reach. */
      hasEmail?: boolean;
      page?: number;
      limit?: number;
    } = {},
  ): Promise<{ data: Contact[]; pagination: Pagination }> {
    const p = new URLSearchParams();
    if (filter.search) p.set('search', filter.search);
    if (filter.lifecycleStage) p.set('lifecycle_stage', filter.lifecycleStage);
    if (filter.companyId) p.set('company_id', filter.companyId);
    if (filter.minScore) p.set('min_score', String(filter.minScore));
    if (filter.hasEmail) p.set('email_status', 'has_email');
    if (filter.page) p.set('page', String(filter.page));
    if (filter.limit) p.set('limit', String(filter.limit));
    const qs = p.toString();
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/contacts${qs ? `?${qs}` : ''}`,
    );
    const b = res.body ?? {};
    const pg = (b.pagination as Record<string, unknown>) ?? {};
    return {
      data: list(b.data).map(toContact),
      pagination: {
        total: n(pg.total),
        page: n(pg.page),
        limit: n(pg.limit),
        totalPages: n(pg.total_pages),
      },
    };
  }

  async get(id: string): Promise<Contact> {
    const res = await this.t.get<Record<string, unknown>>(`/api/v1/salesshift/contacts/${id}`);
    return toContact(res.body ?? {});
  }

  /** Email is required: a contact without an address is skipped by every
   *  downstream send, so creating one is almost always a mistake. */
  async create(input: {
    email: string;
    firstName?: string;
    lastName?: string;
    phone?: string;
    companyName?: string;
    jobTitle?: string;
    linkedinUrl?: string;
    city?: string;
    country?: string;
    source?: string;
    lifecycleStage?: string;
  }): Promise<Contact> {
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/contacts', {
      email: input.email,
      first_name: input.firstName,
      last_name: input.lastName,
      phone: input.phone,
      company_name: input.companyName,
      job_title: input.jobTitle,
      linkedin_url: input.linkedinUrl,
      city: input.city,
      country: input.country,
      source: input.source,
      lifecycle_stage: input.lifecycleStage,
    });
    return toContact(res.body ?? {});
  }

  async update(
    id: string,
    patch: {
      firstName?: string;
      lastName?: string;
      email?: string;
      phone?: string;
      companyName?: string;
      jobTitle?: string;
      lifecycleStage?: string;
      status?: string;
    },
  ): Promise<Contact> {
    const body: Record<string, unknown> = {};
    if (patch.firstName !== undefined) body.first_name = patch.firstName;
    if (patch.lastName !== undefined) body.last_name = patch.lastName;
    if (patch.email !== undefined) body.email = patch.email;
    if (patch.phone !== undefined) body.phone = patch.phone;
    if (patch.companyName !== undefined) body.company_name = patch.companyName;
    if (patch.jobTitle !== undefined) body.job_title = patch.jobTitle;
    if (patch.lifecycleStage !== undefined) body.lifecycle_stage = patch.lifecycleStage;
    if (patch.status !== undefined) body.status = patch.status;
    const res = await this.t.putJSON<Record<string, unknown>>(
      `/api/v1/salesshift/contacts/${id}`,
      body,
    );
    return toContact(res.body ?? {});
  }

  async remove(id: string): Promise<void> {
    await this.t.delete(`/api/v1/salesshift/contacts/${id}`);
  }

  /**
   * One-off tracked send: suppression gate, sending pool, open pixel and
   * unsubscribe footer.
   *
   * A refused send is NOT thrown — it returns `success: false` with a reason,
   * because "we declined to mail this person" is a policy outcome the caller
   * should surface, not an error to retry.
   */
  async sendEmail(
    contactId: string,
    input: {
      subject: string;
      bodyHtml: string;
      fromEmail?: string;
      fromName?: string;
      replyTo?: string;
      senderAccountId?: string;
      integrationId?: string;
    },
  ): Promise<SendResult> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/contacts/${contactId}/send-email`,
      {
        subject: input.subject,
        body_html: input.bodyHtml,
        from_email: input.fromEmail,
        from_name: input.fromName,
        reply_to: input.replyTo,
        sender_account_id: input.senderAccountId,
        integration_id: input.integrationId,
      },
    );
    const b = res.body ?? {};
    return {
      success: Boolean(b.success),
      status: s(b.status),
      trackingId: s(b.tracking_id),
      provider: s(b.provider),
      error: (b.error as string) ?? null,
    };
  }

  async addNote(contactId: string, content: string): Promise<void> {
    await this.t.postJSON(`/api/v1/salesshift/contacts/${contactId}/notes`, { content });
  }

  async activities(contactId: string): Promise<Activity[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(
      `/api/v1/salesshift/contacts/${contactId}/activities`,
    );
    return list(res.body?.data).map((a) => ({
      id: s(a.id),
      activityType: s(a.activity_type),
      title: s(a.title),
      body: s(a.body),
      createdAt: (a.created_at as string) ?? null,
    }));
  }

  async lists(): Promise<ContactList[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>('/api/v1/salesshift/lists');
    return list(res.body?.data).map((l) => ({
      id: s(l.id),
      name: s(l.name),
      description: s(l.description),
      memberCount: n(l.member_count),
    }));
  }
}

/* ── workflows ───────────────────────────────────────────────────────── */

export interface WorkflowGraph {
  nodes: Record<string, unknown>[];
  edges: Record<string, unknown>[];
}

export interface Workflow {
  id: string;
  name: string;
  description: string;
  status: string;
  triggerType: string;
  triggerConfig: Record<string, unknown>;
  graph: WorkflowGraph;
  stats: Record<string, unknown>;
  runsCount: number;
  createdAt: string | null;
  updatedAt: string | null;
}

export interface GraphIssue {
  nodeId: string;
  code: string;
  message: string;
}

export interface RunStep {
  nodeId: string;
  nodeType: string;
  status: string;
  summary: string;
  error: string;
  output: Record<string, unknown>;
}

export interface WorkflowRun {
  id: string;
  workflowId: string;
  contactId: string;
  contactName: string;
  status: string;
  isSample: boolean;
  startedAt: string | null;
  finishedAt: string | null;
  steps: RunStep[];
}

function toStep(r: Record<string, unknown>): RunStep {
  return {
    nodeId: s(r.node_id),
    nodeType: s(r.node_type),
    status: s(r.status),
    summary: s(r.summary),
    error: s(r.error),
    output: (r.output as Record<string, unknown>) ?? {},
  };
}

function toRun(r: Record<string, unknown>): WorkflowRun {
  return {
    id: s(r.id),
    workflowId: s(r.workflow_id),
    contactId: s(r.contact_id),
    contactName: s(r.contact_name),
    status: s(r.status),
    isSample: Boolean(r.is_sample),
    startedAt: (r.started_at as string) ?? null,
    finishedAt: (r.finished_at as string) ?? null,
    steps: list(r.steps).map(toStep),
  };
}

function toWorkflow(w: Record<string, unknown>): Workflow {
  const g = (w.graph as Record<string, unknown>) ?? {};
  return {
    id: s(w.id),
    name: s(w.name),
    description: s(w.description),
    status: s(w.status),
    triggerType: s(w.trigger_type),
    triggerConfig: (w.trigger_config as Record<string, unknown>) ?? {},
    graph: { nodes: list(g.nodes), edges: list(g.edges) },
    stats: (w.stats as Record<string, unknown>) ?? {},
    runsCount: n(w.runs_count),
    createdAt: (w.created_at as string) ?? null,
    updatedAt: (w.updated_at as string) ?? null,
  };
}

export class SalesShiftWorkflows {
  constructor(private t: Transport) {}

  async list(status?: string): Promise<Workflow[]> {
    const qs = status ? `?status=${encodeURIComponent(status)}` : '';
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(
      `/api/v1/salesshift/workflows${qs}`,
    );
    return list(res.body?.data).map(toWorkflow);
  }

  /** The list returns graphs too, but only the detail call is guaranteed to
   *  have every node's settings hydrated. */
  async get(id: string): Promise<Workflow> {
    const res = await this.t.get<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/workflows/${id}`,
    );
    return toWorkflow(res.body?.data ?? {});
  }

  async create(input: {
    name: string;
    description?: string;
    triggerType?: string;
    triggerConfig?: Record<string, unknown>;
    graph?: WorkflowGraph;
  }): Promise<Workflow> {
    const res = await this.t.postJSON<{ data?: Record<string, unknown> }>(
      '/api/v1/salesshift/workflows',
      {
        name: input.name,
        description: input.description,
        trigger_type: input.triggerType,
        trigger_config: input.triggerConfig,
        graph: input.graph,
      },
    );
    return toWorkflow(res.body?.data ?? {});
  }

  async update(
    id: string,
    patch: { name?: string; description?: string; triggerType?: string; graph?: WorkflowGraph },
  ): Promise<Workflow> {
    const body: Record<string, unknown> = {};
    if (patch.name !== undefined) body.name = patch.name;
    if (patch.description !== undefined) body.description = patch.description;
    if (patch.triggerType !== undefined) body.trigger_type = patch.triggerType;
    if (patch.graph !== undefined) body.graph = patch.graph;
    const res = await this.t.putJSON<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/workflows/${id}`,
      body,
    );
    return toWorkflow(res.body?.data ?? {});
  }

  async remove(id: string): Promise<void> {
    await this.t.delete(`/api/v1/salesshift/workflows/${id}`);
  }

  private async action(id: string, verb: string): Promise<Workflow> {
    const res = await this.t.postJSON<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/workflows/${id}/${verb}`,
      {},
    );
    return toWorkflow(res.body?.data ?? {});
  }

  /** Re-validates the graph first; a 400 here is a lint failure, not a
   *  transport problem. */
  activate(id: string): Promise<Workflow> {
    return this.action(id, 'activate');
  }

  /** Stops new enrollments. Runs already in flight are unaffected. */
  pause(id: string): Promise<Workflow> {
    return this.action(id, 'pause');
  }

  duplicate(id: string): Promise<Workflow> {
    return this.action(id, 'duplicate');
  }

  /** Warnings do not block activation; errors do. */
  async validate(id: string): Promise<{ valid: boolean; errors: GraphIssue[]; warnings: GraphIssue[] }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/workflows/${id}/validate`,
      {},
    );
    const b = res.body ?? {};
    const issue = (i: Record<string, unknown>): GraphIssue => ({
      nodeId: s(i.node_id),
      code: s(i.code),
      message: s(i.message),
    });
    return {
      valid: Boolean(b.valid),
      errors: list(b.errors).map(issue),
      warnings: list(b.warnings).map(issue),
    };
  }

  /**
   * Execute the graph once and return the trace.
   *
   * `dryRun` defaults to true — a test must not mail anyone by accident. With
   * no `contactId` a transient sample contact is used and the run is flagged
   * `isSample`, so run history stays clean.
   */
  async testRun(
    id: string,
    opts: { contactId?: string; dryRun?: boolean } = {},
  ): Promise<WorkflowRun> {
    const body: Record<string, unknown> = { dry_run: opts.dryRun !== false };
    if (opts.contactId) body.contact_id = opts.contactId;
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/workflows/${id}/test-run`,
      body,
    );
    const b = res.body ?? {};
    const raw = (b.run as Record<string, unknown>) ?? (b.data as Record<string, unknown>) ?? {};
    const run = toRun(raw);
    if (run.steps.length === 0) run.steps = list(b.steps).map(toStep);
    return run;
  }

  /** One run per contact. The workflow must be active — enrolling into a
   *  draft is refused, not queued. */
  async enroll(
    id: string,
    contactIds: string[],
  ): Promise<{ enrolled: number; skipped: SkipDetail[]; runIds: string[] }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/workflows/${id}/enroll`,
      { contact_ids: contactIds },
    );
    const b = res.body ?? {};
    // `skipped` is an ARRAY of {contact_id, reason}, not a count. Coercing it
    // with a numeric mapper produced 0 for every call, hiding the one fact
    // worth reporting: which contacts did not enrol, and why.
    return {
      enrolled: n(b.enrolled),
      skipped: list(b.skipped).map((d) => ({
        contactId: s(d.contact_id),
        email: s(d.email),
        reason: s(d.reason),
      })),
      runIds: Array.isArray(b.run_ids) ? b.run_ids.map(String) : [],
    };
  }

  async runs(id: string): Promise<WorkflowRun[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(
      `/api/v1/salesshift/workflows/${id}/runs`,
    );
    return list(res.body?.data).map(toRun);
  }
}

/* ── sequences ───────────────────────────────────────────────────────── */

export interface SequenceStep {
  id: string;
  stepNumber: number;
  stepType: string;
  name: string;
  subject: string;
  bodyHtml: string;
  delayDays: number;
  delayHours: number;
  isActive: boolean;
}

export interface Sequence {
  id: string;
  name: string;
  description: string;
  status: string;
  stopOnReply: boolean;
  sendDays: string[];
  stepsCount: number;
  steps: SequenceStep[];
  enrolled: number;
  sent: number;
  opened: number;
  clicked: number;
  replied: number;
  openRate: number;
  replyRate: number;
}

export interface SequenceTotals {
  sequences: number;
  active: number;
  enrolled: number;
  sent: number;
  opened: number;
  replied: number;
  openRate: number;
  replyRate: number;
}

/** Why one contact was not enrolled. Three of the four reasons (suppressed,
 *  unsubscribed, no address) are permanent; "already enrolled" is not. */
export interface SkipDetail {
  contactId: string;
  email: string;
  reason: string;
}

export interface Enrollment {
  id: string;
  contactId: string;
  contactName: string;
  contactEmail: string;
  status: string;
  currentStep: number;
  nextSendAt: string | null;
  stopReason: string;
}

function toSeqStep(x: Record<string, unknown>): SequenceStep {
  return {
    id: s(x.id),
    stepNumber: n(x.step_number),
    stepType: s(x.step_type),
    name: s(x.name),
    subject: s(x.subject),
    bodyHtml: s(x.body_html),
    delayDays: n(x.delay_days),
    delayHours: n(x.delay_hours),
    isActive: x.is_active !== false,
  };
}

function toSequence(x: Record<string, unknown>): Sequence {
  return {
    id: s(x.id),
    name: s(x.name),
    description: s(x.description),
    status: s(x.status),
    stopOnReply: x.stop_on_reply !== false,
    sendDays: Array.isArray(x.send_days) ? (x.send_days as string[]) : [],
    stepsCount: n(x.steps_count),
    steps: list(x.steps).map(toSeqStep),
    enrolled: n(x.enrolled),
    sent: n(x.sent),
    opened: n(x.opened),
    clicked: n(x.clicked),
    replied: n(x.replied),
    openRate: n(x.open_rate),
    replyRate: n(x.reply_rate),
  };
}

export class SalesShiftSequences {
  constructor(private t: Transport) {}

  async list(
    filter: { q?: string; status?: string; includeArchived?: boolean } = {},
  ): Promise<{ data: Sequence[]; totals: SequenceTotals }> {
    const p = new URLSearchParams();
    if (filter.q) p.set('q', filter.q);
    if (filter.status) p.set('status', filter.status);
    if (filter.includeArchived) p.set('include_archived', 'true');
    const qs = p.toString();
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/sequences${qs ? `?${qs}` : ''}`,
    );
    const b = res.body ?? {};
    const t = (b.totals as Record<string, unknown>) ?? {};
    return {
      data: list(b.data).map(toSequence),
      totals: {
        sequences: n(t.sequences),
        active: n(t.active),
        enrolled: n(t.enrolled),
        sent: n(t.sent),
        opened: n(t.opened),
        replied: n(t.replied),
        openRate: n(t.open_rate),
        replyRate: n(t.reply_rate),
      },
    };
  }

  /** List rows carry `stepsCount` but an empty `steps` — only this call
   *  hydrates the timeline. */
  async get(id: string): Promise<Sequence> {
    const res = await this.t.get<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/sequences/${id}`,
    );
    return toSequence(res.body?.data ?? {});
  }

  async create(input: {
    name: string;
    description?: string;
    stopOnReply?: boolean;
    sendDays?: string[];
    steps?: {
      stepType?: string;
      subject?: string;
      bodyHtml?: string;
      delayDays?: number;
      delayHours?: number;
    }[];
  }): Promise<Sequence> {
    const res = await this.t.postJSON<{ data?: Record<string, unknown> }>(
      '/api/v1/salesshift/sequences',
      {
        name: input.name,
        description: input.description,
        stop_on_reply: input.stopOnReply !== false,
        send_days: input.sendDays,
        steps: (input.steps ?? []).map((st, i) => ({
          step_number: i + 1,
          step_type: st.stepType ?? 'email',
          subject: st.subject,
          body_html: st.bodyHtml,
          delay_days: st.delayDays ?? 0,
          delay_hours: st.delayHours ?? 0,
        })),
      },
    );
    return toSequence(res.body?.data ?? {});
  }

  async addStep(
    sequenceId: string,
    step: { stepType?: string; subject?: string; bodyHtml?: string; delayDays?: number },
  ): Promise<SequenceStep> {
    const res = await this.t.postJSON<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/sequences/${sequenceId}/steps`,
      {
        step_type: step.stepType ?? 'email',
        subject: step.subject,
        body_html: step.bodyHtml,
        delay_days: step.delayDays ?? 0,
      },
    );
    return toSeqStep(res.body?.data ?? {});
  }

  private async action(id: string, verb: string): Promise<Sequence> {
    const res = await this.t.postJSON<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/sequences/${id}/${verb}`,
      {},
    );
    return toSequence(res.body?.data ?? {});
  }

  activate(id: string): Promise<Sequence> {
    return this.action(id, 'activate');
  }

  pause(id: string): Promise<Sequence> {
    return this.action(id, 'pause');
  }

  archive(id: string): Promise<Sequence> {
    return this.action(id, 'archive');
  }

  duplicate(id: string): Promise<Sequence> {
    return this.action(id, 'duplicate');
  }

  async remove(id: string): Promise<void> {
    await this.t.delete(`/api/v1/salesshift/sequences/${id}`);
  }

  /** The suppression list is a hard gate: a suppressed contact is skipped,
   *  never queued. Every skip comes back with its reason. */
  async enroll(
    id: string,
    contactIds: string[],
  ): Promise<{ enrolled: number; skipped: number; skippedDetails: SkipDetail[] }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/sequences/${id}/enroll`,
      { contact_ids: contactIds },
    );
    const b = res.body ?? {};
    return {
      enrolled: n(b.enrolled),
      skipped: n(b.skipped),
      skippedDetails: list(b.skipped_details).map((d) => ({
        contactId: s(d.contact_id),
        email: s(d.email),
        reason: s(d.reason),
      })),
    };
  }

  async enrollments(id: string): Promise<Enrollment[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>(
      `/api/v1/salesshift/sequences/${id}/enrollments`,
    );
    return list(res.body?.data).map((e) => ({
      id: s(e.id),
      contactId: s(e.contact_id),
      contactName: s(e.contact_name),
      contactEmail: s(e.contact_email),
      status: s(e.status),
      currentStep: n(e.current_step),
      nextSendAt: (e.next_send_at as string) ?? null,
      stopReason: s(e.stop_reason),
    }));
  }

  async analytics(id: string): Promise<Record<string, unknown>> {
    const res = await this.t.get<{ data?: Record<string, unknown> }>(
      `/api/v1/salesshift/sequences/${id}/analytics`,
    );
    return res.body?.data ?? {};
  }

  /** Force this tenant's due steps to run now instead of waiting for the
   *  scheduler. The scheduler still owns the normal cadence. */
  async dispatchNow(): Promise<{ due: number; sent: number; failed: number }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/sequences/dispatch-now',
      {},
    );
    const b = res.body ?? {};
    return { due: n(b.due), sent: n(b.sent), failed: n(b.failed) };
  }
}
