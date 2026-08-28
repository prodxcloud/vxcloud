# @vxcloud/sdk — Changelog

All notable changes to the TypeScript SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Pre-1.0 releases may break public API in any minor bump.

## [Unreleased]

## [2026.8.27]

Version-alignment release — **no code changed since `2026.8.26`**. Published so
every vxcloud surface reports one number; the desktop app, which had lagged at
`2026.8.17-0127`, rejoins the line here. See the
[root CHANGELOG](../CHANGELOG.md) for the full surface table.


## [2026.8.26]

First release to reach npm since `2026.8.14` — `2026.8.17` was tagged but never
published, so its **BREAKING** `INFINITY_*` → `VXCLOUD_*` environment rename
(documented below under 2026.8.17) lands here for `npm` users.

### Fixed — `sequences.enroll()` reported 0 skipped and no reasons, always

`skipped` is the **list** of `{contact_id, reason}`; it was read as a number, so
it came back `0` on every call. `skipped_details` is a key the route has never
sent, so no skip reason ever reached the caller. Both now derive from the one
list the route does send. Those entries carry no `email` field either, so the
one that decoded to `''` forever was removed.

### Fixed — `WorkflowRun.steps` was always `[]` on a list response

The list endpoint returns a **count**, not the step bodies — only the per-run
detail route populates `steps`. New `stepsCount` carries the number; reading
`steps` off a list response never worked.

### Added

- `billing.events(limit)` — the workspace's own append-only billing trail,
  newest first. Kept locally, so it answers for comped workspaces that Stripe
  knows nothing about. `amountCents` is `null` for events that moved no money.
  Server clamps `limit` to 1..100.
- `opportunities.convert(id)` — contact + deal in the workspace's default
  pipeline in one call. Idempotent: a second call hands back the ids the first
  one made with `alreadyConverted` set rather than minting a duplicate. Fails
  with 400 when the workspace has no pipeline or the pipeline has no stages.
- `workflows.runs(id, { limit })` — the route defaults to 50 and caps at 200,
  and does not page beyond that, so more than the newest 50 must be asked for.
- `opportunities.list({ category })` and `tasks.list({ assigneeId })` — filters
  the routes already accepted.
- New exported types: `BillingEvent`, `OpportunityConversion`.


## [2026.8.17]

### Changed — BREAKING: the Infinity environment variables were renamed

`INFINITY_*` is **removed, not deprecated** — nothing reads it, so a stale
config falls back to defaults silently instead of erroring.

| Old (no longer read) | New |
|---|---|
| `INFINITY_API_URL` | `VXCLOUD_API_URL` |
| `INFINITY_WS_URL` | `VXCLOUD_WS_URL` |
| `NEXT_PUBLIC_INFINITY_API_URL` | `NEXT_PUBLIC_VXCLOUD_API_URL` |
| `NEXT_PUBLIC_INFINITY_WS_URL` | `NEXT_PUBLIC_VXCLOUD_WS_URL` |

Identifiers followed: `infinityURL` → `vxcloudURL`, `WithInfinityURL` →
`WithVxCloudURL`. The node vars `VX_NODE_URL` / `NODE_URL` are unchanged.

## [2026.8.14]

### Changed — documentation

Same code as `2026.8.13`. npm cannot rewrite the README of a published version,
so the docs ship as their own release.

- New **SalesShift** section with runnable TypeScript: `c.leads` (search,
  `revealQuota`, `estimateRevealCost`, `revealLead`, `convertFromPool`),
  `c.salesshift`, `c.opportunities`, `c.tasks`, `c.campaigns`, `c.social` and
  `c.salesshiftBilling` — including the `simulated` delivery flag and the
  null-means-unlimited quota rule.
- Module table gains every SalesShift accessor; install table gains C++, Java
  and the CLI.
- Links block and author attribution.

## [2026.8.13]

### Added — Leads (`src/leads.ts`)

The prospect pool: search with filters and facets, pool person/company detail,
reveal with quota accounting, save, update, convert (single / bulk / straight
from the pool), company enrichment, saved searches, and GDPR erasure requests.

Kept separate from `SalesShift` on purpose — a lead is not mailable until it is
converted into a Contact, and collapsing the two would invite sending to
unconverted rows.

- Server-enforced limits are exported (`LEADS_MAX_BATCH`, `LEADS_MAX_PAGE_SIZE`,
  `LEADS_MAX_LIST_LIMIT`, `LEADS_TOTAL_CAP`) so callers chunk correctly instead
  of discovering the ceilings through 4xx responses.
- `VxLeadQuotaExceededError` and `VxLeadErasedError` are separate classes: the
  first means back off and retry later, the second means never ask again.
- `estimateRevealCost` prices a batch before quota is spent; `describeConvert`
  renders every bucket a convert splits into, because a partial success reported
  as success is how duplicate contacts get created.

### Added — SalesShift platform (`src/salesshift-platform.ts`)

`SalesShiftBilling`, `SalesShiftSocial`, `SalesShiftOpportunities`,
`SalesShiftTasks`, `SalesShiftCampaigns`.

- `PlanQuotas` fields are nullable — **null means unlimited**. A plain number
  would render as 0, which reads as "no allowance", the opposite of the truth.
- Every `SocialDelivery` carries `simulated`. Surface it: a deployment without
  social API credentials still returns a delivery record, and reporting a
  simulated post as published is the one unforgivable lie this SDK could tell.

### Added — SalesShift CRM (`src/salesshift-crm.ts`)

`SalesShiftContacts`, `SalesShiftWorkflows`, `SalesShiftSequences`. The
workflow type is exported as `SalesShiftWorkflow`: `Workflow` is already taken
by the VxCloud workflow engine (`./workflow.js`), a different product entirely.

### Added — `Transport` is now exported

Every resource constructor takes a `Transport`, so without it exported the
classes could be imported but never named in a typed signature. It is also the
escape hatch for an endpoint the SDK does not wrap yet, and the seam for
stubbing in tests (`new Leads(fakeTransport)`).

### Changed

- `VERSION` → `2026.8.13`, matching the Go, Python, C++ and Java SDKs.
- `agentcontrol` and `cicd` pick up the endpoints added server-side since
  `2026.6.15`.

## [2026.6.10]

- Adopt CalVer (`YYYY.M.D`) — the package version now tracks the vxnode fleet
  release date (e.g. `v2026.6.10-1`) so the SDK, binary, and dashboard all
  read the same number. No API changes vs. 0.1.1.

## [0.1.1]

### Added — M3 + M4
- `client.networks` — script catalog + remote-execute.
- `client.agents` — `coding / devops / git / parallel / run / presets / tools / tool`.
- `client.chat` — `send(...) / quick(provider, model, question)`.
- `client.observability.{backups, migrations, sync}` — backup CRUD,
  migration plan/execute, batch sync.
- `client.billing` — `multicloud / optimization`.
- `client.workspace` — full `/api/v2/setup/*` (workspace + organization
  lifecycle, cloud-provider creds, AI-provider creds, API tokens,
  Git/payment/SMTP/SSL/OAuth/OKTA/CyberArk credentials).

### Docs
- Rewrote the npm README: badges, tagline, nav, full 21-module capability
  table (now documents `agents`, `billing`, `chat`, `networks`,
  `observability`, `workspace`), cross-language SDK matrix.

## [0.1.0] — 2026-04-30

Initial release. Hand-written TypeScript SDK for Node.js 18+ with full
type definitions. Mirrors the Go and Python SDKs at the wire layer.

### Added

- `VxCloud` client class with two constructors:
  - `new VxCloud({ apiKey, username, ... })` — explicit credentials.
  - `VxCloud.loadFromVxcli()` — read `~/.vxcloud/credentials.json`.
- Resource modules:
  - `client.auth` — whoami, exchange, refresh.
  - `client.cicd` — pipelines (list, show, trigger), builds (show).
  - `client.sessions` — list, show, apply, pull, delete.
  - `client.install` — script, compose.
  - `client.deploy` — container, plus all 12 stack types
    (fastapi, react, nextjs, django, nodejs, python, golang, rust, cpp,
    php, static).
  - `client.services` — lifecycle plane mirroring `vxcli services`:
    list, status, start, stop, restart, remove, logs, plus
    `services.vm.{reboot, shutdown, diskCleanup, dockerCleanup,
    restartDocker, memory, disk, listServices, listContainers,
    killPort, stopService}`.
  - `client.marketplace` — agents/models/solutions: list, show, deploy.
  - `client.nodes` — list, default, setDefault.
  - `client.cloud` — VM, IAM, S3, Database, Network, Kubernetes,
    Serverless (thin in v0.1, full in v1.0).
- Auth model: `X-API-Key` + `Bearer` JWT, single-flight refresh on 401.
- Errors: typed tree (`VxAuthError`, `VxValidationError`,
  `VxRateLimitError`, `VxServerError`, `VxNetworkError`,
  `VxNotFoundError`) all extending `VxError`.
- `--key-pair-location` ergonomics: any SSH method also accepts
  `keyPairLocation` (path to a local PEM). Read locally and attached as
  `private_key_pem` multipart part.
- Built with `tsup` for ESM + CJS dual output. Types via TypeScript 5.3.

### Notes
- Browser support is best-effort (CORS may apply); the SDK targets Node.js
  primarily. A separate browser bundle ships at v1.0 if demand arises.
- Live `WebSocket` log streaming (planned in BIG_PLAN M2) is stubbed in
  v0.1 — `client.services.logs(unit)` returns the last 50 lines today.
