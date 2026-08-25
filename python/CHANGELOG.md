# vxsdk (Python) — Changelog

All notable changes to the Python SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning is **CalVer** (`YYYY.M.D`) to stay aligned with the vxnode fleet
release tags (e.g. `v2026.6.10-1`). The 0.1.x preview line predates this switch.

## [Unreleased]

## [2026.8.26]

First release to reach PyPI since `2026.8.14` — `2026.8.17` was tagged but
never published, so its **BREAKING** `INFINITY_*` → `VXCLOUD_*` environment
rename (documented below under 2026.8.17) lands here for `pip` users.

### Fixed — `AsyncClient` rejected `tenant_id=` and `organization=`

The sync `Client` accepts both; `AsyncClient.__init__` did not, so any code that
constructed the two the same way died with a `TypeError` on the async path.
`organization` falls back to `username` exactly as the sync client does.

### Changed

Doc and behaviour corrections carried across the SalesShift surface in this
release — see the [root CHANGELOG](../CHANGELOG.md#2026826) for the full audit
(enrolment decode failure, `SendCampaign` envelope, opportunity paging, the
missing `limit` on email/post listings).


## [2026.8.17]

### Changed — BREAKING: the Infinity environment variables were renamed

`INFINITY_*` is **removed, not deprecated** — nothing reads it, so a stale
config falls back to defaults silently instead of erroring.

| Old (no longer read) | New |
|---|---|
| `VX_INFINITY_URL`, `INFINITY_URL` | `VXCLOUD_URL` (the pair collapses to one) |
| `INFINITY_API_URL` | `VXCLOUD_API_URL` |
| `INFINITY_WS_URL` | `VXCLOUD_WS_URL` |

Identifiers followed: `infinity_url` → `vxcloud_url`. The node vars
`VX_NODE_URL` / `NODE_URL` are unchanged.

## [2026.8.14]

### Changed — documentation

Same code as `2026.8.13`. PyPI cannot rewrite the description of a published
version, so the docs ship as their own release.

- The landing page was a repo-internal file tree; it is now a real landing page
  with badges, a hero, and section nav — matching `vxcloud`'s.
- New **SalesShift** section with runnable Python covering the prospect pool
  (masked addresses, quota, `preview_reveal_cost` before spending), pool → lead
  → contact conversion via `describe_convert`, tracked email and campaigns,
  opportunity signals, tasks, social distribution (`simulated` deliveries), and
  SalesShift billing (`None` quota means unlimited) — plus the matching
  `vxcli salesshift …` commands.
- Resource map gains the `c.salesshift.*` and `c.sandboxes.*` rows.
- Cross-language install table, Links block, and author attribution.

## [2026.8.13]

### Added — Leads / prospect pool

The cross-tenant pool of people and companies, plus the saved-lead lifecycle
on top of it, on the `SalesShift` resource:

- Search — `search_leads`, `search_all_leads` (auto-paginates to the server's
  cap), `lead_facets`, `get_pool_person`, `get_pool_company`.
- Reveal — `reveal_quota`, `preview_reveal_cost`, `reveal_lead`. A revealed
  email costs quota, so pricing a batch **before** spending it is a first-class
  call, not something you infer from a failure.
- Lifecycle — `save_leads`, `list_leads`, `get_lead`, `update_lead`,
  `convert_lead`, `bulk_convert_leads`, `convert_from_pool`.
- `enrich_company`, `list_saved_searches`, `save_search`, `request_erasure`.

`VxLeadQuotaExhaustedError` (402) and `VxLeadErasedError` (410) are distinct
exception types because the caller's response differs: back off and retry later
vs. never ask again — and one of them means the customer was *not* charged.
Both are defined in `vxsdk` and re-exported by `vxsdk_async`, so the **same**
class is raised by either flavor; a single `except` clause covers both. Both
still subclass `VxError` and preserve `http_status`, so existing handlers keep
working.

`describe_convert()` renders every bucket a convert splits into (converted /
skipped / failed) — a partial success reported as success is how duplicate
contacts get created.

### Added — Sandboxes

`Sandboxes` — `create`, `list`, `get`, `delete`, `extend`, `wait_ready` for the
ephemeral podman-container dev environments. `extend` exists because sandboxes
carry a TTL and will expire out from under a long job; `wait_ready` polls so
callers do not hand-roll a sleep loop against a container still pulling its
image.

### Added — SalesShift platform surface

Reaching parity with the Go and TypeScript SDKs:

- Billing — `billing_plans`, `billing_subscription`, `billing_invoices`,
  `billing_checkout`.
- Social — `social_channels`, `social_posts`, `create_social_post`,
  `distribute_post`, `webmaster_inspect`. Every delivery carries `simulated`;
  surface it, because a deployment without social API credentials still returns
  a delivery record.
- Opportunities — `list_opportunities`, `get_opportunity`, `post_opportunity`,
  `apply_to_opportunity`, `save_opportunity`, `dismiss_opportunity`,
  `push_opportunity_to_lead`.
- Tasks — `list_tasks`, `create_task` (with `goal` / `progress` / assignee),
  `update_task`, `complete_task`, `delete_task`.
- Campaigns — `list_campaigns`, `create_campaign`, `get_campaign`,
  `send_campaign`, `wait_for_campaign`.
- Email + health — `send_email`, `list_emails`, `get_stats`,
  `get_worker_health`.

`vxsdk_async` carries the async flavor of the same surface via
`_AsyncSalesShift`.

### Changed

- `__version__` → `2026.8.13`, matching the package version and the Go,
  TypeScript, C++ and Java SDKs. It had drifted to `2026.6.10` while the
  package shipped as `2026.6.14`, which put a stale number in the User-Agent.

## [2026.6.10]

Adopt CalVer (`YYYY.M.D`) — the package version now tracks the vxnode fleet
release date so SDK, binary, and dashboard all read the same number. Bundles
the M1–M4 resource classes listed below (no breaking changes vs. 0.1.0).

### Added — M3 + M4 (six new resource classes)
- `vxsdk.Networks` — diagnostic-script catalog + remote run.
  `client.networks.list() / run_remote(script, host=, ssh_user=, ...)`.
- `vxsdk.Agents` — AI-agent surface mirroring `vxcli agent`.
  `client.agents.{coding, devops, git, parallel, presets, tools, tool, run}`.
- `vxsdk.Chat` — multi-provider AI chat (16 providers).
  `client.chat.send(provider=, model=, messages=) / quick(provider, model, q)`.
- `vxsdk.Observability` — `client.observability.{backups, migrations, sync}`:
  - `backups.create / list / restore`
  - `migrations.plan / execute`
  - `sync.batch`
- `vxsdk.Billing` — `client.billing.{multicloud(start_date=, end_date=),
  optimization(provider=)}`.
- `vxsdk.Workspace` — full `/api/v2/setup/*` surface (35 endpoints,
  26 helper methods): create_workspace, create_organization, store_aws_/
  azure_/gcp_credentials, create_api_token, get_api_token,
  store_ai_credentials(provider, ...), get_all_ai_credentials,
  store_git/gitlab/kubeconfig/oauth/okta/cyberark/payment/smtp/
  ssl_certificate/messaging_bot_credentials, get_vault_credentials,
  delete_credential, etc.

### Added — M1 + M2
- `vxsdk.Services` — lifecycle plane mirroring `vxcli services`: `list`,
  `status(name)`, `start(name)`, `stop(name)`, `restart(name)`,
  `remove(name)`, `logs(unit, tail)`, plus `Services.vm.reboot/shutdown/
  disk_cleanup/docker_cleanup/restart_docker/memory/disk/list_services/
  list_containers/kill_port/stop_service`. Reachable as `client.services`.
- `vxsdk.Sessions` deep CRUD — `show(id)`, `apply(id)`, `pull(id, out_dir)`,
  `delete(id, force)` in addition to the existing `list()`.
- `--key-pair-location` ergonomics: every method that takes SSH credentials
  now also accepts `key_pair_location=` (path to a local PEM). Read locally
  and attached as `private_key_pem` multipart part on the request.

### Changed
- Package now ships via PyPI (`pip install vxsdk`). Single-file drop-in
  use is still supported.

## [0.1.0] — 2026-04-30

Initial preview release. Hand-written port of `vxsdk-go`. Same wire
contract, same auth model (`API key → JWT, refresh on 401`), same error
taxonomy. Two flavors:

- `vxsdk` (sync) — stdlib only.
- `vxsdk_async` (async) — depends on `httpx>=0.25`. Install as
  `pip install vxsdk[async]`.

### Surface
- `Client.load_from_vxcli()` — read `~/.vxcloud/credentials.json`.
- `Client(api_key=, username=)` — explicit credentials.
- Resource modules: `cicd`, `sessions`, `install`, `deploy`,
  `marketplace`, `cloud`, `nodes`.
- `deploy.container(image, name, host, ssh_user, key_pair_name, ports,
  env, ...)` — deploy any Docker image to a remote VM.
- `deploy.stack(kind, source_dir, ...)` — bundle and deploy any of the
  12 supported stacks (fastapi, react, nextjs, django, nodejs, python,
  golang, rust, cpp, php, static).
- `install.script(path, args, ...)` and `install.compose(yaml_path, ...)`.
- `marketplace.agents/models/solutions.list/show/deploy/provision`.
- `nodes.list/default`.
- Errors: `VxAuthError`, `VxValidationError`, `VxNotFoundError`,
  `VxRateLimitError`, `VxServerError`, `VxNetworkError`.
