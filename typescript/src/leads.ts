/**
 * Leads — the global prospect pool, and this tenant's saved copies of it.
 *
 * Endpoints (infinity control plane, all under /api/v1/salesshift):
 *   POST /leads/search            POST /leads/facets       GET  /leads/quota
 *   POST /leads/reveal            POST /leads/save         GET  /leads/pool/{id}
 *   GET  /leads/company/{id}      GET  /leads              GET  /leads/{id}
 *   PATCH /leads/{id}             POST /leads/{id}/convert POST /leads/bulk-convert
 *   POST /leads/convert-from-pool POST /leads/erasure
 *   GET  /lead-searches           POST /lead-searches
 *
 * `Transport.absoluteURL` already routes `/api/v1/salesshift/*` to the Infinity
 * URL rather than the tenant node, so these paths stay relative like the
 * `salesshift email/*` methods do — see transport.ts.
 *
 * ── The five rules this module exists to keep a client from breaking ────────
 *
 * 1. **Leads are not mailable.** Nothing here hands a lead to a send, sequence
 *    or campaign call. `convertLead` / `convertFromPool` are the only routes to
 *    a mailable Contact, and they are where consent metadata is written. A
 *    scraped record entering a send path is how a tenant's sending domain dies.
 * 2. **An unrevealed address is a MASK** (`j•••@acme.com`), not an address.
 *    `hasEmail` says an address EXISTS; `emailRevealed` says you may see it.
 *    Use {@link revealedEmail} to get a value that is safe to treat as real —
 *    it returns `null` rather than a mask.
 * 3. **Reveal spends metered quota.** `revealLead` and
 *    `convertFromPool({ revealIfNeeded: true })` cost money. `revealIfNeeded`
 *    is deliberately REQUIRED here so no caller spends quota by omission, and
 *    {@link estimateRevealCost} exists to price a batch before you run it.
 * 4. **`convertFromPool` accounts for every id.** Render every bucket with
 *    {@link describeConvert}; printing only `converted` hides a partial spend.
 * 5. **Erasure is global and irreversible.** `requestErasure` removes a person
 *    for EVERY tenant, not just yours, and cannot be undone — so it requires
 *    `confirmGlobalErasure: true`.
 *
 * The `nextCursor` is OPAQUE: never parse one, never build one, never carry one
 * across a filter or sort change. See {@link Leads.searchAllLeads} for a walk
 * that handles the keyset correctly.
 */

import type { Transport } from './transport.js';
import { VxError, VxNotFoundError, VxValidationError, type VxErrorPayload } from './errors.js';

// ──────────────────────────────────────────────────────────────────────────
// Server-side limits, surfaced so callers can validate before a round trip
// ──────────────────────────────────────────────────────────────────────────

/** Max ids per `saveLeads` / `convertFromPool` call. */
export const LEADS_MAX_BATCH = 200;
/** Max `limit` on a pool search page. */
export const LEADS_MAX_PAGE_SIZE = 100;
/** Max rows `listLeads` will return in one call. */
export const LEADS_MAX_LIST_LIMIT = 500;
/** `total` is counted up to here and then reported as an estimate. */
export const LEADS_TOTAL_CAP = 10_000;

// ──────────────────────────────────────────────────────────────────────────
// Errors worth catching by name
// ──────────────────────────────────────────────────────────────────────────

/**
 * HTTP 402 — the org's reveal allowance for this period is spent.
 *
 * **Nothing was charged for the attempt that raised this.** Say so: a user who
 * thinks a failed reveal still cost them stops trusting the meter entirely.
 *
 * Extends `VxValidationError` (which is what the transport produces for 4xx
 * today) so existing `instanceof VxValidationError` branches keep working.
 */
export class VxLeadQuotaExceededError extends VxValidationError {
  constructor(p: VxErrorPayload) {
    super(p);
    this.name = 'VxLeadQuotaExceededError';
  }
}

/**
 * HTTP 410 — the person asked to be forgotten and the record is gone.
 *
 * Terminal. This is not an outage and not a rate limit: retrying will never
 * succeed, and the record will not come back on the next crawl either.
 */
export class VxLeadErasedError extends VxValidationError {
  constructor(p: VxErrorPayload) {
    super(p);
    this.name = 'VxLeadErasedError';
  }
}

// ──────────────────────────────────────────────────────────────────────────
// Filters, sorting, search input
// ──────────────────────────────────────────────────────────────────────────

export type LeadResultType = 'person' | 'company';

export type LeadSeniority =
  | 'founder' | 'c_suite' | 'vp' | 'director' | 'manager' | 'senior' | 'entry';

export type LeadDepartment =
  | 'engineering' | 'sales' | 'marketing' | 'finance'
  | 'hr' | 'ops' | 'legal' | 'executive';

export type LeadEmailStatus =
  | 'verified' | 'unverified' | 'guessed' | 'catch_all' | 'invalid';

export type LeadEmployeeRange =
  | '1-10' | '11-50' | '51-200' | '201-500' | '501-1000' | '1001-5000' | '5000+';

/** Sortable fields for `resultType: 'person'`. */
export type LeadPersonSortField =
  | 'score' | 'name' | 'title' | 'company' | 'location' | 'employees' | 'email';

/** Sortable fields for `resultType: 'company'`. */
export type LeadCompanySortField =
  | 'score' | 'name' | 'employees' | 'industry' | 'location';

/**
 * Any sortable field. The `(string & {})` arm keeps autocomplete for the known
 * fields while still accepting whatever the server echoes back, so a field
 * added server-side does not become a type error in an old SDK build.
 */
export type LeadSortField =
  | LeadPersonSortField
  | LeadCompanySortField
  // eslint-disable-next-line @typescript-eslint/ban-types
  | (string & {});

export interface LeadSort {
  field: LeadSortField;
  desc: boolean;
}

/**
 * Pool filters. Every list is OR'd internally except `excludeTitles`, which is
 * AND'd as NOT. String matches are substrings, not exact matches.
 */
export interface LeadFilters {
  /** Free text over name / title / company. */
  q?: string;
  /** Substring match on title, OR'd together. */
  titles?: string[];
  /** Substring match on title, each AND'd as NOT. */
  excludeTitles?: string[];
  seniorities?: LeadSeniority[];
  departments?: LeadDepartment[];
  /** ISO-2 country codes. Upper-cased for you before they go on the wire. */
  countries?: string[];
  industries?: string[];
  emailStatuses?: LeadEmailStatus[];
  employeeRanges?: LeadEmployeeRange[];
  /** Lower-cased for you before they go on the wire. */
  companyDomains?: string[];
  /** SUBSTRING against company keywords — "saas" also matches "saas management". */
  keywords?: string[];
  minScore?: number;
  /**
   * Only rows where an address EXISTS. Note: this says nothing about whether
   * you may SEE it — that is `emailRevealed` on the result row.
   */
  hasEmail?: boolean;
  hasPhone?: boolean;
}

export interface LeadSearchInput {
  filters?: LeadFilters;
  /** Default `'person'`. */
  resultType?: LeadResultType;
  /**
   * Opaque keyset position from a previous page's `nextCursor`.
   *
   * Never construct one, never parse one, and never reuse one after changing
   * the filters or the sort — a keyset position only means anything within the
   * result set AND the ordering it came from. Reusing it across a sort change
   * compares the wrong column and silently drops or repeats rows.
   */
  cursor?: string | null;
  /** 1–100. Values outside that range are clamped, matching the server. */
  limit?: number;
  /**
   * An unknown field degrades to `score` desc server-side — which is why the
   * result carries the sort that was APPLIED. Render that one, not this one.
   */
  sort?: LeadSort;
}

// ──────────────────────────────────────────────────────────────────────────
// Result rows
// ──────────────────────────────────────────────────────────────────────────

/** The company summary carried alongside a person in a search result. */
export interface PoolPersonCompany {
  name: string | null;
  domain: string | null;
  logoUrl: string | null;
  employeeCount: number | null;
  industry: string | null;
  /** Industries beyond the one shown. */
  industriesMore: number;
  /** First couple of keywords only. */
  keywords: string[];
  /** Keywords beyond the ones listed. */
  keywordsMore: number;
}

/** One person in the pool, as returned by a search. */
export interface PoolPerson {
  poolId: string;
  fullName: string | null;
  title: string | null;
  seniority: string | null;
  department: string | null;
  /**
   * **A MASK (`j•••@acme.com`) unless `emailRevealed` is true.** Never present
   * this as an address, never copy it as one, never feed it to anything that
   * sends. Use {@link revealedEmail} when you need a value that is either a
   * real address or `null`.
   */
  email: string | null;
  /** Whether this org has paid to see the address. */
  emailRevealed: boolean;
  emailStatus: string | null;
  /** An address EXISTS. Not that you may see it. */
  hasEmail: boolean;
  /** `null` unless revealed. */
  phone: string | null;
  phoneAvailable: boolean;
  phoneCount: number;
  /** `null` unless revealed. */
  linkedinUrl: string | null;
  location: string;
  /** Flattened from the wire's `{ value }` object — the Go node emits a flat
   *  int and the ORM fallback an object, so the SDK normalises both to a
   *  number and a client reading it can't silently render every row as zero. */
  score: number;
  company: PoolPersonCompany;
  /** Set once this org has saved the row into its own list. */
  savedLeadId: string | null;
}

/** One company in the pool, as returned by a `resultType: 'company'` search. */
export interface PoolCompanyResult {
  poolId: string;
  name: string | null;
  domain: string | null;
  logoUrl: string | null;
  industry: string | null;
  industriesMore: number;
  employeeCount: number | null;
  employeeRange: string | null;
  location: string;
  keywords: string[];
  keywordsMore: number;
  score: number;
}

/** One page of pool results. Both `resultType`s share this shape. */
export interface LeadSearchPage<TItem> {
  resultType: LeadResultType;
  items: TItem[];
  /**
   * Capped at {@link LEADS_TOTAL_CAP}. **Do not render this when
   * `totalIsEstimate` is true** — render `totalDisplay` ("10,000+") instead.
   */
  total: number;
  /** Always safe to render: "260", "1.4K", "10,000+". */
  totalDisplay: string;
  /** True once the count hit the cap; `total` is then a floor, not a count. */
  totalIsEstimate: boolean;
  /** Opaque. Feed straight back as `cursor`; `null` means you reached the end. */
  nextCursor: string | null;
  /** "go-node", or "fastapi-orm" while the node is down. */
  searchBackend: string;
  /** The sort the server APPLIED — render this, not the one you asked for. */
  sort: LeadSort;
}

export type LeadPersonSearchPage = LeadSearchPage<PoolPerson> & { resultType: 'person' };
export type LeadCompanySearchPage = LeadSearchPage<PoolCompanyResult> & { resultType: 'company' };
export type LeadSearchResult = LeadPersonSearchPage | LeadCompanySearchPage;

export interface LeadFacetBucket {
  value: string;
  count: number;
}

/** Counts beside each filter. Pool-wide — no tenant overlay. */
export interface LeadFacets {
  seniority: LeadFacetBucket[];
  department: LeadFacetBucket[];
  country: LeadFacetBucket[];
  emailStatus: LeadFacetBucket[];
  /** Only populated by the Go node backend; `[]` on the ORM fallback. */
  industry: LeadFacetBucket[];
  searchBackend: string;
}

// ──────────────────────────────────────────────────────────────────────────
// Quota + reveal
// ──────────────────────────────────────────────────────────────────────────

/** Reveals spent this period. `remaining` is authoritative — do not derive it. */
export interface RevealQuota {
  used: number;
  /** -1 when the org is uncapped. Read {@link unlimited} rather than testing
   *  this for -1 — a caller that renders `allowance` verbatim shows "-1". */
  allowance: number;
  /** Large finite number when uncapped (never Infinity), so integer
   *  comparisons and arithmetic keep working. */
  remaining: number;
  /** True when this org has no cap. The server sends it; without it a caller
   *  sees `allowance: -1` and has no way to tell "uncapped" from "broken", so
   *  uncapped orgs get told their allowance is spent. */
  unlimited: boolean;
  /** "3 / 200". */
  display: string;
}

/** What a reveal bought. These fields are real addresses, not masks. */
export interface RevealResult {
  poolId: string;
  email: string | null;
  phone: string | null;
  linkedinUrl: string | null;
  /** Quota AFTER the spend. */
  quota: RevealQuota;
}

// ──────────────────────────────────────────────────────────────────────────
// Saved leads
// ──────────────────────────────────────────────────────────────────────────

export interface SavedLeadCompany {
  name: string | null;
  domain: string | null;
  employeeRange: string | null;
  industry: string | null;
}

/**
 * A lead in this org's own list. A SNAPSHOT taken at save time — the pool is
 * re-crawled continuously and a qualified list must not mutate underneath the
 * person who qualified it.
 */
export interface SavedLead {
  id: string;
  poolPersonId: string | null;
  firstName: string | null;
  lastName: string | null;
  fullName: string | null;
  title: string | null;
  seniority: string | null;
  department: string | null;
  /**
   * The REAL address, or `null` when this org has not revealed it (or when the
   * person has since been erased). Unlike {@link PoolPerson.email} this is never
   * a mask — the mask lives in `emailMasked`.
   */
  email: string | null;
  emailStatus: string | null;
  /** `j•••@acme.com`, from the live pool row. Display only. */
  emailMasked: string | null;
  /** An address EXISTS on the live pool row — reveal to obtain it. */
  hasEmail: boolean;
  phone: string | null;
  linkedinUrl: string | null;
  phoneAvailable: boolean;
  company: SavedLeadCompany;
  location: string;
  status: string | null;
  score: number;
  source: string | null;
  notes: string | null;
  tags: string[];
  ownerId: string | null;
  /** True once someone erased this person. The lead is kept as an audit row
   *  but is stripped of contact data and can never be converted. */
  erasurePending: boolean;
  convertedContactId: string | null;
  convertedAt: string | null;
  createdAt: string | null;
}

/** The live pool row behind a saved lead, for comparison against the snapshot. */
export interface LeadPoolSnapshot {
  poolId: string;
  isActive: boolean;
  emailRevealed: boolean;
  emailStatus: string | null;
  title: string | null;
  companyName: string | null;
  qualityScore: number;
  lastVerifiedAt: string | null;
}

export interface SavedLeadDetail extends SavedLead {
  /** `null` when the lead has no pool row behind it (imported, or erased). */
  pool: LeadPoolSnapshot | null;
  /** Human-readable diffs where the pool has moved on since the snapshot.
   *  Empty when the snapshot is still accurate. */
  drift: string[];
}

export interface UpdateLeadInput {
  status?: string;
  score?: number;
  notes?: string;
  disqualifyReason?: string;
  ownerId?: string;
  tags?: string[];
}

// ──────────────────────────────────────────────────────────────────────────
// Pool detail views
// ──────────────────────────────────────────────────────────────────────────

/** The company block on a pool person's detail view. */
export interface PoolCompanyBrief {
  poolId: string | null;
  name: string | null;
  domain: string | null;
  logoUrl: string | null;
  website: string | null;
  linkedinUrl: string | null;
  description: string | null;
  industry: string | null;
  industries: string[];
  employeeCount: number | null;
  employeeRange: string | null;
  revenueRange: string | null;
  foundedYear: number | null;
  city: string | null;
  country: string | null;
  keywords: string[];
  techStack: string[];
  score: number;
}

/** Everything the pool knows about one person, plus this org's relationship
 *  to them. Masking applies here exactly as it does in search. */
export interface PoolPersonDetail {
  poolId: string;
  fullName: string | null;
  firstName: string | null;
  lastName: string | null;
  title: string | null;
  seniority: string | null;
  department: string | null;
  /** **A MASK unless `emailRevealed` is true.** See {@link revealedEmail}. */
  email: string | null;
  emailRevealed: boolean;
  emailStatus: string | null;
  emailConfidence: number | null;
  hasEmail: boolean;
  phone: string | null;
  phoneCount: number;
  phoneAvailable: boolean;
  linkedinUrl: string | null;
  linkedinAvailable: boolean;
  city: string | null;
  country: string | null;
  location: string;
  score: number;
  source: string | null;
  /** Freshness — the honest counterweight to a confident-looking record. */
  firstSeenAt: string | null;
  lastVerifiedAt: string | null;
  company: PoolCompanyBrief;
  savedLeadId: string | null;
  savedStatus: string | null;
  convertedContactId: string | null;
  /** This org already has a Contact on this address — do not re-buy them. */
  existingContactId: string | null;
}

/** A person listed under a company detail view. */
export interface PoolCompanyPerson {
  poolId: string;
  fullName: string | null;
  title: string | null;
  seniority: string | null;
  department: string | null;
  /** **A MASK unless `emailRevealed` is true.** */
  email: string | null;
  emailRevealed: boolean;
  emailStatus: string | null;
  hasEmail: boolean;
  phoneAvailable: boolean;
  phoneCount: number;
  location: string;
  score: number;
  savedLeadId: string | null;
  existingContactId: string | null;
}

/** A company in the pool with its people split by what this org already owns. */
export interface PoolCompanyDetail {
  poolId: string;
  name: string | null;
  legalName: string | null;
  domain: string | null;
  website: string | null;
  linkedinUrl: string | null;
  logoUrl: string | null;
  description: string | null;
  industry: string | null;
  industries: string[];
  employeeCount: number | null;
  employeeRange: string | null;
  revenueRange: string | null;
  foundedYear: number | null;
  city: string | null;
  stateProvince: string | null;
  country: string | null;
  location: string;
  keywords: string[];
  techStack: string[];
  score: number;
  source: string | null;
  firstSeenAt: string | null;
  lastVerifiedAt: string | null;
  isActive: boolean;
  /** Everyone (capped at 100), highest score first. */
  people: PoolCompanyPerson[];
  /** The subset this org does NOT already have as Contacts. */
  newProspects: PoolCompanyPerson[];
  /** The subset already in Contacts — what is left to work, not a headcount. */
  existingContacts: PoolCompanyPerson[];
  peopleTotal: number;
  departments: LeadFacetBucket[];
}

// ──────────────────────────────────────────────────────────────────────────
// Save / convert / erasure results
// ──────────────────────────────────────────────────────────────────────────

export interface SaveLeadsResult {
  saved: number;
  alreadySaved: number;
}

export interface ConvertLeadResult {
  contactId: string;
  /** True when this lead had already been converted — nothing new happened. */
  alreadyConverted: boolean;
  /** True when an existing Contact on the same address was reused. */
  reusedExistingContact: boolean;
}

/**
 * `bulk-convert` outcome. Converts leads this org has already saved, so it
 * never touches reveal quota — the buckets `convertFromPool` reports for
 * metering (`revealedNow`, `skippedNoQuota`, `skippedErased`) do not exist here.
 */
export interface BulkConvertReport {
  converted: number;
  skippedNoEmail: number;
  alreadyConverted: number;
}

/**
 * `convert-from-pool` outcome. **Accounts for every id you passed in.**
 * Rendering only `converted` hides a partial spend — pass this to
 * {@link describeConvert}, which prints every bucket.
 */
export interface ConvertFromPoolReport {
  converted: number;
  /** Reveals spent by this call. Each one is a unit of metered quota. */
  revealedNow: number;
  alreadyConverted: number;
  /** Either the allowance ran out mid-batch, or `revealIfNeeded` was false. */
  skippedNoQuota: number;
  /** The pool has no address for these — reveal would return nothing. */
  skippedNoEmail: number;
  /** Erased at the person's request. Terminal; do not retry these. */
  skippedErased: number;
  contactIds: string[];
  /** Quota AFTER the batch. */
  quota: RevealQuota;
}

export interface ErasureInput {
  email?: string;
  linkedinUrl?: string;
  note?: string;
  /** Default `'gdpr_erasure'`. */
  reason?: string;
  /**
   * Must be `true`.
   *
   * Erasure removes this person from the pool for **every tenant on the
   * platform**, not just yours, strips them from every org's saved leads, and
   * blocks them from being re-crawled. It cannot be undone. Requiring an
   * explicit acknowledgement here is the SDK's equivalent of the confirmation
   * prompt the contract requires of any client that exposes this.
   */
  confirmGlobalErasure: true;
}

export interface ErasureResult {
  /** Pool rows deactivated and stripped — across all tenants. */
  poolRowsErased: number;
  /** Saved leads flagged `erasurePending` — across all tenants. */
  savedLeadsFlagged: number;
  /** This address was already on the erasure list before you asked. */
  alreadyRecorded: boolean;
}

// ── enrichment ────────────────────────────────────────────────────────────

/** What to crawl. Exactly one of the two is required. */
export interface EnrichInput {
  /** A company already in the pool. */
  companyId?: string;
  /** A hostname. If no company holds it, the crawl CREATES the company and
   *  any people it finds — this is how a domain the pool has never seen ends
   *  up on file. */
  domain?: string;
}

/**
 * What a crawl read, and what it wrote.
 *
 * Read `crawled` first. When it is 0, `note` says WHY — blocked by a CDN,
 * nothing readable, a server error — and every other field is 0, because
 * nothing was written.
 */
export interface EnrichResult {
  companyId: string | null;
  companyCreated: boolean;
  /** Pages successfully read. */
  crawled: number;
  /** Pages tried. Only sent when `crawled` is 0. */
  attempted: number | null;
  /** Company fields the crawl FILLED. A crawl never overwrites a value that
   *  was already there, so this is always gaps. */
  changed: string[];
  peopleFound: number;
  peopleAdded: number;
  peopleAlreadyKnown: number;
  /** People a crawl refused to re-create because they asked to be forgotten.
   *  Never hide this from a user. */
  peopleSkippedErased: number;
  statusCodes: number[];
  elapsedMs: number | null;
  note: string | null;
}

export interface SavedSearch {
  id: string;
  name: string;
  filters: Record<string, unknown>;
  isShared: boolean;
}

export interface SavedSearchRef {
  id: string;
  name: string;
}

export interface SaveSearchInput {
  name: string;
  filters?: LeadFilters;
  isShared?: boolean;
}

/** Options for {@link Leads.searchAllLeads}. */
export interface SearchAllLeadsOptions {
  /** Stop after this many people. Default 10,000 — the server's count cap. */
  maxItems?: number;
  /** Stop after this many pages, whatever `maxItems` says. Default 500. */
  maxPages?: number;
  /** Rows per request, 1–100. Default 100 — fewer round trips for a full walk. */
  pageSize?: number;
  /** Checked between pages; aborts the walk with an error. */
  signal?: AbortSignal;
  /** Called with each page before its rows are yielded. */
  onPage?: (page: LeadPersonSearchPage) => void;
}

// ──────────────────────────────────────────────────────────────────────────
// Leads resource
// ──────────────────────────────────────────────────────────────────────────

/**
 * The leads surface — `client.leads`.
 *
 * Deliberately NOT hung off `client.salesshift`, even though the endpoints sit
 * under `/api/v1/salesshift/*`: that class is the SEND surface (`sendEmail`,
 * `listEmails`), and rule 1 says a lead must never reach a send path. Keeping
 * the two objects apart means no one can reach a lead and a send call through
 * the same handle by accident, and it matches how every other domain in this
 * SDK gets its own module and its own top-level accessor.
 */
export class Leads {
  constructor(private t: Transport) {}

  // ── search ──────────────────────────────────────────────────────────────

  /**
   * Search the global pool.
   *
   * Rows come back MASKED. `emailRevealed` tells you whether a given row's
   * `email` is a real address or `j•••@acme.com`; `hasEmail` only tells you one
   * exists. Nothing here spends quota.
   *
   * Render `page.totalDisplay` (never `page.total` when `totalIsEstimate`) and
   * `page.sort` (the sort the server APPLIED — an unknown field degrades to
   * `score` desc rather than erroring).
   *
   *     const page = await client.leads.searchLeads({
   *       filters: { seniorities: ['director'], countries: ['AU'] },
   *       sort: { field: 'score', desc: true },
   *       limit: 25,
   *     });
   *     console.log(`${page.totalDisplay} matches, sorted by ${page.sort.field}`);
   */
  async searchLeads(input?: LeadSearchInput & { resultType?: 'person' }): Promise<LeadPersonSearchPage>;
  async searchLeads(input: LeadSearchInput & { resultType: 'company' }): Promise<LeadCompanySearchPage>;
  async searchLeads(input: LeadSearchInput): Promise<LeadSearchResult>;
  async searchLeads(input: LeadSearchInput = {}): Promise<LeadSearchResult> {
    const resultType: LeadResultType = input.resultType ?? 'person';
    const body: Record<string, unknown> = {
      filters: filtersToWire(input.filters),
      result_type: resultType,
      // The cursor is passed through byte-for-byte. Never parsed, never built.
      cursor: input.cursor ?? '',
      limit: clamp(input.limit ?? 25, 1, LEADS_MAX_PAGE_SIZE),
      sort: input.sort ? { field: input.sort.field, desc: input.sort.desc } : null,
    };
    const data = asObj(await this.post('searchLeads', '/api/v1/salesshift/leads/search', body));
    return toSearchPage(data, resultType);
  }

  /**
   * Walk an entire result set, following `nextCursor` to exhaustion.
   *
   * People only: the company path does not paginate on the ORM fallback, so a
   * generic walk over it would silently stop after one page during a node
   * outage rather than after the last row.
   *
   * The walk owns the keyset, which is what makes it safe: the filters and the
   * sort are pinned for its whole lifetime, and after the first page it re-sends
   * the sort the server ECHOED rather than the one requested — so a field that
   * degraded to `score` desc stays consistent across every page instead of the
   * cursor being compared against a different column.
   *
   * Bounded by `maxItems` (default 10,000) and `maxPages` (default 500), and it
   * stops if the server ever repeats a cursor rather than looping forever.
   *
   *     for await (const p of client.leads.searchAllLeads(
   *       { filters: { departments: ['engineering'] } }, { maxItems: 500 },
   *     )) {
   *       console.log(p.fullName, p.emailRevealed ? p.email : '(masked)');
   *     }
   */
  async *searchAllLeads(
    input: LeadSearchInput = {},
    opts: SearchAllLeadsOptions = {},
  ): AsyncGenerator<PoolPerson, void, undefined> {
    const maxItems = Math.max(0, opts.maxItems ?? LEADS_TOTAL_CAP);
    const maxPages = Math.max(1, opts.maxPages ?? 500);
    const pageSize = clamp(opts.pageSize ?? LEADS_MAX_PAGE_SIZE, 1, LEADS_MAX_PAGE_SIZE);

    // A resumed cursor is only valid for the filters + sort it came from; the
    // caller passing one is asserting they have not changed since.
    let cursor: string = input.cursor ?? '';
    let sort: LeadSort | undefined = input.sort;
    let yielded = 0;
    let pages = 0;
    const seenCursors = new Set<string>();

    while (pages < maxPages && yielded < maxItems) {
      if (opts.signal?.aborted) {
        throw new Error('leads.searchAllLeads: aborted');
      }
      const page = await this.searchLeads({
        ...input,
        resultType: 'person',
        cursor,
        limit: pageSize,
        ...(sort ? { sort } : {}),
      });
      pages += 1;
      // Pin to what the server actually applied, for every page after this one.
      sort = page.sort;
      if (opts.onPage) opts.onPage(page);

      if (page.items.length === 0) return;
      for (const item of page.items) {
        yield item;
        yielded += 1;
        if (yielded >= maxItems) return;
      }

      const next = page.nextCursor;
      if (!next) return;
      // A repeated cursor means the server is not advancing. Stop rather than
      // spin: an infinite paging loop on a metered API is worse than a short read.
      if (seenCursors.has(next)) return;
      seenCursors.add(next);
      cursor = next;
    }
  }

  /** Counts per seniority / department / country / email status / industry for
   *  the given filters. Pool-wide — the number of Directors in the pool is the
   *  same for every tenant, so nothing here is masked or metered. */
  async leadFacets(filters: LeadFilters = {}): Promise<LeadFacets> {
    const data = asObj(await this.post('leadFacets', '/api/v1/salesshift/leads/facets', {
      filters: filtersToWire(filters),
    }));
    return {
      seniority: toFacets(data.seniority),
      department: toFacets(data.department),
      country: toFacets(data.country),
      emailStatus: toFacets(data.email_status),
      industry: toFacets(data.industry),
      searchBackend: str(data.search_backend),
    };
  }

  // ── quota + reveal ──────────────────────────────────────────────────────

  /** Reveals used / allowance / remaining for the current period. */
  async revealQuota(): Promise<RevealQuota> {
    const data = asObj(await this.get('revealQuota', '/api/v1/salesshift/leads/quota'));
    return toQuota(data);
  }

  /**
   * Un-mask one person. **SPENDS ONE UNIT OF METERED QUOTA** unless this org has
   * already revealed them, in which case it is free.
   *
   * Throws {@link VxLeadQuotaExceededError} (402) when the allowance is spent —
   * and nothing was charged for that attempt. Throws {@link VxLeadErasedError}
   * (410) when the person asked to be forgotten; that is terminal, not an outage.
   *
   * Check `revealQuota()` first if you want to show the cost before spending it.
   */
  async revealLead(poolPersonId: string): Promise<RevealResult> {
    if (!poolPersonId) throw new Error('leads.revealLead: poolPersonId is required');
    const data = asObj(await this.post('revealLead', '/api/v1/salesshift/leads/reveal', {
      pool_person_id: poolPersonId,
    }));
    return {
      poolId: str(data.pool_id),
      email: strOrNull(data.email),
      phone: strOrNull(data.phone),
      linkedinUrl: strOrNull(data.linkedin_url),
      quota: toQuota(asObj(data.quota)),
    };
  }

  // ── save + list ─────────────────────────────────────────────────────────

  /**
   * Copy pool rows into this org's saved leads. Free — saving is not revealing,
   * and a lead saved while masked stores no address until you reveal it.
   *
   * Max {@link LEADS_MAX_BATCH} ids per call.
   */
  async saveLeads(poolPersonIds: readonly string[]): Promise<SaveLeadsResult> {
    const ids = requireBatch('saveLeads', poolPersonIds);
    const data = asObj(await this.post('saveLeads', '/api/v1/salesshift/leads/save', {
      pool_person_ids: ids,
    }));
    return { saved: num(data.saved), alreadySaved: num(data.already_saved) };
  }

  /** Full detail for one pool person. Masked exactly as search is — a detail
   *  view is not a back door around the meter. */
  async getPoolPerson(poolId: string): Promise<PoolPersonDetail> {
    if (!poolId) throw new Error('leads.getPoolPerson: poolId is required');
    const data = asObj(await this.get(
      'getPoolPerson', `/api/v1/salesshift/leads/pool/${encodeURIComponent(poolId)}`));
    return toPoolPersonDetail(data);
  }

  /** A pool company plus its people, split into `newProspects` and
   *  `existingContacts` by what this org already holds. */
  async getPoolCompany(companyId: string): Promise<PoolCompanyDetail> {
    if (!companyId) throw new Error('leads.getPoolCompany: companyId is required');
    const data = asObj(await this.get(
      'getPoolCompany', `/api/v1/salesshift/leads/company/${encodeURIComponent(companyId)}`));
    return toPoolCompanyDetail(data);
  }

  /** This org's saved leads. `limit` is clamped to {@link LEADS_MAX_LIST_LIMIT}. */
  async listLeads(opts: { status?: string; limit?: number } = {}): Promise<SavedLead[]> {
    const qs = new URLSearchParams();
    if (opts.status) qs.set('status', opts.status);
    if (opts.limit !== undefined) qs.set('limit', String(clamp(opts.limit, 1, LEADS_MAX_LIST_LIMIT)));
    const q = qs.toString();
    const data = await this.get('listLeads', `/api/v1/salesshift/leads${q ? `?${q}` : ''}`);
    return asArr(data).map((row) => toSavedLead(asObj(row)));
  }

  /** One saved lead, plus the live pool row behind it and a `drift` list of the
   *  fields that have moved since the snapshot was taken. */
  async getLead(leadId: string): Promise<SavedLeadDetail> {
    if (!leadId) throw new Error('leads.getLead: leadId is required');
    const data = asObj(await this.get(
      'getLead', `/api/v1/salesshift/leads/${encodeURIComponent(leadId)}`));
    const pool = data.pool;
    return {
      ...toSavedLead(data),
      pool: pool && typeof pool === 'object' ? toPoolSnapshot(asObj(pool)) : null,
      drift: strArr(data.drift),
    };
  }

  /** Update status / score / notes / tags / owner on a saved lead. Only the
   *  keys you pass are sent, so this never blanks a field by omission. */
  async updateLead(leadId: string, input: UpdateLeadInput): Promise<SavedLead> {
    if (!leadId) throw new Error('leads.updateLead: leadId is required');
    const body: Record<string, unknown> = {};
    if (input.status !== undefined) body.status = input.status;
    if (input.score !== undefined) body.score = input.score;
    if (input.notes !== undefined) body.notes = input.notes;
    if (input.disqualifyReason !== undefined) body.disqualify_reason = input.disqualifyReason;
    if (input.ownerId !== undefined) body.owner_id = input.ownerId;
    if (input.tags !== undefined) body.tags = input.tags;
    if (Object.keys(body).length === 0) {
      throw new Error('leads.updateLead: pass at least one field to update');
    }
    const data = asObj(await this.patch(
      'updateLead', `/api/v1/salesshift/leads/${encodeURIComponent(leadId)}`, body));
    return toSavedLead(data);
  }

  // ── convert — the one-way gate into mailable Contacts ────────────────────

  /**
   * Saved lead → Contact. **This is the only route to a mailable record** and
   * the point at which consent metadata is written; the lead row is kept as an
   * audit trail, never moved.
   *
   * Requires a revealed address: a 400 here means either the lead has no
   * address yet (reveal it first) or the person was erased. Read the message.
   */
  async convertLead(leadId: string, opts: { lifecycleStage?: string } = {}): Promise<ConvertLeadResult> {
    if (!leadId) throw new Error('leads.convertLead: leadId is required');
    const data = asObj(await this.post(
      'convertLead', `/api/v1/salesshift/leads/${encodeURIComponent(leadId)}/convert`,
      opts.lifecycleStage ? { lifecycle_stage: opts.lifecycleStage } : {},
    ));
    return {
      contactId: str(data.contact_id),
      alreadyConverted: bool(data.already_converted),
      reusedExistingContact: bool(data.reused_existing_contact),
    };
  }

  /**
   * Convert many SAVED leads. Spends no quota — these are leads this org
   * already holds; anything still masked lands in `skippedNoEmail`.
   *
   * Pass the result to {@link describeConvert} rather than printing `converted`
   * on its own.
   */
  async bulkConvertLeads(leadIds: readonly string[]): Promise<BulkConvertReport> {
    if (!leadIds || leadIds.length === 0) {
      throw new Error('leads.bulkConvertLeads: at least one leadId is required');
    }
    const data = asObj(await this.post('bulkConvertLeads', '/api/v1/salesshift/leads/bulk-convert', {
      lead_ids: [...leadIds],
    }));
    return {
      converted: num(data.converted),
      skippedNoEmail: num(data.skipped_no_email),
      alreadyConverted: num(data.already_converted),
    };
  }

  /**
   * Pool → Contact in one step: save, reveal if needed, convert.
   *
   * **This SPENDS METERED QUOTA when `revealIfNeeded` is true** — one unit per
   * previously-unrevealed person. `revealIfNeeded` has no default on purpose:
   * no caller should spend an org's allowance by leaving a field out. Set it
   * `false` to convert only the already-revealed rows and have the rest come
   * back as `skippedNoQuota`, spending nothing.
   *
   * Price the batch first with {@link estimateRevealCost}, and render the result
   * with {@link describeConvert} — the report accounts for EVERY id you passed
   * in (`converted` / `alreadyConverted` / `skippedNoQuota` / `skippedNoEmail` /
   * `skippedErased`), and showing only the success count of a partial spend is
   * what destroys trust in the meter.
   *
   * Max {@link LEADS_MAX_BATCH} ids per call — a bigger batch would spend more
   * quota than anyone intends in one click.
   */
  async convertFromPool(input: {
    poolPersonIds: readonly string[];
    /** Required. `true` spends quota for every unrevealed person in the batch. */
    revealIfNeeded: boolean;
    lifecycleStage?: string;
  }): Promise<ConvertFromPoolReport> {
    const ids = requireBatch('convertFromPool', input?.poolPersonIds);
    if (typeof input.revealIfNeeded !== 'boolean') {
      throw new Error(
        'leads.convertFromPool: revealIfNeeded must be set explicitly (true spends '
        + 'reveal quota for every unrevealed person in the batch; false converts only '
        + 'already-revealed rows and reports the rest as skippedNoQuota, spending nothing)',
      );
    }
    const data = asObj(await this.post('convertFromPool', '/api/v1/salesshift/leads/convert-from-pool', {
      pool_person_ids: ids,
      reveal_if_needed: input.revealIfNeeded,
      lifecycle_stage: input.lifecycleStage ?? 'lead',
    }));
    return {
      converted: num(data.converted),
      revealedNow: num(data.revealed_now),
      alreadyConverted: num(data.already_converted),
      skippedNoQuota: num(data.skipped_no_quota),
      skippedNoEmail: num(data.skipped_no_email),
      skippedErased: num(data.skipped_erased),
      contactIds: strArr(data.contact_ids),
      quota: toQuota(asObj(data.quota)),
    };
  }

  // ── erasure ─────────────────────────────────────────────────────────────

  /**
   * Right to be forgotten.
   *
   * **GLOBAL AND IRREVERSIBLE.** This does not clean up your copy of a person —
   * it deactivates them in the pool for every tenant on the platform, strips
   * the address, phone and LinkedIn from every organisation's saved leads, and
   * records a hash so the crawler will not resurrect them. There is no undo, and
   * the caller's org has no special standing here: an erasure scoped to the
   * requester would be theatre.
   *
   * Requires `confirmGlobalErasure: true` — the SDK's stand-in for the explicit
   * confirmation any client exposing this must obtain.
   */
  async requestErasure(input: ErasureInput): Promise<ErasureResult> {
    if (input?.confirmGlobalErasure !== true) {
      throw new Error(
        'leads.requestErasure: pass confirmGlobalErasure: true. Erasure removes this '
        + 'person from the pool for EVERY tenant, strips them from every org\'s saved '
        + 'leads, and cannot be undone.',
      );
    }
    const email = (input.email ?? '').trim();
    const linkedinUrl = (input.linkedinUrl ?? '').trim();
    if (!email && !linkedinUrl) {
      throw new Error('leads.requestErasure: email or linkedinUrl is required');
    }
    const data = asObj(await this.post('requestErasure', '/api/v1/salesshift/leads/erasure', {
      ...(email ? { email } : {}),
      ...(linkedinUrl ? { linkedin_url: linkedinUrl } : {}),
      ...(input.note ? { note: input.note } : {}),
      reason: input.reason ?? 'gdpr_erasure',
    }));
    return {
      poolRowsErased: num(data.pool_rows_erased),
      savedLeadsFlagged: num(data.saved_leads_flagged),
      alreadyRecorded: bool(data.already_recorded),
    };
  }

  // ── enrichment ──────────────────────────────────────────────────────────

  /**
   * Crawl a company's own website and fold what it finds into the pool.
   *
   * The only call on this class that WRITES to the shared pool, so it is worth
   * knowing what it will and will not do:
   *
   * - **Gaps only.** An existing description, keyword set or address is never
   *   replaced by crawl output.
   * - **Erasure is checked before every insert**, so a crawl cannot resurrect
   *   someone who asked to be forgotten.
   * - **Shared mailboxes are not people.** `sales@`, `info@`, `announce@` and
   *   their regional variants are dropped, and a name is derived from an
   *   address only when the local part plausibly is one.
   * - **Everything found is UNVERIFIED**, and no reveal quota is spent.
   *
   * Slow by nature — it fetches up to a dozen pages, and the server's own
   * ceiling is 90s — so show a pending state rather than assuming it returns
   * quickly.
   */
  async enrich(input: EnrichInput): Promise<EnrichResult> {
    const companyId = (input?.companyId ?? '').trim();
    const domain = (input?.domain ?? '').trim().toLowerCase()
      .replace(/^https?:\/\//, '').replace(/^www\./, '').split('/')[0];
    if (!companyId && !domain) {
      throw new Error('leads.enrich: companyId or domain is required');
    }
    const data = asObj(await this.post('enrich', '/api/v1/salesshift/leads/enrich', {
      ...(companyId ? { company_id: companyId } : {}),
      ...(domain ? { domain } : {}),
    }));
    return {
      companyId: strOrNull(data.company_id),
      companyCreated: bool(data.company_created),
      crawled: num(data.crawled),
      attempted: numOrNull(data.attempted),
      changed: strArr(data.changed),
      peopleFound: num(data.people_found),
      peopleAdded: num(data.people_added),
      peopleAlreadyKnown: num(data.people_already_known),
      peopleSkippedErased: num(data.people_skipped_erased),
      statusCodes: asArr(data.status_codes).map((c) => num(c)),
      elapsedMs: numOrNull(data.elapsed_ms),
      note: strOrNull(data.note),
    };
  }

  // ── saved searches ──────────────────────────────────────────────────────

  /** This org's saved searches. */
  async listSavedSearches(): Promise<SavedSearch[]> {
    const data = await this.get('listSavedSearches', '/api/v1/salesshift/lead-searches');
    return asArr(data).map((row) => {
      const r = asObj(row);
      return {
        id: str(r.id),
        name: str(r.name),
        filters: asObj(r.filters),
        isShared: bool(r.is_shared),
      };
    });
  }

  /** Save a filter set for reuse. Filters only — a saved search never carries a
   *  cursor, because a keyset position is meaningless outside the page it came
   *  from. */
  async saveSearch(input: SaveSearchInput): Promise<SavedSearchRef> {
    const name = (input?.name ?? '').trim();
    if (!name) throw new Error('leads.saveSearch: name is required');
    const data = asObj(await this.post('saveSearch', '/api/v1/salesshift/lead-searches', {
      name,
      filters: filtersToWire(input.filters),
      is_shared: Boolean(input.isShared),
    }));
    return { id: str(data.id), name: str(data.name) };
  }

  // ── transport plumbing ──────────────────────────────────────────────────

  private async post(op: string, path: string, body: unknown): Promise<unknown> {
    try {
      const res = await this.t.postJSON<Record<string, unknown>>(path, body);
      return payloadOf(res.body);
    } catch (err) {
      throw leadsError(op, err);
    }
  }

  private async get(op: string, path: string): Promise<unknown> {
    try {
      const res = await this.t.get<Record<string, unknown>>(path);
      return payloadOf(res.body);
    } catch (err) {
      throw leadsError(op, err);
    }
  }

  private async patch(op: string, path: string, body: unknown): Promise<unknown> {
    try {
      const res = await this.t.patchJSON<Record<string, unknown>>(path, body);
      return payloadOf(res.body);
    } catch (err) {
      throw leadsError(op, err);
    }
  }
}

// ──────────────────────────────────────────────────────────────────────────
// Helpers a caller reaches for
// ──────────────────────────────────────────────────────────────────────────

/**
 * The address, or `null` — never a mask.
 *
 * `row.email` on a pool row is `j•••@acme.com` until the org pays to see it, and
 * a mask is not an address: it must never be copied as one or handed to
 * anything that sends. This is the accessor to use anywhere the value might be
 * treated as real. Erased rows return `null` too.
 */
export function revealedEmail(row: {
  email?: string | null;
  emailRevealed?: boolean;
  erasurePending?: boolean;
}): string | null {
  if (!row) return null;
  if (row.erasurePending) return null;
  // Pool rows carry `emailRevealed`; saved leads do not — their `email` is
  // already null until this org owns it, so absence of the flag is not "masked".
  if (row.emailRevealed === false) return null;
  return row.email ? row.email : null;
}

export interface RevealCostEstimate {
  /** Rows considered. */
  total: number;
  /** Already paid for — these cost nothing to use again. */
  alreadyRevealed: number;
  /** The pool has no address; revealing would buy nothing. */
  noAddress: number;
  /** Units of quota a reveal-everything run would spend. */
  willSpend: number;
  /** Remaining allowance, or `null` when no quota was supplied. */
  remaining: number | null;
  /** How many of `willSpend` the remaining allowance actually covers. */
  affordable: number;
  /** How many would come back as `skippedNoQuota`. */
  shortfall: number;
  /** One-line summary safe to show a user before they commit. */
  display: string;
}

/**
 * Price a batch BEFORE spending anything — rule 3: a bulk helper must surface
 * the cost before it acts.
 *
 * Works off rows you already have (search results, a company's people) plus an
 * optional {@link RevealQuota} from `revealQuota()`, and makes no network call,
 * so it is safe to run on every selection change.
 *
 *     const est = estimateRevealCost(selected, await client.leads.revealQuota());
 *     if (!confirm(est.display)) return;
 *     const report = await client.leads.convertFromPool({
 *       poolPersonIds: selected.map((p) => p.poolId), revealIfNeeded: true,
 *     });
 *     console.log(describeConvert(report, { requested: selected.length }));
 */
export function estimateRevealCost(
  rows: readonly { emailRevealed?: boolean; hasEmail?: boolean }[],
  quota?: RevealQuota | null,
): RevealCostEstimate {
  const list = rows ?? [];
  let alreadyRevealed = 0;
  let noAddress = 0;
  let willSpend = 0;
  for (const r of list) {
    if (r?.emailRevealed) { alreadyRevealed += 1; continue; }
    if (r?.hasEmail === false) { noAddress += 1; continue; }
    willSpend += 1;
  }
  const remaining = quota ? Math.max(0, quota.remaining) : null;
  // An uncapped org can always afford the batch. Its `remaining` is a large
  // finite sentinel, so the arithmetic below would "work" but render nonsense
  // like "1000000000 remaining now" — hence the explicit branch.
  const uncapped = Boolean(quota?.unlimited);
  const affordable = remaining === null || uncapped ? willSpend : Math.min(willSpend, remaining);
  const shortfall = willSpend - affordable;

  const parts = [`${list.length} selected`];
  if (alreadyRevealed) parts.push(`${alreadyRevealed} already revealed (free)`);
  if (noAddress) parts.push(`${noAddress} with no address`);
  parts.push(willSpend === 1 ? '1 reveal will be spent' : `${willSpend} reveals will be spent`);
  if (uncapped) {
    parts.push('unlimited allowance');
  } else if (remaining !== null) {
    parts.push(shortfall > 0
      ? `only ${affordable} of ${willSpend} affordable — ${shortfall} would come back as skippedNoQuota (${quota?.display ?? ''})`
      : `${remaining} remaining now, ${remaining - willSpend} after this`);
  }

  return {
    total: list.length,
    alreadyRevealed,
    noAddress,
    willSpend,
    remaining,
    affordable,
    shortfall,
    display: parts.join(' · '),
  };
}

/**
 * Render EVERY bucket of a convert report — rule 4.
 *
 * `convertFromPool` splits its outcome five ways and a client that prints only
 * `converted` is hiding a partial result: the ids that hit the quota wall, the
 * ones with no address, and the ones erased at the person's request all
 * disappear, and the user reads a partial spend as a clean success.
 *
 * Also accepts a {@link BulkConvertReport}; the buckets that endpoint does not
 * report (it spends no quota) are simply omitted rather than printed as zero.
 *
 * @param report the report returned by `convertFromPool` or `bulkConvertLeads`
 * @param opts.requested the ids you sent, or how many — used to flag any that
 *        the server did not account for
 */
export function describeConvert(
  report: ConvertFromPoolReport | BulkConvertReport,
  opts: { requested?: number | readonly string[] } = {},
): string {
  const r = report as Partial<ConvertFromPoolReport> & BulkConvertReport;
  const metered = typeof r.revealedNow === 'number';

  const rows: [string, string][] = [
    ['converted', String(r.converted ?? 0)],
    ['already converted', String(r.alreadyConverted ?? 0)],
  ];
  if (metered) rows.push(['skipped: no quota', String(r.skippedNoQuota ?? 0)]);
  rows.push(['skipped: no email', String(r.skippedNoEmail ?? 0)]);
  if (metered) rows.push(['skipped: erased', String(r.skippedErased ?? 0)]);
  if (metered) {
    const spent = r.revealedNow ?? 0;
    rows.push(['revealed now', `${spent}  (${spent === 1 ? '1 reveal' : `${spent} reveals`} spent)`]);
  }
  if (r.contactIds) rows.push(['contact ids', String(r.contactIds.length)]);
  if (r.quota) {
    rows.push(['reveal quota', `${r.quota.display} — ${r.quota.remaining} remaining`]);
  }

  const accounted = (r.converted ?? 0) + (r.alreadyConverted ?? 0)
    + (r.skippedNoQuota ?? 0) + (r.skippedNoEmail ?? 0) + (r.skippedErased ?? 0);
  const requested = Array.isArray(opts.requested)
    ? opts.requested.length
    : (typeof opts.requested === 'number' ? opts.requested : undefined);

  const title = metered ? 'convert-from-pool' : 'bulk-convert';
  const lines = [`${title} — ${accounted} ${accounted === 1 ? 'id' : 'ids'} accounted for`];
  const width = Math.max(...rows.map(([label]) => label.length));
  for (const [label, value] of rows) lines.push(`  ${label.padEnd(width)}  ${value}`);

  const skipped = (r.skippedNoQuota ?? 0) + (r.skippedNoEmail ?? 0) + (r.skippedErased ?? 0);
  if (skipped > 0) {
    lines.push(`  ! ${skipped} of ${accounted} were NOT converted — see the skipped rows above.`);
  }
  if ((r.skippedErased ?? 0) > 0) {
    lines.push('  ! erased records are terminal: they will not convert on a retry.');
  }
  if ((r.skippedNoQuota ?? 0) > 0) {
    lines.push('  ! no quota was charged for the skipped rows.');
  }
  if (!metered) {
    lines.push('  (bulk-convert works on already-saved leads; no reveal quota is spent)');
  }
  if (requested !== undefined && requested !== accounted) {
    lines.push(`  ! ${requested} ids were sent but ${accounted} came back accounted for — `
      + 'do not report this as a success.');
  }
  return lines.join('\n');
}

// ──────────────────────────────────────────────────────────────────────────
// Internals
// ──────────────────────────────────────────────────────────────────────────

/** Per-op hints for the status codes the contract says to handle by name. */
const NOT_FOUND_HINT: Record<string, string> = {
  searchLeads: '',
  getPoolPerson: 'This id is not in the global pool (it may never have existed, or it was erased).',
  getPoolCompany: 'This company id is not in the global pool.',
  revealLead: 'This id is not in the global pool — nothing was charged.',
  getLead: 'No saved lead with this id in YOUR organisation. Saved leads are per-tenant: a pool id is not a lead id.',
  updateLead: 'No saved lead with this id in YOUR organisation.',
  convertLead: 'No saved lead with this id in YOUR organisation.',
};

function leadsError(op: string, err: unknown): unknown {
  if (!(err instanceof VxError)) return err;
  const base: VxErrorPayload = {
    status: err.status,
    code: err.code,
    message: err.message,
    source: `leads.${op}`,
    body: err.body,
    cause: err.cause,
  };

  if (err.status === 402) {
    return new VxLeadQuotaExceededError({
      ...base,
      message: `${err.message} Nothing was charged for this attempt.`,
    });
  }
  if (err.status === 410) {
    return new VxLeadErasedError({
      ...base,
      message: `${err.message} This is terminal, not an outage: the record was erased at `
        + 'the person\'s request and will not return. Do not retry.',
    });
  }
  if (err.status === 404) {
    const hint = NOT_FOUND_HINT[op];
    return new VxNotFoundError({ ...base, message: hint ? `${err.message} ${hint}` : err.message });
  }
  if (err.status === 400 && /cursor/i.test(err.message)) {
    return new VxValidationError({
      ...base,
      message: `${err.message} A cursor is opaque and only valid for the exact filters and `
        + 'sort it came from — never build one, and never reuse one after changing either.',
    });
  }
  return err;
}

function filtersToWire(f: LeadFilters = {}): Record<string, unknown> {
  return {
    q: f.q ?? null,
    titles: [...(f.titles ?? [])],
    exclude_titles: [...(f.excludeTitles ?? [])],
    seniorities: [...(f.seniorities ?? [])],
    departments: [...(f.departments ?? [])],
    // Normalised client-side so the Go node and the ORM fallback agree: the ORM
    // path upper/lower-cases these itself, the node takes them as given.
    countries: (f.countries ?? []).map((c) => c.toUpperCase()),
    industries: [...(f.industries ?? [])],
    email_statuses: [...(f.emailStatuses ?? [])],
    employee_ranges: [...(f.employeeRanges ?? [])],
    company_domains: (f.companyDomains ?? []).map((d) => d.toLowerCase()),
    keywords: [...(f.keywords ?? [])],
    min_score: f.minScore ?? null,
    ...(f.hasEmail === undefined ? {} : { has_email: f.hasEmail }),
    ...(f.hasPhone === undefined ? {} : { has_phone: f.hasPhone }),
  };
}

function toSearchPage(data: Record<string, unknown>, requested: LeadResultType): LeadSearchResult {
  const resultType: LeadResultType = data.result_type === 'company' ? 'company' : (
    data.result_type === 'person' ? 'person' : requested
  );
  const rawItems = asArr(data.items);
  const total = num(data.total);
  const page = {
    total,
    // Never fabricate a display string from `total` when the count is capped —
    // fall back to the raw number only when the server said it is exact.
    totalDisplay: str(data.total_display) || (bool(data.total_is_estimate)
      ? `${LEADS_TOTAL_CAP.toLocaleString('en-US')}+`
      : String(total)),
    totalIsEstimate: bool(data.total_is_estimate),
    nextCursor: strOrNull(data.next_cursor),
    searchBackend: str(data.search_backend) || str(data.backend),
    sort: toSort(data.sort),
  };
  if (resultType === 'company') {
    return {
      ...page,
      resultType: 'company',
      items: rawItems.map((row) => toCompanyResult(asObj(row))),
    };
  }
  return {
    ...page,
    resultType: 'person',
    items: rawItems.map((row) => toPoolPerson(asObj(row))),
  };
}

function toSort(raw: unknown): LeadSort {
  const s = asObj(raw);
  return {
    field: str(s.field) || 'score',
    // The server defaults to descending; only an explicit false is ascending.
    desc: s.desc === undefined ? true : bool(s.desc),
  };
}

function toPoolPerson(r: Record<string, unknown>): PoolPerson {
  const c = asObj(r.company);
  return {
    poolId: str(r.pool_id),
    fullName: strOrNull(r.full_name),
    title: strOrNull(r.title),
    seniority: strOrNull(r.seniority),
    department: strOrNull(r.department),
    email: strOrNull(r.email) ?? strOrNull(r.email_masked),
    emailRevealed: bool(r.email_revealed),
    emailStatus: strOrNull(r.email_status),
    hasEmail: bool(r.has_email),
    phone: strOrNull(r.phone),
    phoneAvailable: bool(r.phone_available),
    phoneCount: num(r.phone_count),
    linkedinUrl: strOrNull(r.linkedin_url),
    location: str(r.location),
    score: scoreOf(r.score),
    company: {
      name: strOrNull(c.name),
      domain: strOrNull(c.domain),
      logoUrl: strOrNull(c.logo_url),
      employeeCount: numOrNull(c.employee_count),
      industry: strOrNull(c.industry),
      industriesMore: num(c.industries_more),
      keywords: strArr(c.keywords),
      keywordsMore: num(c.keywords_more),
    },
    savedLeadId: strOrNull(r.saved_lead_id),
  };
}

function toCompanyResult(r: Record<string, unknown>): PoolCompanyResult {
  return {
    poolId: str(r.pool_id),
    name: strOrNull(r.name),
    domain: strOrNull(r.domain),
    logoUrl: strOrNull(r.logo_url),
    industry: strOrNull(r.industry),
    industriesMore: num(r.industries_more),
    employeeCount: numOrNull(r.employee_count),
    employeeRange: strOrNull(r.employee_range),
    location: str(r.location),
    keywords: strArr(r.keywords),
    keywordsMore: num(r.keywords_more),
    score: scoreOf(r.score),
  };
}

function toPoolPersonDetail(r: Record<string, unknown>): PoolPersonDetail {
  const c = asObj(r.company);
  return {
    poolId: str(r.pool_id),
    fullName: strOrNull(r.full_name),
    firstName: strOrNull(r.first_name),
    lastName: strOrNull(r.last_name),
    title: strOrNull(r.title),
    seniority: strOrNull(r.seniority),
    department: strOrNull(r.department),
    email: strOrNull(r.email),
    emailRevealed: bool(r.email_revealed),
    emailStatus: strOrNull(r.email_status),
    emailConfidence: numOrNull(r.email_confidence),
    hasEmail: bool(r.has_email),
    phone: strOrNull(r.phone),
    phoneCount: num(r.phone_count),
    phoneAvailable: bool(r.phone_available),
    linkedinUrl: strOrNull(r.linkedin_url),
    linkedinAvailable: bool(r.linkedin_available),
    city: strOrNull(r.city),
    country: strOrNull(r.country),
    location: str(r.location),
    score: scoreOf(r.score),
    source: strOrNull(r.source),
    firstSeenAt: strOrNull(r.first_seen_at),
    lastVerifiedAt: strOrNull(r.last_verified_at),
    company: toCompanyBrief(c),
    savedLeadId: strOrNull(r.saved_lead_id),
    savedStatus: strOrNull(r.saved_status),
    convertedContactId: strOrNull(r.converted_contact_id),
    existingContactId: strOrNull(r.existing_contact_id),
  };
}

function toCompanyBrief(c: Record<string, unknown>): PoolCompanyBrief {
  return {
    poolId: strOrNull(c.pool_id),
    name: strOrNull(c.name),
    domain: strOrNull(c.domain),
    logoUrl: strOrNull(c.logo_url),
    website: strOrNull(c.website),
    linkedinUrl: strOrNull(c.linkedin_url),
    description: strOrNull(c.description),
    industry: strOrNull(c.industry),
    industries: strArr(c.industries),
    employeeCount: numOrNull(c.employee_count),
    employeeRange: strOrNull(c.employee_range),
    revenueRange: strOrNull(c.revenue_range),
    foundedYear: numOrNull(c.founded_year),
    city: strOrNull(c.city),
    country: strOrNull(c.country),
    keywords: strArr(c.keywords),
    techStack: strArr(c.tech_stack),
    score: scoreOf(c.score),
  };
}

function toCompanyPerson(r: Record<string, unknown>): PoolCompanyPerson {
  return {
    poolId: str(r.pool_id),
    fullName: strOrNull(r.full_name),
    title: strOrNull(r.title),
    seniority: strOrNull(r.seniority),
    department: strOrNull(r.department),
    email: strOrNull(r.email),
    emailRevealed: bool(r.email_revealed),
    emailStatus: strOrNull(r.email_status),
    hasEmail: bool(r.has_email),
    phoneAvailable: bool(r.phone_available),
    phoneCount: num(r.phone_count),
    location: str(r.location),
    score: scoreOf(r.score),
    savedLeadId: strOrNull(r.saved_lead_id),
    existingContactId: strOrNull(r.existing_contact_id),
  };
}

function toPoolCompanyDetail(r: Record<string, unknown>): PoolCompanyDetail {
  const people = (raw: unknown) => asArr(raw).map((row) => toCompanyPerson(asObj(row)));
  return {
    poolId: str(r.pool_id),
    name: strOrNull(r.name),
    legalName: strOrNull(r.legal_name),
    domain: strOrNull(r.domain),
    website: strOrNull(r.website),
    linkedinUrl: strOrNull(r.linkedin_url),
    logoUrl: strOrNull(r.logo_url),
    description: strOrNull(r.description),
    industry: strOrNull(r.industry),
    industries: strArr(r.industries),
    employeeCount: numOrNull(r.employee_count),
    employeeRange: strOrNull(r.employee_range),
    revenueRange: strOrNull(r.revenue_range),
    foundedYear: numOrNull(r.founded_year),
    city: strOrNull(r.city),
    stateProvince: strOrNull(r.state_province),
    country: strOrNull(r.country),
    location: str(r.location),
    keywords: strArr(r.keywords),
    techStack: strArr(r.tech_stack),
    score: scoreOf(r.score),
    source: strOrNull(r.source),
    firstSeenAt: strOrNull(r.first_seen_at),
    lastVerifiedAt: strOrNull(r.last_verified_at),
    isActive: r.is_active === undefined ? true : bool(r.is_active),
    people: people(r.people),
    newProspects: people(r.new_prospects),
    existingContacts: people(r.existing_contacts),
    peopleTotal: num(r.people_total),
    departments: toFacets(r.departments),
  };
}

function toSavedLead(r: Record<string, unknown>): SavedLead {
  const c = asObj(r.company);
  return {
    id: str(r.id),
    poolPersonId: strOrNull(r.pool_person_id),
    firstName: strOrNull(r.first_name),
    lastName: strOrNull(r.last_name),
    fullName: strOrNull(r.full_name),
    title: strOrNull(r.title),
    seniority: strOrNull(r.seniority),
    department: strOrNull(r.department),
    email: strOrNull(r.email),
    emailStatus: strOrNull(r.email_status),
    emailMasked: strOrNull(r.email_masked),
    hasEmail: bool(r.has_email),
    phone: strOrNull(r.phone),
    linkedinUrl: strOrNull(r.linkedin_url),
    phoneAvailable: bool(r.phone_available),
    company: {
      name: strOrNull(c.name),
      domain: strOrNull(c.domain),
      employeeRange: strOrNull(c.employee_range),
      industry: strOrNull(c.industry),
    },
    location: str(r.location),
    status: strOrNull(r.status),
    score: scoreOf(r.score),
    source: strOrNull(r.source),
    notes: strOrNull(r.notes),
    tags: strArr(r.tags),
    ownerId: strOrNull(r.owner_id),
    erasurePending: bool(r.erasure_pending),
    convertedContactId: strOrNull(r.converted_contact_id),
    convertedAt: strOrNull(r.converted_at),
    createdAt: strOrNull(r.created_at),
  };
}

function toPoolSnapshot(p: Record<string, unknown>): LeadPoolSnapshot {
  return {
    poolId: str(p.pool_id),
    isActive: bool(p.is_active),
    emailRevealed: bool(p.email_revealed),
    emailStatus: strOrNull(p.email_status),
    title: strOrNull(p.title),
    companyName: strOrNull(p.company_name),
    qualityScore: num(p.quality_score),
    lastVerifiedAt: strOrNull(p.last_verified_at),
  };
}

function toQuota(q: Record<string, unknown>): RevealQuota {
  const used = num(q.used);
  const allowance = num(q.allowance);
  // Trust the server's flag; fall back to the -1 sentinel only when it is
  // absent, so an older control plane still reports uncapped orgs correctly.
  const unlimited = q.unlimited === undefined ? allowance < 0 : Boolean(q.unlimited);
  return {
    used,
    allowance,
    // `remaining` is authoritative when the server sends it — deriving it is
    // how clients end up disagreeing with the meter they are displaying.
    remaining: q.remaining === undefined ? Math.max(0, allowance - used) : num(q.remaining),
    unlimited,
    display: str(q.display) || (unlimited ? `${used} revealed` : `${used} / ${allowance}`),
  };
}

function toFacets(raw: unknown): LeadFacetBucket[] {
  return asArr(raw).map((row) => {
    const r = asObj(row);
    return { value: str(r.value), count: num(r.count) };
  });
}

function requireBatch(op: string, ids: readonly string[] | undefined): string[] {
  if (!ids || ids.length === 0) {
    throw new Error(`leads.${op}: at least one poolPersonId is required`);
  }
  if (ids.length > LEADS_MAX_BATCH) {
    throw new Error(
      `leads.${op}: max ${LEADS_MAX_BATCH} ids per call (got ${ids.length}) — `
      + 'reveals are metered, so a bigger batch would spend more quota than anyone '
      + 'intends in one action. Chunk the ids and confirm the cost of each batch.',
    );
  }
  return [...ids];
}

/** `{success, data}` envelopes unwrap to `data`; flat bodies pass through. */
function payloadOf(body: unknown): unknown {
  if (body && typeof body === 'object' && !Array.isArray(body) && 'data' in body) {
    return (body as { data: unknown }).data ?? {};
  }
  return body ?? {};
}

function asObj(v: unknown): Record<string, unknown> {
  return v && typeof v === 'object' && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

function asArr(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

function str(v: unknown, fallback = ''): string {
  return v === null || v === undefined ? fallback : String(v);
}

function strOrNull(v: unknown): string | null {
  return v === null || v === undefined || v === '' ? null : String(v);
}

function strArr(v: unknown): string[] {
  return asArr(v).map((x) => String(x));
}

function num(v: unknown, fallback = 0): number {
  const n = typeof v === 'number' ? v : Number(v);
  return Number.isFinite(n) ? n : fallback;
}

function numOrNull(v: unknown): number | null {
  if (v === null || v === undefined || v === '') return null;
  const n = Number(v);
  return Number.isFinite(n) ? n : null;
}

function bool(v: unknown): boolean {
  return Boolean(v);
}

/** Score arrives as `{ value }` from the ORM path and as a flat int from the Go
 *  node. Normalise, or a client reading `.value` off an int renders every row
 *  as zero. */
function scoreOf(v: unknown): number {
  if (v && typeof v === 'object' && !Array.isArray(v)) {
    return num((v as Record<string, unknown>).value);
  }
  return num(v);
}

function clamp(n: number, lo: number, hi: number): number {
  if (!Number.isFinite(n)) return lo;
  return Math.min(hi, Math.max(lo, Math.trunc(n)));
}
