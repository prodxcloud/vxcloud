# vxsdk-go — Changelog

All notable changes to the Go SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Pre-1.0 releases may break public API in any minor bump.

## [Unreleased]

## [2026.8.26]

Ships the SalesShift parity audit: every method below was checked against the
route that actually serves it, and the mismatches were real — some of them
silent. **This is also the first release to reach the registries since
`2026.8.14`**; `2026.8.17` was tagged but never published, so its Infinity →
VxCloud rename lands here for npm and PyPI users.

### Fixed — enrolments came back as an error even when they succeeded

`SequenceEnrollResult.Skipped` was declared `int`, but the route sends a
**list** of `{contact_id, reason}`. `encoding/json` failed with an
`UnmarshalTypeError` on **every** call — including fully successful
enrolments — which transport surfaces as `decode response`, so
`EnrollInSequence` returned `(nil, err)` and the contacts it had just enrolled
were invisible to the caller.

`SkippedDetails` / `SkippedCounts` were keys the route has never emitted. They
are replaced by the fields it does send: `Skipped []SkipDetail`, `Candidates`,
`Cap`, `SequenceStatus`, `Dispatching`, and the flat counters
`SkippedExisting` / `SkippedSuppressed` / `SkippedNoEmail` /
`SkippedUnsubscribed`. `SkipDetail.Email` is gone — the route sends
`contact_id` and `reason` only.

The same bug was fixed in the TypeScript SDK (`sequences.enroll`), where
`skipped` read as `0` for every call and no skip reason ever reached the
caller.

### Fixed — a stopped enrolment was indistinguishable from a running one

`Enrollment` carried three tags for keys `_enrollment_out` has never emitted —
`next_send_at`, plus `stopped_at` and `stop_reason` behind a `StoppedeAt`
typo — so they decoded as `""` on every call. The struct now follows the
route: `NextActionAt`, `LastStepAt`, `PausedAt`, `CompletedAt`, `LastError`,
`ContactCompany`, `VariantPicks`, `CreatedAt`.

New `EnrollmentFilter` (`Status`, `Q`, `ErrorOnly`, `Page`, `Limit`) exposes
the filters the route already accepted, including the ops view for
enrolments carrying a `last_error`.

### Fixed — `TestRun` never reported a sample run

`is_sample` is a **sibling** of `run`, not a field inside it. Reading it from
the run object left `IsSample` false even on a sample run.

### Fixed — `SendCampaign` returned a zero-valued struct on every send

`POST /campaigns/{id}/send` replies flat: there is no `data` envelope and no
`Campaign` in it. Unwrapping `data` into a `Campaign` produced an empty struct
for every call — no status, no counts, no way to tell a scheduled campaign
from one that had just mailed the entire list. It now returns
`*SendCampaignResult` (`Status`, `SendAt`, `Sent`, `Failed`, `Suppressed`).

A `send_at` in the past is refused by the server rather than falling through to
"now" — that fallthrough used to blast the whole audience immediately.

### Fixed — paging silently truncated at 50 rows

`ListOpportunities` discarded the pagination envelope, so a caller could not
tell a complete result from a truncated one. It now returns
`(opps, OpportunityPage, err)` with the server-stated `Total`, `Count`,
`HasMore` and `Limit` — `Total` is the pool under the current filters (6,608
open signals), `Count` is the page. `HasMore` comes from the server instead of
being inferred from `len(data) >= Limit`, an inference that is wrong at 499 of
500. `OpportunityFilter.Offset` reaches the rest.

`Opportunity.DescriptionTruncated` reports when the list route cut the body at
its 600-character budget — a cut body used to be indistinguishable from a whole
one.

`ListEmails` and `ListPosts` had no `limit` parameter at all, so every caller
silently got 50 rows with no way to ask for more. Both now take one, and the
`status` value is escaped rather than concatenated — a status containing a
space or `&` used to corrupt the query string. The routes declare
`ge=1,le=200`, so a limit above 200 is **rejected with 422, not clamped**.

### Fixed — the async Python client rejected `tenant_id` and `organization`

`AsyncClient.__init__` did not accept the two keywords the sync `Client` takes,
so any code that constructed the two the same way died with a `TypeError` on
the async path.

### Fixed — C++: ids went into the URL path unescaped

`leadsPoolPerson`, `leadsCompany` and `leadsGet` dropped their id straight into
the path, so a value like `abc/../../admin` could reshape the request URL. All
id-in-path getters now percent-encode the segment.

`CURLOPT_CONNECTTIMEOUT` (10s) is now set. Without it, an unreachable or
silently dropped host made every call block for the whole request timeout — on
a long-timeout write, effectively a hang.

### Added

- **TypeScript** — `billing.events(limit)` reads the workspace's own
  append-only billing trail, which answers for comped workspaces Stripe knows
  nothing about; `opportunities.convert(id)` creates contact + deal in one
  idempotent call; `workflows.runs(id, {limit})`; `WorkflowRun.stepsCount`
  (the list route returns a count, not step bodies, so reading `steps` off a
  list response always gave `[]`); `tasks.list({assigneeId})` and
  `opportunities.list({category})`.

### Changed — BREAKING (Go)

| Before | After |
|---|---|
| `ListEmails(ctx, status)` | `ListEmails(ctx, status, limit)` |
| `ListPosts(ctx, status)` | `ListPosts(ctx, status, limit)` |
| `ListOpportunities(…) (opps, []SourceFacet, err)` | `(opps, OpportunityPage, err)` — facets moved to `page.Sources` |
| `SendCampaign(…) (*Campaign, error)` | `(*SendCampaignResult, error)` |
| `SequenceEnrollResult.Skipped int` | `Skipped []SkipDetail` |

`SendContactEmail`'s doc comment was wrong in the other direction: a refused
send arrives as HTTP 400 `Cannot send: <reason>` and is returned as an **error**
(reasons: `no_email`, `unsubscribed`, `suppressed`). Only a delivery *failure*
comes back 200 with `Success` false. `SendEmailResult.ContactID` is populated
by `POST /email/send` only; the per-contact route omits it.

### Go module

```bash
go get github.com/prodxcloud/vxcloud@latest
go get github.com/prodxcloud/vxcloud@v0.20260826.0   # exact pin
```


## [2026.8.17]

### Changed — BREAKING: the Infinity environment variables were renamed

The control plane is called **VxCloud** everywhere now. The old `INFINITY_*`
names are **removed, not deprecated** — nothing reads them, so a stale config
falls back to defaults silently instead of erroring.

| Old (no longer read) | New |
|---|---|
| `VX_INFINITY_URL`, `INFINITY_URL` | `VXCLOUD_URL` (the pair collapses to one) |
| `INFINITY_API_URL` | `VXCLOUD_API_URL` |
| `INFINITY_WS_URL` | `VXCLOUD_WS_URL` |
| `INFINITY_SERVICE_TOKEN` | `VXCLOUD_SERVICE_TOKEN` |

The node pair `VX_NODE_URL` / `NODE_URL` is unchanged. Identifiers followed:
`InfinityURL` → `VxCloudURL`, `infinity_url` → `vxcloud_url`, and equivalents in
the TypeScript, Python, Java and C++ SDKs.

### Fixed — the Go module is installable again, at `v0.20260817.0`

```bash
go get github.com/prodxcloud/vxcloud@latest
go get github.com/prodxcloud/vxcloud@v0.20260817.0   # exact pin
```

The date lives in the **minor** field as `YYYYMMDD`, so the Go tag carries the
release number even though it cannot *be* the release number:

| Release | Go tag |
|---|---|
| `2026.8.17` | `v0.20260817.0` |
| `2026.12.25` | `v0.20261225.0` |
| same-day hotfix | `v0.20260817.1` |

`minor = YYYY*10000 + MM*100 + DD` is a fixed-width positional encoding, so it
is injective and strictly increasing for every date in any year — no collisions,
no inversions at month or year boundaries — and the patch field leaves room for
same-day hotfixes. The year is ≥ 1000, so the leading digit is never zero and
the semver no-leading-zeros rule is never at risk.

`v0.2.0` — a plain semver tag briefly published for this same release — is
**retracted**, not deleted. `sum.golang.org` has already recorded it
permanently; deleting a published tag breaks anyone who pinned it instead of
helping them.

The previously documented `go get github.com/prodxcloud/vxcloud@2026.8.14`
never resolved; `@latest` silently fell back to `v0.1.0-preview`, so Go users
have been building against April's API while every other SDK moved on.

**The Go module line is semver and deliberately does not match the CalVer
release number the other five SDKs share.** Adding a `v` prefix does not fix a
CalVer tag — Go parses `v2026.8.17` as *major version 2026* and rejects it
unless the module path ends in `/v2026`:

```
invalid version: module contains a go.mod file, so module path
must match major version ("github.com/prodxcloud/vxcloud/v2026")
```

So CalVer cannot be a Go module version at all. `v2026.8.17` is still pushed as
the cross-language release marker (matching the `2026.8.14` / `2026.8.13` tags),
but the proxy ignores it entirely and Go resolution runs off the encoded line
above: `v0.1.0-preview` → `v0.20260817.0`.

## [2026.8.14]

### Changed — documentation

Same code as `2026.8.13`; this release exists to carry the docs, because a
registry cannot rewrite the landing page of a version already published.

- Every package README now documents the **SalesShift** surface with runnable
  examples: prospect pool (search / quota / preview-cost / reveal), pool → lead
  → contact conversion, tracked email and campaigns, opportunity signals, tasks,
  social distribution, and SalesShift billing — plus the matching
  `vxcli salesshift …` commands for each.
- `vxsdk`'s PyPI landing page was a repo-internal file listing; it is now a
  proper landing page matching `vxcloud`'s.
- READMEs carry a cross-language install table (Python, TypeScript, Go, C++,
  Java, CLI), a Links block, and author attribution.

## [2026.8.13]

Released across every language at once: Go, Python (sync + async), TypeScript,
and — new in this release — C++ and Java. `Version` in `client.go` moves off
`0.1.0-preview` onto the same CalVer number the other SDKs already carried, so
`vxsdk-go/<version>` in the User-Agent finally matches the tag.

### Added — C++ SDK (`cpp/`)

A header + single translation unit (`include/vxsdk/vxsdk.hpp`, `src/vxsdk.cpp`)
built on libcurl, C++17, no other dependency. `VxClient` covers auth, agents,
chat and AgentControl; errors surface as `VxException` rather than return codes.
Build with CMake (`project(vxsdk_cpp VERSION 2026.8.13)`) or drop the two files
into an existing tree. `examples/example.cpp` and `examples/ac_test.cpp` compile
against a live workspace.

### Added — Java SDK (`java/`)

`io.vxcloud.sdk` — `VxClient` (builder-configured), `VxException`, and a
zero-dependency `Json` codec, so the artifact pulls in nothing but the JDK
(11+, `java.net.http`). Maven coordinates in `java/pom.xml` at `2026.8.13`.
Chosen over Jackson/Gson deliberately: an SDK that forces a JSON library on a
caller who already has one is a dependency conflict waiting to happen.

### Added — Leads / prospect pool (Python, TypeScript)

The cross-tenant pool of people and companies, and the saved-lead lifecycle on
top of it. Search (`search_leads` / `search_all_leads` with auto-pagination),
facets, pool detail lookups, reveal with quota accounting
(`reveal_quota` / `preview_reveal_cost` / `reveal_lead`), save, update, convert
(single, bulk, and straight from the pool), company enrichment, saved searches,
and GDPR erasure requests.

- A revealed email costs quota, so `preview_reveal_cost` exists to price a
  batch **before** spending it. `VxLeadQuotaExhaustedError` /
  `VxLeadErasedError` are distinct exception types because the caller's
  response differs: back off and retry later vs. never ask again.
- `describe_convert` renders every bucket a convert splits into
  (converted / skipped / failed) — a partial success reported as success is
  how duplicate contacts get created.
- TypeScript mirrors this in `src/leads.ts` with `LEADS_MAX_BATCH`,
  `LEADS_MAX_PAGE_SIZE`, `LEADS_MAX_LIST_LIMIT` and `LEADS_TOTAL_CAP` exported,
  so callers can chunk correctly instead of discovering the server's ceilings
  through 4xx responses.

### Added — Sandboxes (Python)

`Sandboxes` — `create` / `list` / `get` / `delete` / `extend` / `wait_ready`
for the ephemeral podman-container dev environments. `extend` exists because
sandboxes carry a TTL and expire out from under a long job; `wait_ready` polls
so callers do not hand-roll a sleep loop against a container that is still
pulling its image.

### Added — SalesShift parity in Python (sync + async)

Everything the Go and TypeScript SDKs gained below now has a Python equivalent
on the `SalesShift` resource: billing (plans / subscription / invoices /
checkout), social (channels / posts / distribute / webmaster inspect),
opportunities (list / get / post / apply / save / dismiss / push-to-lead),
tasks (list / create / update / complete / delete) and campaigns (list /
create / get / send / `wait_for_campaign`). `vxsdk_async` carries the async
flavor of the same surface.

### Added — TypeScript module exports

`Transport` and its types are now exported from `index.ts`. Previously the
resource classes could be imported but never named in a typed signature — every
constructor takes a `Transport` — and there was no escape hatch for an endpoint
the SDK does not wrap yet. It is also the seam for stubbing in tests
(`new Leads(fakeTransport)`).

`SalesShiftContacts` / `SalesShiftWorkflows` / `SalesShiftSequences` ship in
`src/salesshift-crm.ts`. Note the rename on the way out: `Workflow` is exported
as `SalesShiftWorkflow`, because `Workflow` is already taken by the VxCloud
workflow engine, which is a different product entirely.

### Added — SalesShift platform surface: billing, social, signals, tasks, campaigns (2026-08-12)

The SalesShift API grew five surfaces that neither the SDK nor `vxcli` could
reach. Both now cover all of them, in Go and TypeScript, with matching `vxcli`
commands. Previously the SDK exposed exactly four SalesShift methods
(`SendEmail`, `ListEmails`, `GetStats`, `GetWorkerHealth`) — everything below
was unreachable except by hand-rolling HTTP.

**Go — `salesshift/billing.go`** (what the workspace pays US, not what its own
customers pay it):
- `ListPlans`, `GetSubscription`, `ListInvoices`, `ListBillingEvents`
- `StartCheckout`, `ConfirmCheckout`, `BillingPortal`
- `ChangeSubscription`, `CancelSubscription`, `ResumeSubscription`
- `PlanQuotas` uses `*int` — **nil means unlimited**. A plain `int` would render
  as 0, which reads as "no allowance", the opposite of what the API means.

**Go — `salesshift/social.go`**:
- `ListChannels`, `ListPosts`, `CreatePost`, `DeletePost`, `DistributePost`,
  `GetSocialStats`
- `DistributeJob` carries `WallMs` / `SequentialMs` / `Speedup` — the fan-out
  runs one goroutine per network in the `vxsocial` service, and the speedup is
  a measurement rather than a claim.
- Every `SocialDelivery` carries `Simulated`. Callers **must** surface it: this
  deployment holds no social API credentials, and reporting a simulated post as
  published is the one unforgivable lie this SDK could tell.
- Webmaster: `InspectURL`, `CheckRobots`, `CheckSitemap`, `GenerateWebmasterFiles`.

**Go — `salesshift/opportunities.go`** (the cross-tenant signal pool):
- `ListOpportunities` (source / signal_type / min_score / saved_only filters),
  `GetOpportunity`, `SaveOpportunity`, `DismissOpportunity`, `PushToLead`,
  `ConvertOpportunity`
- Save and dismiss are per-organization: the rows are shared by every tenant,
  so those PATCHes change side-table state, never the signal itself.

**Go — `salesshift/tasks.go`**:
- `ListTasks` / `CreateTask` / `UpdateTask` / `DeleteTask` — now including
  `Goal`, `Progress` and `Assignee*`, which the API gained and the SDK never had.
- `ListCampaigns`, `GetCampaign` (recipients + hourly timeline), `SendCampaign`
- `GetRevealQuota` — `Unlimited` is a first-class field; `Allowance` is -1 when
  uncapped and `Remaining` stays a large finite number so callers can keep
  doing integer comparisons.

**TypeScript — `src/salesshift-platform.ts`**: `SalesShiftBilling`,
`SalesShiftSocial`, `SalesShiftOpportunities`, `SalesShiftTasks`,
`SalesShiftCampaigns`, exported from `index.ts`. Same snake_case ↔ camelCase
mapping at the boundary as every other module.

**vxcli — `cmd/salesshift_platform.go`**: `billing`, `social`, `webmaster`,
`opportunities`, `tasks` and `campaigns` command trees under `vxcli salesshift`.
All honour `--output json|yaml`. Destructive or spending commands
(`billing cancel`, `campaigns send`) confirm first and take `--yes`.

Verified live against `api.vxcloud.io` — billing plans/subscription/invoices/
events, social channels/stats/post+distribute (3.4x measured across 6 channels),
opportunities list with real scraped signals, tasks list/add, campaign report,
and webmaster inspect/robots.


### Added — Workspace surface backfill + 3 new credential entities (2026-05-29)

Cross-SDK additions covering every language (Go / Python sync + async / TypeScript)
and `vxcli`. All four SDKs and the CLI now expose the same Workspace surface.

**New entities — backed by `vxnode/services/workspace/workspace.go`:**
- `storeDockerRegistry / getAllDockerRegistries / getDockerRegistry / deleteDockerRegistry`
  — Docker registry endpoint (ECR / GCR / ACR / GHCR / GitLab / Quay / Harbor / JFrog / custom)
  distinct from credentials, stored at `docker/registry-endpoints/<slug>`. May reference a saved
  Docker credential by slug via `default_credential_slug`.
- `storeRandomCredential / getAllRandomCredentials / getRandomCredential / deleteRandomCredential`
  — free-form credential bucket at `random/credentials/<slug>`. Stores arbitrary `fields` map +
  opaque `json_blob` (useful for full GCP service-account JSON, GitHub-App JSON, FTP creds, license keys).
  Sensitive fields are masked on read.
- `storeServer / getAllServers / getServer / deleteServer` — developer host inventory at
  `servers/<slug>`. `name` + `ip_address` required; `hostname`, `port`, `description`,
  `keypair_name`, `keypair_location`, `tags` optional.

**Backfill of pre-existing server endpoints that were never exposed via SDK:**
- `storeDockerCredentials / getAllDockerCredentials / getDockerCredentialsByRegistry` — multi-registry
  Docker credentials at `docker/registries/<slug>`. Previously only reachable from `vxcli configure setup`
  and the dashboard.
- `storeVMCredentials / getAllVMCredentials / getVMCredentialsByKeypair` — VM keypairs at
  `vm/keypairs/<name>`. Previously only reachable from `vxcli configure setup` and the dashboard.
- `storeGitHubCredentials` — named GitHub PATs at `github/credentials/<name>`.

**Python async (`vxsdk_async.py`) — full Workspace class added.** The async client previously
had no `Workspace` resource at all; `AsyncClient.workspace` now exposes the complete surface
(35 + 19 = ~54 methods) at parity with `vxsdk.Workspace`.

**`vxcli` — 3 new wizard providers.** `vxcli configure setup` now exposes `docker-registry`,
`random`, and `server` alongside the existing providers (`configure_setup_new.go`).

**Dashboard UI (`vxcloud_web/app/pages/vaults/page.tsx`) — 3 new vault cards, 1 renamed.**
"Docker Registry" was renamed to "Docker Credentials" (no functional change — same backend
endpoint and storage path). New cards: "Docker Registry" (endpoint def), "Random Credentials",
"Servers List". TypeScript compile clean across the project.

PARITY.md `### workspace` table updated — total method count rose from Go 27 / Py 26 / TS 27 to
**Go 46 / Py 45 / TS 46**.

### Added — Go SDK parity with Python (metaldb + agentcontrol)
- `metaldb` package — self-managed PostgreSQL over SSH. `c.MetalDB().
  {TestConnection, Provision}`; `metaldb.DefaultProvisionInput()` mirrors
  the web dashboard's Metal DB wizard defaults. Mirrors `/api/v2/tenant/
  provision/metaldb*`.
- `agentcontrol` package — the AgentControl surface. `c.AgentControl()`
  exposes `FineTuning / Training / Knowledge` (`List/Get/Create/Wait`),
  `Datasets` (`List/Get/Preview/Upload`), `Agents` (`List/Execute`),
  `GitHub` (`ListRepos/ImportDataset`), and `Summary`. `Wait` polls a
  long-running job to a terminal status. Mirrors `/api/v2/agentcontrol/*`.
- `transport` — `JSONWithHeaders` and `MultipartWithHeaders` added so
  modules can send extra request headers (agentcontrol's `X-Tenant-ID`).
  Existing `JSON` / `Multipart` delegate to them — no behaviour change.
- Tenant id support — `WithTenantID(id)` option, `Client.TenantID()`
  accessor, `cred.File.TenantID` / `OrganizationID` fields; `LoadFromVxcli`
  populates the tenant id from `credentials.json` automatically.
- With these, the Go SDK reaches module-level parity with the Python SDK.

### Added — control-plane packages (vxcomputer / workflow / vxchrono / robotic)
- `vxcomputer` package — VXCOMPUTER node-local policy-governed agent
  runtime. `c.VxComputer().{Info, Health, Classify, Run, ResolveApproval,
  AuditVerify}`. Mirrors `vxcli vxcomputer` and `/api/v2/vxcomputer/*`.
- `workflow` package — n8n-style visual workflow engine. `c.Workflow().
  {List, Get, Create, Delete, Save, Publish, Validate, Execute, TestNode,
  ListExecutions, GetExecution, CancelExecution, DeleteExecution, Export,
  Health}`. Mirrors `vxcli workflow` and `/api/v2/workflow/*`.
- `vxchrono` package — autonomous goal executor & scheduler. `c.VxChrono().
  {Init, CreateGoal, ListGoals, GetGoal, UpdateGoal, DeleteGoal, Schedule,
  LaunchRun, GetRun, PauseRun, ResumeRun, StopRun, DispatchScheduler}`.
  Mirrors `vxcli vxchrono` and `/api/v2/vxchrono/*`.
- `robotic` package — robotic control cloud. `c.Robotic().{Info,
  ListRobots, GetRobot, RegisterRobot, DeleteRobot, SendCommand,
  CommandStatus, EmergencyStop, Telemetry, Plan, ResolveApproval,
  FleetCommand}`. Mirrors `vxcli robotic` and `/api/v2/robotic/*`.
- Parity: the same four modules were added to the Python SDK
  (`vxsdk.Workflow` + the existing `VxComputer`/`Robotic`/`VxChrono`,
  plus async equivalents in `vxsdk_async.py`) and the TypeScript SDK
  (`VxComputer`/`Robotic`/`VxChrono`/`Workflow`). The TS transport gained
  a `patchJSON` helper for `vxchrono.updateGoal`.

### Added — M3 + M4 (six new resource packages)
- `networks` package — script catalog list + remote-execute (delegates to
  install.script). `c.Networks().List(ctx) / RunRemote(ctx, opts)`.
- `agents` package — AI-agent surface mirroring `vxcli agent {coding,
  devops, git, parallel, presets, tool, tools}`. `c.Agents().Run/Coding/
  Devops/Git/Parallel/Presets/Tools/Tool`.
- `chat` package — multi-provider AI chat (Anthropic / OpenAI / Google
  / OpenClaw / Deepseek / Qwen / Groq / Mistral / Perplexity / Hugging
  Face / Ollama / Hermes / Cohere / Azure-OpenAI / Gemini / Llama).
  `c.Chat().Send/Quick`.
- `observability` package — `c.Observability().{Backups, Migrations, Sync}`:
  - Backups: `Create / List / Restore`
  - Migrations: `Plan / Execute`
  - Sync: `Batch` (resource discovery)
- `billing` package — `c.Billing().{Multicloud, Optimization}`.
- `workspace` package — full `/api/v2/setup/*` surface (35 endpoints):
  - workspace + organization lifecycle
  - cloud-provider creds (AWS / Azure / GCP)
  - AI-provider creds (16 providers)
  - API token lifecycle
  - Git / Gitlab / kubeconfig / OAuth / OKTA / CyberArk / external Vault
  - Payment / SMTP / messaging-bot / SSL-certificate creds
  - `DeleteCredential` by name
- `services` package — lifecycle plane mirroring `vxcli services`. New
  client accessor `c.Services()` exposes:
  - `Start / Stop / Remove / Restart` (container lifecycle, JSON endpoints)
  - `Status` (docker/container/status, JSON)
  - `List` (admin action `list_docker_containers`, multipart)
  - `Logs(unit)` (admin action `tail_logs`, multipart)
  - `c.Services().VM().{Reboot, Shutdown, DiskCleanup, DockerCleanup,
    RestartDocker, Memory, Disk, ListServices, ListContainers,
    KillPort, StopService}`
- Sessions deep CRUD scaffolding — `Show`, `Apply`, `Pull`, `Delete`
  surfaces planned (in flight; pending the live endpoints).

### Changed
- Module path documented as `github.com/vxcloud/vxsdk-go`. Pre-tag
  releases use `replace` directives in `examples/*/go.mod`.

## [0.1.0-preview] — 2026-04-29

Initial preview release. The SDK extracts the vxcloud wire contract
from `vxcli` so other Go services can talk to the platform without
rebuilding the request layer.

### Surface
- `vxsdk.New(ctx, opts...)` and `vxsdk.LoadFromVxcli(ctx)`.
- `transport` — single `*http.Client` per Client, retry/backoff, single-
  flight refresh on 401, multipart helpers.
- `auth` — APIKey validation, exchange against the VxCloud control plane.
- `errors` — typed Failure tree (`AuthError`, `ValidationError`,
  `RateLimitError`, `ServerError`, `NetworkError`, `NotFoundError`).
- Resource modules:
  - `sessions` — `List`
  - `cicd` — `Pipelines.List/Show/Trigger`, `Builds.Show`
  - `install` — `Script`, `Compose`
  - `deploy` — `Container`, `Stack(kind, opts)` for all 12 kinds
  - `marketplace` — `Agents/Models/Solutions.List/Show/Deploy/Provision`
  - `cloud` — `S3/IAM/VM/Network/Database/Kubernetes/Serverless`
  - `nodes` — `List/Default/SetDefault`
- `vxsdktest` — stub HTTP server for downstream tests.
