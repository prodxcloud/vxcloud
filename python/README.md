# vxsdk · Python SDK

[![PyPI version](https://img.shields.io/pypi/v/vxsdk.svg)](https://pypi.org/project/vxsdk/)
[![Python versions](https://img.shields.io/pypi/pyversions/vxsdk.svg)](https://pypi.org/project/vxsdk/)
[![License](https://img.shields.io/pypi/l/vxsdk.svg)](https://www.apache.org/licenses/LICENSE-2.0)
[![Downloads](https://static.pepy.tech/badge/vxsdk/month)](https://pepy.tech/project/vxsdk)
[![Wheel](https://img.shields.io/pypi/wheel/vxsdk.svg)](https://pypi.org/project/vxsdk/#files)

**Provision infrastructure, deploy applications, run AI agents, and drive the SalesShift GTM stack on the [vxcloud](https://vxcloud.io) platform — straight from Python.**

`vxsdk` is the canonical Python distribution of the vxcloud SDK. Same wire
contract, same auth model (API key with automatic refresh on 401), and the same
resource layout as the [Go](https://github.com/prodxcloud/vxcloud),
[TypeScript](https://www.npmjs.com/package/@vxcloud/sdk), C++ and Java SDKs. The
sync client is **stdlib-only** — zero third-party dependencies; the async client
is one extra away.

Prefer the brand name? [`pip install vxcloud`](https://pypi.org/project/vxcloud/)
installs the identical surface under `import vxcloud`.

[Installation](#installation) · [Quick start](#quick-start) · [SalesShift](#salesshift--leads-crm-campaigns-and-signals) · [Resource map](#resource-map) · [Async](#two-flavors) · [Errors](#errors) · [Docs](https://vxcloud.io/docs/sdks)

---

## Two flavors

| Flavor | Module | Dependency | When to use |
|---|---|---|---|
| **Sync** | `vxsdk` | stdlib only | Scripts, notebooks, single-shot operations, simple migrations from `vxcli` |
| **Async** | `vxsdk_async` | `httpx` (`pip install httpx`) | FastAPI / aiohttp services, concurrent fan-out (multi-host deploys, batch installs), high-throughput integration |

Same class hierarchy, same method signatures, same auth model. Switching
is essentially `vxsdk.Client` → `vxsdk_async.AsyncClient` plus
`async`/`await`.

## Why a parallel Python implementation?

Python and Go don't share runtimes. The Go SDK is the right answer for
Go services and customers; this Python file is the right answer for
Python customers. Both are wrappers over the same FastAPI HTTP surface,
so the JSON wire is identical and they can be regenerated from a single
OpenAPI spec when the platform team enables one.

For now, both are hand-written and kept in sync by review.

## Installation

```bash
# PyPI (canonical name)
pip install vxsdk
pip install vxsdk[async]      # adds httpx for vxsdk_async

# PyPI (brand-name alias — same code, just `import vxcloud`)
pip install vxcloud
pip install vxcloud[async]

# Drop-in (no install, stdlib-only)
cp services/sdk/python/vxsdk.py /path/to/your/project/
```

Stdlib only for the sync flavor — no extra deps. Tested on Python 3.9+.

## Entry-point styles

All four below resolve to the same class object — pick the name you and
your team prefer. There is no behavior difference:

```python
import vxsdk

c = vxsdk.Client.load_from_vxcli()      # canonical
c = vxsdk.VxCloud.load_from_vxcli()     # PascalCase brand (matches TS SDK)
c = vxsdk.vxcloud.load_from_vxcli()     # lowercase brand

# Or via the alias package:
import vxcloud
c = vxcloud.Client.load_from_vxcli()
c = vxcloud.load_from_vxcli()            # module-level convenience
```

## Quick start

```python
import vxsdk

# Reads ~/.vxcloud/credentials.json (the file `vxcli auth login` writes)
c = vxsdk.Client.load_from_vxcli()

# Or with explicit credentials
# c = vxsdk.Client(api_key="xc_dev_...", username="alice")

# Provision a VM — `c.cloud.vm.provision(...)` mirrors the TypeScript SDK.
# (The legacy flat `c.cloud.create_vm(...)` also still works.)
vm = c.cloud.vm.provision(
    name="api-vm", cloud="aws", region="us-east-1",
    instance_type="t3.small", key_pair_name="AWSPRODKEY2",
)
print(vm["public_ip"])

# Deploy FastAPI onto the new VM
sess = c.deploy.fastapi(
    path="./", entry="app.app:app",
    requirements="requirements.txt",
    app_port=8000, http_port=80, app_name="studio-backend",
    host=vm["public_ip"], ssh_user="ubuntu",
    key_pair_name="AWSPRODKEY1.PEM",
)
print(sess["session_id"])

# Deploy a Docker container onto a VM
result = c.deploy.container(
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    image="grafana/grafana:latest",
    name="grafana", ports=["3000:3000"],
    restart_policy="unless-stopped",
)
print(result["session_id"], result.get("status"))

# Deploy a language stack from a public git repo
result = c.deploy.stack(
    "golang",
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    repo_url="https://github.com/joelwembo/va-sample-golang.git", branch="main",
    git_provider="github", app_name="va-sample-golang",
    http_port="80", app_port="8080", go_version="1.22",
)

# Run a custom shell script over SSH
result = c.install.script(
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    script="#!/bin/bash\necho hello\n",
    script_name="hello.sh",
)

# CI/CD
for p in c.cicd.pipelines.list():
    print(p["id"], p["name"], p.get("repository_url"))

build = c.cicd.pipelines.trigger(pipeline_id="abc...", branch="main")

# Marketplace
agents = c.marketplace.agents.list()
result = c.marketplace.agents.deploy(
    "golang_url_status_agent",
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    http_port="8094",
)

# Cloud provisioning (real AWS resources)
result = c.cloud.create_s3_bucket("my-bucket-001", region="us-east-1")
result = c.cloud.create_iam_policy("my-policy-001",
                                    policy_document={"Version": "2012-10-17", "Statement": [...]})
```

## SalesShift — leads, CRM, campaigns and signals

SalesShift is the go-to-market layer of the platform, and all of it hangs off
`c.salesshift`. It covers the global prospect pool, the CRM that pool feeds,
tracked email and campaigns, the cross-tenant opportunity signal pool, tasks,
social distribution, and the workspace's own billing.

### The prospect pool

Search returns **masked** addresses (`j•••@acme.com`). A mask is not an address —
revealing one spends quota, so price the batch before you spend it.

```python
import vxsdk

c = vxsdk.Client.load_from_vxcli()
ss = c.salesshift

# Search the global pool. Paging is keyset-based; the cursor is opaque —
# pass back exactly what the server gave you.
page = ss.search_leads(
    filters={"seniority": ["c_level", "vp"], "employee_range": ["51-200"],
             "country": ["AU"], "department": ["engineering"]},
    limit=50,
)
for p in page["results"]:
    print(p["full_name"], p["title"], p["email"])   # email is MASKED here

# Walk every page without hand-rolling the cursor loop
for person in ss.search_all_leads(filters={"country": ["AU"]}):
    ...

# Where does this org stand on reveal quota?
q = ss.reveal_quota()
print(q["remaining"], "of", q["allowance"], "unlimited:", q["unlimited"])

# Price a batch BEFORE spending anything on it
ids = [p["pool_person_id"] for p in page["results"][:10]]
est = ss.preview_reveal_cost(ids)
print("would cost", est["cost"], "already revealed:", est["already_revealed"])

# Reveal one. Nothing is charged if this raises VxLeadQuotaExhaustedError.
try:
    person = ss.reveal_lead(ids[0])
    print(person["email"])                          # real address
except vxsdk.VxLeadQuotaExhaustedError:
    print("allowance spent — you were NOT charged for this attempt")
except vxsdk.VxLeadErasedError:
    print("erased at the person's request — terminal, never retry")
```

### Pool → lead → contact

A pool row is not mailable. It becomes a lead when saved, and a mailable CRM
contact only when converted.

```python
ss.save_leads(ids)                       # pool → saved leads
leads = ss.list_leads(status="new", limit=100)

report = ss.convert_from_pool(ids, lifecycle_stage="lead")

# A convert splits into buckets. Reporting a partial success as success is
# how duplicate contacts get created — render every bucket.
print(vxsdk.describe_convert(report))

ss.request_erasure(email="someone@example.com")     # GDPR, global + terminal
ss.enrich_company(domain="acme.com")                # crawl + fill in firmographics
```

### Tracked email and campaigns

```python
ss.send_email(
    to_email="ada@acme.com",
    subject="Quick question about your deploy pipeline",
    body_html="<p>Hi Ada — noticed you are hiring platform engineers…</p>",
)

for e in ss.list_emails(status="delivered"):
    print(e["to_email"], e["opened_at"], e["clicked_at"])

print(ss.get_stats())                    # sends, opens, clicks, bounces
print(ss.get_worker_health())            # the Go email worker behind it

cid = ss.create_campaign("AU founders Q3", "Subject line", "<p>Body</p>")["id"]
ss.send_campaign(cid)
report = ss.wait_for_campaign(cid, timeout=120)
```

### Opportunities, tasks and social

```python
# Opportunities are a cross-tenant signal pool — save/dismiss are per-org
# side-table state, never a mutation of the shared signal.
opps = ss.list_opportunities(source="hn", min_score=70, saved_only=False)
ss.save_opportunity(opps["results"][0]["id"])
ss.push_opportunity_to_lead(opps["results"][0]["id"])

ss.create_task("Follow up with Ada", goal="Book a 20-min call", priority="high")
for t in ss.list_tasks(status="open")["results"]:
    print(t["title"], t["progress"], t["assignee_name"])

post = ss.create_social_post("Shipping vxcli 2026.8.13 today.", title="Release")
job = ss.distribute_post(post["id"])

# Fan-out is one goroutine per network. `speedup` is measured, not claimed.
print(job["job"]["speedup"], "x faster than sequential")

# A deployment without social API credentials still returns delivery records.
# Surface `simulated` — reporting a simulated post as published is the one
# unforgivable lie this SDK could tell.
for d in job["job"]["deliveries"]:
    print(d["channel"], "SIMULATED" if d["simulated"] else "published")
```

### Billing (what the workspace pays for SalesShift)

```python
plans = ss.billing_plans()
sub   = ss.billing_subscription()

# Quota fields are None when UNLIMITED. A plain 0 would read as "no
# allowance" — the exact opposite of what the API means.
for code, limit in sub["plan"]["quotas"].items():
    print(code, "unlimited" if limit is None else limit)

print(ss.billing_checkout("growth", seats=5)["url"])
for inv in ss.billing_invoices()["invoices"]:
    print(inv["number"], inv["amount_due"], inv["status"])
```

### The same surface from `vxcli`

Every call above has a CLI equivalent. All honour `--output json|yaml`, and
spending or destructive commands confirm first and take `--yes`.

```bash
vxcli salesshift leads search --seniority c_level --country AU --limit 25
vxcli salesshift leads quota
vxcli salesshift leads reveal <pool-id>
vxcli salesshift leads save <pool-id>…
vxcli salesshift leads convert-from-pool <pool-id>… --lifecycle-stage lead
vxcli salesshift leads enrich acme.com

vxcli salesshift email send --to ada@acme.com --subject "…" --html "<p>…</p>"
vxcli salesshift email stats
vxcli salesshift campaigns list
vxcli salesshift campaigns report <campaign-id>

vxcli salesshift contacts list
vxcli salesshift workflows test-run <id>
vxcli salesshift sequences list

vxcli salesshift opportunities list --source hn --min-score 70
vxcli salesshift opportunities push-to-lead <id>
vxcli salesshift tasks add --title "Follow up" --goal "Book a call"
vxcli salesshift social post --content "…" && vxcli salesshift social send <post-id>
vxcli salesshift webmaster inspect https://example.com

vxcli salesshift billing plans
vxcli salesshift billing invoices
vxcli salesshift billing cancel --yes
```

## Resource map

| Path | Method | Backend endpoint |
|---|---|---|
| `c.cicd.pipelines.list/show/trigger` | GET/GET/POST | `/api/v2/cicd/pipelines/...` |
| `c.cicd.builds.show` | GET | `/api/v2/cicd/builds/{id}` |
| `c.sessions.list` | GET | `/api/v3/sessions/list` |
| `c.install.script` | POST multipart | `/api/v2/tenant/install/script` |
| `c.install.compose` | POST multipart | `/api/v2/tenant/provision/docker-compose/custom` |
| `c.deploy.container` | POST multipart | `/api/v2/tenant/container/deploy` |
| `c.deploy.stack(kind)` | POST multipart | `/api/v2/infrastructure/services/<kind>/deploy` |
| `c.marketplace.agents.list/show/deploy` | GET/GET/POST | `/api/v2/marketplace/agents/...` |
| `c.marketplace.models.list/show` | GET/GET | `/api/v2/marketplace/models/...` |
| `c.marketplace.solutions.list/show/provision` | GET/GET/POST | `/api/v2/marketplace/templates`, `/provision` |
| `c.cloud.create_s3_bucket` | POST | `/api/v2/tenant/provision/storage` |
| `c.cloud.create_iam_policy/role/keypair` | POST | `/api/v2/tenant/provision/security` |
| `c.cloud.create_vm` *(legacy)* | POST | `/api/v2/tenant/provision/vm` |
| `c.cloud.vm.provision/status/action` | POST | `/api/v2/tenant/provision/vm`, `/provision/vm/{status,action}` |
| `c.cloud.create_vpc` | POST | `/api/v2/tenant/provision/networks` |
| `c.cloud.create_kubernetes_cluster` | POST | `/api/v2/tenant/provision/kubernetes` |
| `c.cloud.list_kubernetes_clusters` | GET | `/api/v2/tenant/kubernetes/clusters` |
| `c.cloud.kubernetes_cluster_details` | POST | `/api/v2/tenant/kubernetes/cluster/details` |
| `c.cloud.create_serverless_function` | POST | `/api/v2/tenant/provision/serverless` |
| `c.metaldb.test_connection/provision` | POST | `/api/v2/tenant/metaldb/...` |
| `c.nodes.list/default/set_default` | GET/POST | `/api/v1/auth/nodes/` (control plane) |
| `c.workspace.delete_workspace` | DELETE | `/api/v2/setup/workspace` |
| `c.marketplace.models.deploy` | POST | `/api/v2/marketplace/models/deploy` |
| `c.agentcontrol.{summary,fine_tuning,training,knowledge,datasets,agents,github}` | GET/POST | `/api/v2/agentcontrol/...` (X-Tenant-ID header) |
| `c.vxcomputer.info/run/classify/audit_verify` | GET/POST | `/api/v2/vxcomputer/...` |
| `c.workflow.list/create/validate/execute/export` | GET/POST | `/api/v2/workflow/...` |
| `c.vxchrono.create_goal/schedule/launch_run` | POST | `/api/v2/vxchrono/...` |
| `c.robotic.list_robots/register_robot/send_command` | GET/POST | `/api/v2/robotic/...` |
| `c.sandboxes.create/list/get/delete/extend/wait_ready` | POST/GET/DELETE | `/api/v2/sandboxes/...` |
| `c.salesshift.{search_leads,search_all_leads,lead_facets}` | POST | `/api/v1/salesshift/leads/search`, `/facets` |
| `c.salesshift.{reveal_quota,preview_reveal_cost,reveal_lead}` | GET/POST | `/api/v1/salesshift/leads/{quota,preview-cost,reveal}` |
| `c.salesshift.{save_leads,list_leads,update_lead,convert_lead,convert_from_pool}` | GET/POST/PATCH | `/api/v1/salesshift/leads/...` |
| `c.salesshift.{enrich_company,request_erasure,save_search}` | POST | `/api/v1/salesshift/leads/{enrich,erasure,searches}` |
| `c.salesshift.{send_email,list_emails,get_stats,get_worker_health}` | GET/POST | `/api/v1/salesshift/email/...` |
| `c.salesshift.{list_campaigns,create_campaign,send_campaign,wait_for_campaign}` | GET/POST | `/api/v1/salesshift/campaigns/...` |
| `c.salesshift.{list_opportunities,save_opportunity,push_opportunity_to_lead}` | GET/POST/PATCH | `/api/v1/salesshift/opportunities/...` |
| `c.salesshift.{list_tasks,create_task,update_task,complete_task,delete_task}` | GET/POST/PATCH/DELETE | `/api/v1/salesshift/tasks/...` |
| `c.salesshift.{social_channels,create_social_post,distribute_post}` | GET/POST | `/api/v1/salesshift/social/...` |
| `c.salesshift.{billing_plans,billing_subscription,billing_invoices,billing_checkout}` | GET/POST | `/api/v1/salesshift/billing/...` |

Async parity: `vxsdk_async.AsyncClient` exposes the same modules,
including `c.vxcomputer`, `c.workflow`, `c.vxchrono`, `c.robotic`, and the
full `c.salesshift` surface.

## Errors

```python
try:
    c.cicd.pipelines.list()
except vxsdk.VxAuthError as e:        # 401/403
    ...
except vxsdk.VxValidationError as e:  # 400/422
    ...
except vxsdk.VxRateLimitError as e:   # 429 — e.retry_after
    ...
except vxsdk.VxNotFoundError as e:    # 404
    ...
except vxsdk.VxServerError as e:      # 5xx
    ...
except vxsdk.VxNetworkError as e:     # transport
    ...
except vxsdk.VxError as e:            # base, anything else
    ...
```

The SDK retries `VxNetworkError`, `VxServerError`, and `VxRateLimitError`
up to 3 times with exponential backoff. Auth errors and validation errors
are surfaced immediately — retrying them as-is would not succeed.

On `401`, the SDK calls `POST /api/v1/auth/developer/keys/login` once
with the configured API key, replays the original request, and only
surfaces the error if the refresh itself fails. Application code should
not see token expiration.

## Run the sync deploy program

```bash
cd services/sdk/python
python3 deploy_app.py                                  # whoami container on inst3:8085
python3 deploy_app.py --image redis:7 --name r --ports 6380:6379
python3 deploy_app.py --mode stack --kind golang \
    --repo-url https://github.com/joelwembo/va-sample-golang.git
```

Default: deploys `traefik/whoami:latest` to inst3:8085 and polls until it
returns HTTP 200. See `python3 deploy_app.py --help` for flags.

## Run the async demo

```bash
pip install httpx
python3 deploy_async.py
```

Drops three redis containers onto a single host **in parallel** via
`asyncio.gather()`. Verified live: 3 deploys in 22.8s wall clock vs.
~57s sequential — a 2.5× speedup, and a 3× win at higher fan-out.

```python
import asyncio, vxsdk_async as vx

async def main():
    async with await vx.AsyncClient.load_from_vxcli() as c:
        results = await asyncio.gather(
            c.deploy.container(host=h1, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r1"),
            c.deploy.container(host=h2, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r2"),
            c.deploy.container(host=h3, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r3"),
        )

asyncio.run(main())
```

## SDKs for every stack

Same JSON wire contract, same auth model, same error taxonomy in every language.

| Language | Package | Install |
|---|---|---|
| Python | [`vxsdk`](https://pypi.org/project/vxsdk/) · [`vxcloud`](https://pypi.org/project/vxcloud/) | `pip install vxsdk` |
| TypeScript / Node | [`@vxcloud/sdk`](https://www.npmjs.com/package/@vxcloud/sdk) | `npm install @vxcloud/sdk` |
| Go | [`github.com/prodxcloud/vxcloud`](https://github.com/prodxcloud/vxcloud) | `go get github.com/prodxcloud/vxcloud` |
| C++ | [`cpp/`](https://github.com/prodxcloud/vxcloud/tree/main/cpp) | CMake or drop in two files (libcurl, C++17) |
| Java | [`java/`](https://github.com/prodxcloud/vxcloud/tree/main/java) | Maven, `io.vxcloud:vxsdk` (JDK 11+, zero deps) |
| CLI | `vxcli` | `curl -fsSL https://vxcloud.io/download/cli/install.sh \| sh` |

## Links

- 📦 PyPI: [pypi.org/project/vxcloud](https://pypi.org/project/vxcloud/) · [pypi.org/project/vxsdk](https://pypi.org/project/vxsdk/)
- 📖 Documentation: [vxcloud.io/docs/sdks](https://vxcloud.io/docs/sdks)
- 🛠️ Source & issues: [github.com/prodxcloud/vxcloud](https://github.com/prodxcloud/vxcloud)
- 📝 Changelog: [CHANGELOG.md](https://github.com/prodxcloud/vxcloud/blob/main/python/CHANGELOG.md)

## Author

Built and maintained by **Joel O. Wembo** — [linkedin.com/in/joelwembo](https://www.linkedin.com/in/joelwembo/)

## License

Apache-2.0 © vxcloud / ProdXCloud
