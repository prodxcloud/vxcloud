# vxcloud · Python SDK

[![PyPI version](https://img.shields.io/pypi/v/vxcloud.svg)](https://pypi.org/project/vxcloud/)
[![Python versions](https://img.shields.io/pypi/pyversions/vxcloud.svg)](https://pypi.org/project/vxcloud/)
[![License](https://img.shields.io/pypi/l/vxcloud.svg)](https://www.apache.org/licenses/LICENSE-2.0)
[![Downloads](https://static.pepy.tech/badge/vxcloud/month)](https://pepy.tech/project/vxcloud)
[![Wheel](https://img.shields.io/pypi/wheel/vxcloud.svg)](https://pypi.org/project/vxcloud/#files)

**Provision infrastructure, deploy applications, and manage running services on the [vxcloud](https://vxcloud.io) platform — straight from Python.**

`vxcloud` is the official, brand-name distribution of the vxcloud Python SDK. It re-exports the entire [`vxsdk`](https://pypi.org/project/vxsdk/) surface, so `import vxcloud` and `import vxsdk` are byte-for-byte identical — pick the name your team prefers. The sync client is **stdlib-only** (zero third-party dependencies); an optional async client is one extra away.

[Installation](#installation) · [Quick start](#quick-start) · [What you can do](#what-you-can-do) · [Async](#async-flavor) · [Errors](#error-handling) · [Docs](https://vxcloud.io/docs/sdks)

---

## Installation

```bash
pip install vxcloud            # sync client — stdlib only, zero dependencies
pip install vxcloud[async]     # adds httpx for the async client
```

Requires **Python 3.9+**. Tested on CPython 3.9 – 3.12.

## Quick start

```python
import vxcloud

# Reads ~/.vxcloud/credentials.json (written by `vxcli auth login`)
c = vxcloud.Client.load_from_vxcli()

# ...or pass credentials explicitly
# c = vxcloud.Client(api_key="xc_dev_...", username="alice")

# Provision a VM on AWS
vm = c.cloud.vm.provision(
    name="api-vm", cloud="aws", region="us-east-1",
    instance_type="t3.small", key_pair_name="AWSPRODKEY2",
)
print(vm["public_ip"])

# Deploy a Docker container onto it
result = c.deploy.container(
    host=vm["public_ip"], ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    image="grafana/grafana:latest", name="grafana",
    ports=["3000:3000"], restart_policy="unless-stopped",
)
print(result["session_id"], result.get("status"))
```

### Pick the entry-point name you like

All four resolve to the **same** client class — there is no behavior difference:

```python
import vxcloud

c = vxcloud.Client.load_from_vxcli()      # canonical
c = vxcloud.VxCloud.load_from_vxcli()     # PascalCase brand (matches the TS SDK)
c = vxcloud.vxcloud.load_from_vxcli()     # lowercase brand
c = vxcloud.load_from_vxcli()             # module-level convenience
```

## What you can do

`vxcloud` is a thin, typed wrapper over the vxcloud FastAPI control plane. The
same JSON wire contract powers the Go and TypeScript SDKs.

| Area | Example | Backend |
|---|---|---|
| **Compute** | `c.cloud.vm.provision(...)`, `c.cloud.vm.status(...)`, `c.cloud.vm.action(...)` | `/api/v2/tenant/provision/vm` |
| **Containers** | `c.deploy.container(...)`, `c.install.compose(...)` | `/api/v2/tenant/container/deploy` |
| **App stacks** | `c.deploy.stack("golang", repo_url=..., ...)`, `c.deploy.fastapi(...)` | `/api/v2/infrastructure/services/<kind>/deploy` |
| **Storage & IAM** | `c.cloud.create_s3_bucket(...)`, `c.cloud.create_iam_policy(...)` | `/api/v2/tenant/provision/{storage,security}` |
| **Networking** | `c.cloud.create_vpc(...)` | `/api/v2/tenant/provision/networks` |
| **Kubernetes** | `c.cloud.create_kubernetes_cluster(...)`, `c.cloud.list_kubernetes_clusters()` | `/api/v2/tenant/provision/kubernetes` |
| **Serverless** | `c.cloud.create_serverless_function(...)` | `/api/v2/tenant/provision/serverless` |
| **CI/CD** | `c.cicd.pipelines.list()`, `c.cicd.pipelines.trigger(...)` | `/api/v2/cicd/...` |
| **Marketplace** | `c.marketplace.agents.deploy(...)`, `c.marketplace.models.list()` | `/api/v2/marketplace/...` |
| **AI agents** | `c.agentcontrol.*`, `c.vxcomputer.run(...)` | `/api/v2/{agentcontrol,vxcomputer}/...` |
| **Workflows** | `c.workflow.create(...)`, `c.workflow.execute(...)`, `c.vxchrono.launch_run(...)` | `/api/v2/{workflow,vxchrono}/...` |
| **Custom scripts** | `c.install.script(host=..., script="#!/bin/bash\n...")` | `/api/v2/tenant/install/script` |

```python
# Deploy a language stack straight from a public git repo
c.deploy.stack(
    "golang",
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    repo_url="https://github.com/joelwembo/va-sample-golang.git", branch="main",
    git_provider="github", app_name="va-sample-golang",
    http_port="80", app_port="8080", go_version="1.22",
)

# Trigger a CI/CD pipeline
for p in c.cicd.pipelines.list():
    print(p["id"], p["name"])
build = c.cicd.pipelines.trigger(pipeline_id="abc...", branch="main")

# Deploy a marketplace agent
c.marketplace.agents.deploy(
    "golang_url_status_agent",
    host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
    http_port="8094",
)
```

## Async flavor

Install the extra and switch `Client` → `AsyncClient`. Same classes, same
method signatures — just add `async`/`await`. Ideal for FastAPI/aiohttp
services and concurrent fan-out (multi-host deploys, batch installs).

```python
import asyncio
import vxcloud_async as vx

async def main():
    async with await vx.AsyncClient.load_from_vxcli() as c:
        # Three deploys in parallel — ~2.5× faster than sequential
        await asyncio.gather(
            c.deploy.container(host=h1, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r1"),
            c.deploy.container(host=h2, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r2"),
            c.deploy.container(host=h3, ssh_user="ubuntu", key_pair_name=K, image="redis:7", ports=["6381:6379"], name="r3"),
        )

asyncio.run(main())
```

## Error handling

```python
try:
    c.cicd.pipelines.list()
except vxcloud.VxAuthError:        # 401 / 403
    ...
except vxcloud.VxValidationError:  # 400 / 422
    ...
except vxcloud.VxRateLimitError as e:   # 429 — inspect e.retry_after
    ...
except vxcloud.VxNotFoundError:    # 404
    ...
except vxcloud.VxServerError:      # 5xx
    ...
except vxcloud.VxNetworkError:     # transport-level
    ...
except vxcloud.VxError:            # base class — anything else
    ...
```

The client **automatically retries** transient failures (`VxNetworkError`,
`VxServerError`, `VxRateLimitError`) up to 3 times with exponential backoff,
and **transparently refreshes** an expired API key on `401` before replaying
the request — so application code rarely sees token expiration. Auth and
validation errors are surfaced immediately.

## vxcloud vs. vxsdk

| | |
|---|---|
| **Same code** | `vxcloud` re-exports every public name from `vxsdk` — `Client`, `VxCloud`, all `Vx*` errors, every resource class, and the module-level `load_from_vxcli()` helper. |
| **Versioning** | Each `vxcloud` release pins the exact matching `vxsdk` release, so the surface is deterministic at install time. |
| **Which to install** | Prefer the brand name? `pip install vxcloud`. Prefer the canonical name? `pip install vxsdk`. They are interchangeable. |

## Links

- 📦 PyPI: [pypi.org/project/vxcloud](https://pypi.org/project/vxcloud/) · [pypi.org/project/vxsdk](https://pypi.org/project/vxsdk/)
- 📖 Documentation: [vxcloud.io/docs/sdks](https://vxcloud.io/docs/sdks)
- 🛠️ Source & issues: [github.com/prodxcloud/vxcloud](https://github.com/prodxcloud/vxcloud)
- 📝 Changelog: [CHANGELOG.md](./CHANGELOG.md)

## License

Apache-2.0 © vxcloud / ProdXCloud
