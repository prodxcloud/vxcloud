# vxsdk (Python, preview)

Single-file, stdlib-only Python port of [`../`](..) — the Go SDK.

Same wire contract, same auth model (API key + auto-refresh on 401),
same resource layout. Use this from Python scripts, Jupyter notebooks,
data pipelines, or any internal Python service that needs to talk to
the vxcloud platform.

```
services/sdk/python/
├── vxsdk.py           sync SDK — stdlib only, drop-in
├── vxsdk_async.py     async SDK — same API, requires httpx
├── deploy_app.py      runnable sync demo (container or stack)
├── deploy_async.py    runnable async demo (3 deploys in parallel)
└── README.md          this file
```

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

Async parity: `vxsdk_async.AsyncClient` exposes the same modules,
including `c.vxcomputer`, `c.workflow`, `c.vxchrono`, and `c.robotic`.

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
