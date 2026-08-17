/**
 * SalesShift platform surfaces — billing, social distribution, the signal
 * pool, tasks and campaigns.
 *
 * Split from `salesshift.ts` (which is the email service) because these are a
 * different concern with a different lifecycle: that module sends mail, this
 * one manages what the workspace pays for and what it publishes.
 *
 *   GET/POST /api/v1/salesshift/billing/*
 *   GET/POST /api/v1/salesshift/social/*
 *   GET/PATCH/POST /api/v1/salesshift/opportunities/*
 *   GET/POST/PUT/DELETE /api/v1/salesshift/tasks
 *   GET/POST /api/v1/salesshift/campaigns/*
 *
 * Wire format is snake_case; this module speaks camelCase and maps at the
 * boundary, matching every other module in the SDK.
 */

import type { Transport } from './transport.js';

/* ── billing ─────────────────────────────────────────────────────────── */

/** Per-seat monthly allowances. `null` means unlimited — never 0, which would
 *  read as "no allowance", the opposite of what the API means. */
export interface PlanQuotas {
  emails: number | null;
  reveals: number | null;
  ai: number | null;
  mailboxes: number | null;
  contacts: number | null;
}

/** What a plan buys FROM US, as opposed to how much of it.
 *
 *  The distinction the free tier rests on: Self-Hosted is not a throttled
 *  Starter, it is the same product running on the tenant's own node, mailboxes
 *  and model key — which is why it is free. Its `quotas.emails` is 0, and that
 *  does not mean "may not send": it means we are not the one sending, and
 *  `sending === false` is what says so. Branch on these flags, never on a
 *  quota of zero or a price of zero. */
export interface ManagedBy {
  compute: boolean;
  sending: boolean;
  ai: boolean;
}

export interface Plan {
  id: string;
  /** self_hosted | starter | professional | organization */
  code: string;
  name: string;
  tagline: string | null;
  unitAmountCents: number;
  priceDisplay: string;
  currency: string;
  interval: string;
  features: string[];
  quotas: PlanQuotas;
  isFree: boolean;
  managed: ManagedBy;
  isPurchasable: boolean;
  /** A free plan is activated, not bought — no card, no $0 Stripe Price.
   *  Call `activatePlan()`, not `checkout()`. */
  isActivatable: boolean;
}

/** The workspace's live allowance, already pooled across seats — unlike
 *  {@link PlanQuotas}, which is the per-seat figure on the price list. */
export interface EntitlementQuotas extends PlanQuotas {
  users: number | null;
}

/** What the workspace may actually do, as opposed to what it pays. The server
 *  refuses over-quota work with HTTP 402, so reading this first lets a caller
 *  say why before it tries. */
export interface Entitlements {
  planCode: string;
  planName: string;
  status: string;
  /** stripe | comp | manual | free */
  source: string;
  seats: number;
  isFree: boolean;
  managed: ManagedBy;
  allowance: EntitlementQuotas;
  selfHosted: {
    required: boolean;
    nodeHost: string | null;
    verifiedAt: string | null;
    /** `required` and a node registered. False on a self-hosted plan means
     *  sending and agents are refused until one is. */
    ready: boolean;
  };
}

/** The node registration screen's whole state. */
export interface SelfHostedStatus {
  required: boolean;
  host: string | null;
  verifiedAt: string | null;
  fingerprint: string | null;
  /** A probe of the node just now. `null` when none is registered; a node that
   *  is down is still the registered node, so this reports rather than throws. */
  live: {
    reachable: boolean;
    version?: string | null;
    tenantName?: string | null;
    time?: string | null;
    error?: string;
  } | null;
  install: {
    /** The workspace UUID — always accepted, never ambiguous. */
    tenantId: string;
    /** Every value a node may report as its tenant id. The workspace NAME is
     *  in here too when it is unique across all organizations, because nodes
     *  provisioned before the handshake carry `TENANT_ID=<name>`. */
    accepts: string[];
    image: string;
    healthPath: string;
  };
}

export interface NodeRegistration {
  host: string;
  verified: boolean;
  version: string | null;
  tenantName: string | null;
  entitlements: Entitlements;
}

export interface Subscription {
  id?: string;
  status: string;
  entitled: boolean;
  source: string | null;
  seats: number;
  plan: Plan | null;
  monthlyTotalDisplay?: string;
  currentPeriodEnd?: string | null;
  cancelAtPeriodEnd?: boolean;
  note?: string | null;
  hasStripeCustomer?: boolean;
  allowance?: PlanQuotas;
  members?: number;
  seatsShortfall?: number;
  /** Always present, even with no subscription row — an unsubscribed workspace
   *  is on the free tier, not in limbo. */
  entitlements?: Entitlements;
}

export interface Invoice {
  id: string;
  number: string | null;
  status: string | null;
  amountDue: number | null;
  amountPaid: number | null;
  currency: string | null;
  hostedInvoiceUrl: string | null;
  invoicePdf: string | null;
}

const quotas = (q: Record<string, unknown> | undefined): PlanQuotas => ({
  emails: (q?.emails as number) ?? null,
  reveals: (q?.reveals as number) ?? null,
  ai: (q?.ai as number) ?? null,
  mailboxes: (q?.mailboxes as number) ?? null,
  contacts: (q?.contacts as number) ?? null,
});

const managed = (m: Record<string, unknown> | undefined): ManagedBy => ({
  compute: Boolean(m?.compute),
  sending: Boolean(m?.sending),
  ai: Boolean(m?.ai),
});

const toPlan = (p: Record<string, unknown>): Plan => ({
  id: String(p.id ?? ''),
  code: String(p.code ?? ''),
  name: String(p.name ?? ''),
  tagline: (p.tagline as string) ?? null,
  unitAmountCents: Number(p.unit_amount_cents ?? 0),
  priceDisplay: String(p.price_display ?? ''),
  currency: String(p.currency ?? 'usd'),
  interval: String(p.interval ?? 'month'),
  features: (p.features as string[]) ?? [],
  quotas: quotas(p.quotas as Record<string, unknown>),
  isFree: Boolean(p.is_free),
  managed: managed(p.managed as Record<string, unknown>),
  isPurchasable: Boolean(p.is_purchasable),
  isActivatable: Boolean(p.is_activatable),
});

const toEntitlements = (e: Record<string, unknown>): Entitlements => {
  const sh = (e.self_hosted as Record<string, unknown>) ?? {};
  const a = (e.allowance as Record<string, unknown>) ?? {};
  return {
    planCode: String(e.plan_code ?? ''),
    planName: String(e.plan_name ?? ''),
    status: String(e.status ?? 'none'),
    source: String(e.source ?? ''),
    seats: Number(e.seats ?? 0),
    isFree: Boolean(e.is_free),
    managed: managed(e.managed as Record<string, unknown>),
    allowance: { ...quotas(a), users: (a.users as number) ?? null },
    selfHosted: {
      required: Boolean(sh.required),
      nodeHost: (sh.node_host as string) ?? null,
      verifiedAt: (sh.verified_at as string) ?? null,
      ready: Boolean(sh.ready),
    },
  };
};

const toSubscription = (s: Record<string, unknown>): Subscription => ({
  id: s.id ? String(s.id) : undefined,
  status: String(s.status ?? 'none'),
  entitled: Boolean(s.entitled),
  source: (s.source as string) ?? null,
  seats: Number(s.seats ?? 0),
  plan: s.plan ? toPlan(s.plan as Record<string, unknown>) : null,
  monthlyTotalDisplay: (s.monthly_total_display as string) ?? undefined,
  currentPeriodEnd: (s.current_period_end as string) ?? null,
  cancelAtPeriodEnd: Boolean(s.cancel_at_period_end),
  note: (s.note as string) ?? null,
  hasStripeCustomer: Boolean(s.has_stripe_customer),
  allowance: s.allowance ? quotas(s.allowance as Record<string, unknown>) : undefined,
  members: s.members !== undefined ? Number(s.members) : undefined,
  seatsShortfall: s.seats_shortfall !== undefined ? Number(s.seats_shortfall) : undefined,
  entitlements: s.entitlements
    ? toEntitlements(s.entitlements as Record<string, unknown>)
    : undefined,
});

export class SalesShiftBilling {
  constructor(private t: Transport) {}

  /** The published tiers. `paymentsEnabled` false = view only, no checkout. */
  async plans(): Promise<{ plans: Plan[]; paymentsEnabled: boolean }> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/billing/plans');
    const b = res.body ?? {};
    return {
      plans: ((b.plans as Record<string, unknown>[]) ?? []).map(toPlan),
      paymentsEnabled: Boolean(b.payments_enabled),
    };
  }

  /** This workspace's plan, seats and pooled allowance. */
  async subscription(): Promise<Subscription> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/billing/subscription');
    return toSubscription(res.body ?? {});
  }

  /** Opens a Stripe Checkout session. Nothing is charged until completed. */
  async checkout(planCode: string, seats = 1): Promise<{ url: string; sessionId: string }> {
    if (!planCode) throw new Error('salesshift.billing.checkout: planCode is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/billing/checkout', { plan_code: planCode, seats });
    const b = res.body ?? {};
    return { url: String(b.url ?? ''), sessionId: String(b.session_id ?? '') };
  }

  /** Re-reads a completed Checkout Session server-side and applies it. */
  async confirmCheckout(sessionId: string): Promise<{ applied: boolean; subscription?: Subscription }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/billing/checkout/confirm', { session_id: sessionId });
    const b = res.body ?? {};
    return {
      applied: Boolean(b.applied),
      subscription: b.subscription ? toSubscription(b.subscription as Record<string, unknown>) : undefined,
    };
  }

  /** Stripe hosted portal for cards, receipts and cancellation. */
  async portal(): Promise<string> {
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/billing/portal', {});
    return String(res.body?.url ?? '');
  }

  /** Change plan and/or seats. Omit either to leave it alone. */
  async change(opts: { planCode?: string; seats?: number }): Promise<Subscription> {
    const body: Record<string, unknown> = {};
    if (opts.planCode) body.plan_code = opts.planCode;
    if (opts.seats) body.seats = opts.seats;
    if (Object.keys(body).length === 0) {
      throw new Error('salesshift.billing.change: pass planCode and/or seats');
    }
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/billing/change', body);
    return toSubscription((res.body?.subscription as Record<string, unknown>) ?? {});
  }

  /** atPeriodEnd keeps access until the period closes — usually what "cancel"
   *  means to a customer. */
  async cancel(atPeriodEnd = true): Promise<Subscription> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/billing/cancel', { at_period_end: atPeriodEnd });
    return toSubscription((res.body?.subscription as Record<string, unknown>) ?? {});
  }

  async resume(): Promise<Subscription> {
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/billing/resume', {});
    return toSubscription((res.body?.subscription as Record<string, unknown>) ?? {});
  }

  /** Stripe is the ledger. A comped workspace has none — that is an empty
   *  list with a reason, not an error. */
  async invoices(): Promise<{ invoices: Invoice[]; reason?: string }> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/billing/invoices');
    const b = res.body ?? {};
    return {
      invoices: ((b.invoices as Record<string, unknown>[]) ?? []).map((i) => ({
        id: String(i.id ?? ''),
        number: (i.number as string) ?? null,
        status: (i.status as string) ?? null,
        amountDue: (i.amount_due as number) ?? null,
        amountPaid: (i.amount_paid as number) ?? null,
        currency: (i.currency as string) ?? null,
        hostedInvoiceUrl: (i.hosted_invoice_url as string) ?? null,
        invoicePdf: (i.invoice_pdf as string) ?? null,
      })),
      reason: (b.reason as string) ?? undefined,
    };
  }

  /** What this workspace may do, without the billing detail.
   *
   *  Separate from `subscription()` because the app asks this far more often
   *  than it asks about money, and a workspace with no subscription resolves
   *  here to the free tier — never to "no plan", which would leave the caller
   *  to guess what still works. */
  async entitlements(): Promise<Entitlements> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/billing/entitlements');
    return toEntitlements(res.body ?? {});
  }

  /** Put the workspace on a free plan. No card, no Stripe.
   *
   *  Free plans only — the server refuses any code not flagged free, and
   *  refuses to downgrade a live non-free plan whether it was bought or
   *  granted. Give that up through `cancel()`, which tells Stripe. */
  async activatePlan(planCode: string): Promise<Subscription> {
    if (!planCode) throw new Error('salesshift.billing.activatePlan: planCode is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/billing/activate', { plan_code: planCode });
    return toSubscription((res.body?.subscription as Record<string, unknown>) ?? {});
  }

  /** The registered node, a live probe of it, and the identity values a node
   *  must report to be accepted. */
  async selfHosted(): Promise<SelfHostedStatus> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/billing/self-hosted');
    const b = res.body ?? {};
    const live = b.live as Record<string, unknown> | null | undefined;
    const install = (b.install as Record<string, unknown>) ?? {};
    return {
      required: Boolean(b.required),
      host: (b.host as string) ?? null,
      verifiedAt: (b.verified_at as string) ?? null,
      fingerprint: (b.fingerprint as string) ?? null,
      live: live
        ? {
            reachable: Boolean(live.reachable),
            version: (live.version as string) ?? null,
            tenantName: (live.tenant_name as string) ?? null,
            time: (live.time as string) ?? null,
            error: (live.error as string) ?? undefined,
          }
        : null,
      install: {
        tenantId: String(install.tenant_id ?? ''),
        accepts: (install.accepts as string[]) ?? [],
        image: String(install.image ?? ''),
        healthPath: String(install.health_path ?? '/health'),
      },
    };
  }

  /** Point the workspace at its own node — after the node proves whose it is.
   *
   *  The server does not take our word for it: it calls `GET {host}/health` and
   *  requires the node to report a tenant id in `selfHosted().install.accepts`.
   *  Setting that needs shell access to the machine, so a node that answers
   *  with this workspace's id is a node this workspace controls.
   *
   *  HTTPS is required (http:// only for localhost). Rejections: 400 no tenant
   *  reported or plaintext, 403 identifies as another workspace, 502
   *  unreachable. */
  async registerNode(host: string): Promise<NodeRegistration> {
    if (!host) throw new Error('salesshift.billing.registerNode: host is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/billing/self-hosted/node', { host });
    const b = res.body ?? {};
    return {
      host: String(b.host ?? ''),
      verified: Boolean(b.verified),
      version: (b.version as string) ?? null,
      tenantName: (b.tenant_name as string) ?? null,
      entitlements: toEntitlements((b.entitlements as Record<string, unknown>) ?? {}),
    };
  }

  /** Unregister the node. On a self-hosted plan, sending and agents stop until
   *  another one is registered — this is not a cosmetic change. */
  async detachNode(): Promise<void> {
    await this.t.delete('/api/v1/salesshift/billing/self-hosted/node');
  }
}

/* ── social ──────────────────────────────────────────────────────────── */

export interface SocialChannel {
  key: string;
  name: string;
  kind: string;
  maxChars: number;
  maxImages: number;
  typicalLatencyMs: number;
}

export interface SocialDelivery {
  channel: string;
  channelName: string | null;
  status: string;
  permalink: string | null;
  error: string | null;
  durationMs: number | null;
  /** True unless the deployment holds real social API credentials. Surface it
   *  — reporting a simulated post as published is the one unforgivable lie. */
  simulated: boolean;
}

export interface SocialPost {
  id: string;
  title: string | null;
  content: string;
  status: string;
  channels: string[];
  speedup: number | null;
  wallMs: number | null;
  sequentialMs: number | null;
  deliveries: SocialDelivery[];
  published?: number;
  rejected?: number;
  failed?: number;
}

export interface DistributeJob {
  jobId: string;
  wallMs: number;
  sequentialMs: number;
  speedup: number;
  concurrency: number;
  published: number;
  rejected: number;
  failed: number;
}

const toPost = (p: Record<string, unknown>): SocialPost => ({
  id: String(p.id ?? ''),
  title: (p.title as string) ?? null,
  content: String(p.content ?? ''),
  status: String(p.status ?? ''),
  channels: (p.channels as string[]) ?? [],
  speedup: (p.speedup as number) ?? null,
  wallMs: (p.wall_ms as number) ?? null,
  sequentialMs: (p.sequential_ms as number) ?? null,
  deliveries: ((p.deliveries as Record<string, unknown>[]) ?? []).map((d) => ({
    channel: String(d.channel ?? ''),
    channelName: (d.channel_name as string) ?? null,
    status: String(d.status ?? ''),
    permalink: (d.permalink as string) ?? null,
    error: (d.error as string) ?? null,
    durationMs: (d.duration_ms as number) ?? null,
    simulated: Boolean(d.simulated),
  })),
  published: p.published !== undefined ? Number(p.published) : undefined,
  rejected: p.rejected !== undefined ? Number(p.rejected) : undefined,
  failed: p.failed !== undefined ? Number(p.failed) : undefined,
});

export class SalesShiftSocial {
  constructor(private t: Transport) {}

  /** The catalogue, with each network's real limits. `available` false means
   *  the vxsocial service is down — drafts save, publishing does not. */
  async channels(): Promise<{ channels: SocialChannel[]; simulated: boolean; available: boolean }> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/social/channels');
    const b = res.body ?? {};
    return {
      channels: ((b.channels as Record<string, unknown>[]) ?? []).map((c) => ({
        key: String(c.key ?? ''),
        name: String(c.name ?? ''),
        kind: String(c.kind ?? ''),
        maxChars: Number(c.max_chars ?? 0),
        maxImages: Number(c.max_images ?? 0),
        typicalLatencyMs: Number(c.typical_latency_ms ?? 0),
      })),
      simulated: Boolean(b.simulated),
      available: b.available !== false,
    };
  }

  async listPosts(status?: string): Promise<SocialPost[]> {
    const path = `/api/v1/salesshift/social/posts${status ? `?status=${encodeURIComponent(status)}` : ''}`;
    const res = await this.t.get<{ posts?: Record<string, unknown>[] }>(path);
    return (res.body?.posts ?? []).map(toPost);
  }

  /** Saves a post. Set `scheduledAt` (RFC3339) to hand it to the Celery beat
   *  worker instead of distributing now. */
  async createPost(input: {
    content: string; title?: string; linkUrl?: string; images?: number;
    hashtags?: string[]; channels?: string[]; scheduledAt?: string;
  }): Promise<SocialPost> {
    if (!input.content) throw new Error('salesshift.social.createPost: content is required');
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/social/posts', {
      content: input.content,
      title: input.title,
      link_url: input.linkUrl,
      images: input.images ?? 0,
      hashtags: input.hashtags ?? [],
      channels: input.channels ?? [],
      scheduled_at: input.scheduledAt ?? null,
    });
    return toPost((res.body?.post as Record<string, unknown>) ?? {});
  }

  /** Fans the post out in parallel. `concurrency` 0 = as parallel as there are
   *  channels. The returned job carries the measured speedup. */
  async distribute(postId: string, concurrency = 0): Promise<{ post: SocialPost; job: DistributeJob }> {
    if (!postId) throw new Error('salesshift.social.distribute: postId is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/social/posts/${encodeURIComponent(postId)}/distribute`, { concurrency });
    const b = res.body ?? {};
    const j = (b.job as Record<string, unknown>) ?? {};
    return {
      post: toPost((b.post as Record<string, unknown>) ?? {}),
      job: {
        jobId: String(j.job_id ?? ''),
        wallMs: Number(j.wall_ms ?? 0),
        sequentialMs: Number(j.sequential_ms ?? 0),
        speedup: Number(j.speedup ?? 1),
        concurrency: Number(j.concurrency ?? 0),
        published: Number(j.published ?? 0),
        rejected: Number(j.rejected ?? 0),
        failed: Number(j.failed ?? 0),
      },
    };
  }

  async deletePost(postId: string): Promise<void> {
    await this.t.delete(`/api/v1/salesshift/social/posts/${encodeURIComponent(postId)}`);
  }

  async stats(): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>('/api/v1/salesshift/social/stats');
    return res.body ?? {};
  }

  /* webmaster — every result is read from the live URL, nothing is estimated */

  async inspect(url: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/social/webmaster/inspect', { url });
    return res.body ?? {};
  }

  async robots(url: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/social/webmaster/robots', { url });
    return res.body ?? {};
  }

  async sitemap(url: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/social/webmaster/sitemap', { url });
    return res.body ?? {};
  }

  async generate(domain: string, paths: string[], disallow?: string[]):
    Promise<{ robotsTxt: string; sitemapXml: string; urlCount: number }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v1/salesshift/social/webmaster/generate',
      { domain, paths, ...(disallow ? { disallow } : {}) });
    const b = res.body ?? {};
    return {
      robotsTxt: String(b.robots_txt ?? ''),
      sitemapXml: String(b.sitemap_xml ?? ''),
      urlCount: Number(b.url_count ?? 0),
    };
  }
}

/* ── opportunities ───────────────────────────────────────────────────── */

export interface Opportunity {
  id: string;
  title: string;
  description: string;
  companyName: string | null;
  contactEmail: string | null;
  location: string | null;
  source: string;
  signalType: string;
  sourceUrl: string | null;
  relevanceScore: number | null;
  isSaved: boolean;
  isDismissed: boolean;
}

const toOpp = (o: Record<string, unknown>): Opportunity => ({
  id: String(o.id ?? ''),
  title: String(o.title ?? ''),
  description: String(o.description ?? ''),
  companyName: (o.company_name as string) ?? null,
  contactEmail: (o.contact_email as string) ?? null,
  location: (o.location as string) ?? null,
  source: String(o.source ?? 'manual'),
  signalType: String(o.signal_type ?? 'manual'),
  sourceUrl: (o.source_url as string) ?? null,
  relevanceScore: (o.relevance_score as number) ?? null,
  isSaved: Boolean(o.is_saved),
  isDismissed: Boolean(o.is_dismissed),
});

export class SalesShiftOpportunities {
  constructor(private t: Transport) {}

  /** The pool is cross-tenant: these rows are shared by every workspace, and
   *  saved/dismissed is per-org state joined in, not a field on the row. */
  async list(filter: {
    q?: string; source?: string; signalType?: string; industry?: string;
    minScore?: number; savedOnly?: boolean; limit?: number;
  } = {}): Promise<{ data: Opportunity[]; sources: { source: string; count: number }[] }> {
    const p = new URLSearchParams();
    if (filter.q) p.set('q', filter.q);
    if (filter.source) p.set('source', filter.source);
    if (filter.signalType) p.set('signal_type', filter.signalType);
    if (filter.industry) p.set('industry', filter.industry);
    if (filter.minScore) p.set('min_score', String(filter.minScore));
    if (filter.savedOnly) p.set('saved_only', 'true');
    if (filter.limit) p.set('limit', String(filter.limit));
    const qs = p.toString();
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/opportunities${qs ? `?${qs}` : ''}`);
    const b = res.body ?? {};
    return {
      data: ((b.data as Record<string, unknown>[]) ?? []).map(toOpp),
      sources: (b.sources as { source: string; count: number }[]) ?? [],
    };
  }

  async get(id: string): Promise<Opportunity> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/opportunities/${encodeURIComponent(id)}`);
    return toOpp((res.body?.data as Record<string, unknown>) ?? {});
  }

  /** Per-organization only — other tenants still see the signal. */
  async save(id: string, saved = true): Promise<Opportunity> {
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v1/salesshift/opportunities/${encodeURIComponent(id)}`, { is_saved: saved });
    return toOpp((res.body?.data as Record<string, unknown>) ?? {});
  }

  async dismiss(id: string, dismissed = true): Promise<Opportunity> {
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v1/salesshift/opportunities/${encodeURIComponent(id)}`, { is_dismissed: dismissed });
    return toOpp((res.body?.data as Record<string, unknown>) ?? {});
  }

  /** Copies the signal's contact into the CRM. Fails when the signal
   *  published no email — there is nothing to create a contact from. */
  async pushToLead(id: string): Promise<{ leadId: string; created: boolean }> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/opportunities/${encodeURIComponent(id)}/push-to-lead`, {});
    const b = res.body ?? {};
    return { leadId: String(b.lead_id ?? ''), created: Boolean(b.created) };
  }
}

/* ── tasks & campaigns ───────────────────────────────────────────────── */

export interface Task {
  id: string;
  title: string;
  description: string | null;
  taskType: string;
  status: string;
  priority: string;
  dueAt: string | null;
  /** What has to exist when this is finished — the field that makes a board
   *  useful rather than a list of reminders. */
  goal: string | null;
  progress: number;
  assigneeId: number | null;
  assigneeName: string | null;
}

const toTask = (t: Record<string, unknown>): Task => ({
  id: String(t.id ?? ''),
  title: String(t.title ?? ''),
  description: (t.description as string) ?? null,
  taskType: String(t.task_type ?? 'todo'),
  status: String(t.status ?? 'open'),
  priority: String(t.priority ?? 'medium'),
  dueAt: (t.due_at as string) ?? null,
  goal: (t.goal as string) ?? null,
  progress: Number(t.progress ?? 0),
  assigneeId: (t.assignee_id as number) ?? null,
  assigneeName: (t.assignee_name as string) ?? null,
});

export class SalesShiftTasks {
  constructor(private t: Transport) {}

  async list(filter: { status?: string; taskType?: string; priority?: string; q?: string; limit?: number } = {}):
    Promise<{ tasks: Task[]; assignees: { id: number; name: string; email: string }[]; total: number }> {
    const p = new URLSearchParams();
    if (filter.status) p.set('status', filter.status);
    if (filter.taskType) p.set('task_type', filter.taskType);
    if (filter.priority) p.set('priority', filter.priority);
    if (filter.q) p.set('q', filter.q);
    if (filter.limit) p.set('limit', String(filter.limit));
    const qs = p.toString();
    const res = await this.t.get<Record<string, unknown>>(`/api/v1/salesshift/tasks${qs ? `?${qs}` : ''}`);
    const b = res.body ?? {};
    return {
      tasks: ((b.data as Record<string, unknown>[]) ?? []).map(toTask),
      assignees: (b.assignees as { id: number; name: string; email: string }[]) ?? [],
      total: Number(b.total ?? 0),
    };
  }

  async create(input: {
    title: string; description?: string; goal?: string; dueAt?: string;
    taskType?: string; priority?: string; progress?: number; assigneeId?: number;
  }): Promise<Task> {
    if (!input.title) throw new Error('salesshift.tasks.create: title is required');
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v1/salesshift/tasks', {
      title: input.title,
      description: input.description,
      goal: input.goal,
      due_at: input.dueAt,
      task_type: input.taskType,
      priority: input.priority,
      progress: input.progress,
      assignee_id: input.assigneeId,
    });
    return toTask(res.body ?? {});
  }

  /** Marking a task done stamps completion and forces progress to 100
   *  server-side, so the board cannot disagree with itself. */
  async update(id: string, patch: Partial<{
    title: string; description: string; status: string; priority: string;
    taskType: string; dueAt: string; goal: string; progress: number; assigneeId: number;
  }>): Promise<Task> {
    const body: Record<string, unknown> = {};
    if (patch.title !== undefined) body.title = patch.title;
    if (patch.description !== undefined) body.description = patch.description;
    if (patch.status !== undefined) body.status = patch.status;
    if (patch.priority !== undefined) body.priority = patch.priority;
    if (patch.taskType !== undefined) body.task_type = patch.taskType;
    if (patch.dueAt !== undefined) body.due_at = patch.dueAt;
    if (patch.goal !== undefined) body.goal = patch.goal;
    if (patch.progress !== undefined) body.progress = patch.progress;
    if (patch.assigneeId !== undefined) body.assignee_id = patch.assigneeId;
    const res = await this.t.putJSON<Record<string, unknown>>(
      `/api/v1/salesshift/tasks/${encodeURIComponent(id)}`, body);
    return toTask(res.body ?? {});
  }

  async delete(id: string): Promise<void> {
    await this.t.delete(`/api/v1/salesshift/tasks/${encodeURIComponent(id)}`);
  }
}

export interface CampaignRecipient {
  toEmail: string;
  status: string;
  contactId: string | null;
  contactName: string | null;
  company: string | null;
  opened: boolean;
  clicked: boolean;
  replied: boolean;
  sentAt: string | null;
}

export class SalesShiftCampaigns {
  constructor(private t: Transport) {}

  async list(): Promise<Record<string, unknown>[]> {
    const res = await this.t.get<{ data?: Record<string, unknown>[] }>('/api/v1/salesshift/campaigns');
    return res.body?.data ?? [];
  }

  /** Full report: the campaign, every tracked recipient (with the CRM contact
   *  behind it) and the hourly engagement timeline. */
  async get(id: string): Promise<{
    campaign: Record<string, unknown>;
    recipients: CampaignRecipient[];
    timeline: Record<string, unknown>[];
  }> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v1/salesshift/campaigns/${encodeURIComponent(id)}`);
    const b = res.body ?? {};
    return {
      campaign: (b.data as Record<string, unknown>) ?? {},
      recipients: ((b.recipients as Record<string, unknown>[]) ?? []).map((r) => ({
        toEmail: String(r.to_email ?? ''),
        status: String(r.status ?? ''),
        contactId: (r.contact_id as string) ?? null,
        contactName: (r.contact_name as string) ?? null,
        company: (r.company as string) ?? null,
        opened: Boolean(r.opened),
        clicked: Boolean(r.clicked),
        replied: Boolean(r.replied),
        sentAt: (r.sent_at as string) ?? null,
      })),
      timeline: (b.timeline as Record<string, unknown>[]) ?? [],
    };
  }

  /** Sends now, or schedules when `sendAt` (RFC3339) is given. */
  async send(id: string, sendAt?: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v1/salesshift/campaigns/${encodeURIComponent(id)}/send`,
      sendAt ? { send_at: sendAt } : {});
    return (res.body?.data as Record<string, unknown>) ?? {};
  }
}
