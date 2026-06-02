<div align="center">

# 📦 vxcloud SDK

### Go · Python · TypeScript — one wire contract, three first-class SDKs

Provision multi-cloud infrastructure, deploy apps, run governed AI agents, and
drive the node control plane — from your code or CI.

[![npm](https://img.shields.io/npm/v/%40vxcloud%2Fsdk?logo=npm&label=%40vxcloud%2Fsdk&color=CB3837)](https://www.npmjs.com/package/@vxcloud/sdk)
[![PyPI](https://img.shields.io/pypi/v/vxsdk?logo=pypi&logoColor=white&label=vxsdk&color=3776AB)](https://pypi.org/project/vxsdk/)
[![Go Reference](https://pkg.go.dev/badge/github.com/prodxcloud/vxcloud.svg)](https://pkg.go.dev/github.com/prodxcloud/vxcloud)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](./LICENSE)

[🌐 Website](https://vxcloud.io) · [📚 Docs](https://vxcloud.io/docs/sdks) · [💻 CLI](https://vxcloud.io/download/cli) · [🐳 vxnode image](https://hub.docker.com/r/vxcloud/vxnode) · [⚙️ node tooling](https://github.com/prodxcloud/vxnode)

</div>

| Language | Install | Registry |
|---|---|---|
| **TypeScript / Node** | `npm install @vxcloud/sdk` | [![npm](https://img.shields.io/npm/v/%40vxcloud%2Fsdk?label=version&color=CB3837)](https://www.npmjs.com/package/@vxcloud/sdk) |
| **Python** | `pip install vxsdk` &nbsp;·&nbsp; `pip install vxcloud` | [![PyPI](https://img.shields.io/pypi/v/vxsdk?label=version&color=3776AB)](https://pypi.org/project/vxsdk/) · [vxcloud](https://pypi.org/project/vxcloud/) |
| **Go** | `go get github.com/prodxcloud/vxcloud` | [pkg.go.dev](https://pkg.go.dev/github.com/prodxcloud/vxcloud) |

> `vxcloud` (PyPI) is a brand alias that re-exports `vxsdk` — `import vxcloud` ≡ `import vxsdk`.
> Source: [github.com/prodxcloud/vxcloud](https://github.com/prodxcloud/vxcloud) · Docs: [vxcloud.io/docs/sdks](https://vxcloud.io/docs/sdks)
>
> **Status: preview (v0.1.0).** API may change before v1.0.

---

## Install

**TypeScript / Node** (≥ 18)
```bash
npm install @vxcloud/sdk
# pnpm add @vxcloud/sdk   |   yarn add @vxcloud/sdk
```

**Python** (≥ 3.9)
```bash
pip install vxsdk            # sync client, stdlib-only
pip install "vxsdk[async]"   # + async client (httpx)
# pip install vxcloud        # identical, brand-name alias
```

**Go** (≥ 1.22)
```bash
go get github.com/prodxcloud/vxcloud@latest
# pin the preview tag:
go get github.com/prodxcloud/vxcloud@v0.1.0-preview
```

---

## Quick start (30 seconds)

All three read `~/.vxcloud/credentials.json`, so if you've logged in with
[`vxcli`](https://vxcloud.io/download/cli) (`vxcli auth login -u <user> -k xc_live_…`)
the SDK is authenticated automatically. Otherwise pass an API key explicitly.

**TypeScript**
```ts
import { VxCloud } from '@vxcloud/sdk';

// from vxcli creds, or: new VxCloud({ apiKey: 'xc_live_…', username: 'alice' })
const c = await VxCloud.loadFromVxcli();

console.log(await c.cicd.pipelines.list());
console.log(await c.vxcomputer.run({ objective: 'Check disk usage', channel: 'cloudshell' }));
```

**Python**
```python
import vxsdk   # or: import vxcloud

c = vxsdk.Client.load_from_vxcli()          # or vxsdk.Client(api_key="xc_live_…", username="alice")

print(c.cicd.pipelines.list())
print(c.vxcomputer.run("Check disk usage", channel="cloudshell"))
```

**Go**
```go
package main

import (
    "context"
    "fmt"
    vxsdk "github.com/prodxcloud/vxcloud"
    "github.com/prodxcloud/vxcloud/vxcomputer"
)

func main() {
    ctx := context.Background()
    c, err := vxsdk.LoadFromVxcli(ctx) // or vxsdk.New(ctx, vxsdk.WithAPIKey("xc_live_…"))
    if err != nil { panic(err) }

    pipelines, _ := c.CICD().Pipelines().List(ctx)
    for _, p := range pipelines {
        fmt.Println(p.ID, p.Name, p.Repository)
    }

    res, _ := c.VxComputer().Run(ctx, vxcomputer.RunInput{
        Objective: "Check disk usage and report",
        Channel:   "cloudshell",
    })
    fmt.Println(res["status"])
}
```

---

## Authentication

Two interchangeable credentials, identical across all three SDKs:

- **API key** — `xc_live_*`, `xc_dev_*`, or `xc_test_*`. The SDK exchanges it for a
  JWT on first call and auto-refreshes on 401. Generate one at
  [app.vxcloud.io/developer/keys](https://app.vxcloud.io/developer/keys).
- **vxcli credentials** — `load_from_vxcli()` / `LoadFromVxcli()` reads
  `~/.vxcloud/credentials.json`, so anyone already signed in via `vxcli` is ready.

The SDK only ever sends `Authorization: Bearer <jwt>` and `X-API-Key: xc_*`. The
correct tenant node (`https://<your-node>`) is resolved automatically.

```ts
const c = new VxCloud({ apiKey: 'xc_live_…', username: 'alice' }); // explicit
```
```python
c = vxsdk.Client(api_key="xc_live_…", username="alice")
```
```go
c, _ := vxsdk.New(ctx, vxsdk.WithAPIKey("xc_live_…"))
```

---

## What you can do

Every SDK exposes the same resource modules off the client:

| Module | Go · Py · TS | Covers |
|---|:--:|---|
| `auth` | ✓ ✓ ✓ | API-key → JWT exchange, whoami, refresh-on-401 |
| `sessions` | ✓ ✓ ✓ | Deploy/install sessions — list, show, apply, pull, delete |
| `cicd` | ✓ ✓ ✓ | Pipelines, builds, Git provider connections |
| `install` | ✓ ✓ ✓ | Run remote install scripts / docker-compose stacks over SSH |
| `deploy` | ✓ ✓ ✓ | Container + language-stack deploys to a VM |
| `marketplace` | ✓ ✓ ✓ | Agents, models, Terraform solutions |
| `cloud` | ✓ ✓ ✓ | VM, S3, IAM, network, database, k8s, serverless provisioning |
| `nodes` | ✓ ✓ ✓ | Tenant-node management (control plane) |
| `services` | ✓ ✓ ✓ | Container/host lifecycle — start/stop/restart/remove |
| `networks` | ✓ ✓ ✓ | Network-diagnostic scripts (DNS, bandwidth, ports, audits) |
| `agents` | ✓ ✓ ✓ | AI agents — coding / devops / git / parallel / tools |
| `chat` | ✓ ✓ ✓ | Multi-provider AI chat |
| `observability` | ✓ ✓ ✓ | Backups, migrations, resource sync |
| `billing` | ✓ ✓ ✓ | Multicloud cost / optimization reports |
| `workspace` | ✓ ✓ ✓ | Workspace + credential storage (incl. SSL certs) |
| `metaldb` | ✓ ✓ — | Self-managed PostgreSQL over SSH |
| `agentcontrol` | ✓ ✓ — | Fine-tuning, training, knowledge bases, datasets |
| `vxcomputer` | ✓ ✓ ✓ | Node-local policy-governed agent runtime (Plan→Act→Reflect) |
| `workflow` | ✓ ✓ ✓ | Visual DAG workflow orchestration |
| `vxchrono` | ✓ ✓ ✓ | Autonomous goal executor & scheduler |
| `robotic` | ✓ ✓ ✓ | Robotic control cloud (robots, fleet, telemetry) |

`metaldb` and `agentcontrol` are Go + Python only for now. Python also ships an
async client (`import vxsdk_async` → `AsyncClient`) covering the same modules.

### A few real calls (Go)
```go
// trigger a build
build, _ := c.CICD().Pipelines().Trigger(ctx, pipelineID, "main")

// deploy a Docker container over SSH
c.Deploy().Container(ctx, deploy.ContainerOpts{
    SSH:   deploy.SSH{Host: ip, User: "ubuntu", KeyPairName: keyName},
    Image: "grafana/grafana:latest", Name: "grafana", Ports: []string{"3000:3000"},
})

// deploy a language stack from a git repo
c.Deploy().Stack(ctx, deploy.StackGolang, deploy.StackOpts{
    SSH: deploy.SSH{Host: ip, User: "ubuntu", KeyPairName: keyName},
    RepoURL: "https://github.com/youruser/yourapp.git", Branch: "main",
    AppName: "yourapp", HTTPPort: "80", GoVersion: "1.22",
})
```
Python and TypeScript expose the same operations with idiomatic naming
(`c.deploy.container(...)`, `await c.deploy.stack(...)`).

---

## Design contracts (stable across versions)

| Contract | Promise |
|---|---|
| **Auth headers** | Only `Authorization: Bearer` and `X-API-Key`. No custom headers, no query-param tokens. |
| **Errors** | Typed error hierarchy per language; categories may be added, never removed. |
| **Retries** | Network / 5xx / rate-limit retried with exponential backoff (default 3). Others surface immediately. |
| **Wire format** | `snake_case` JSON matching the API verbatim; idiomatic identifiers per language. |
| **Versioning** | Tagged `v0.1.0-preview`; v1.0 will guarantee stability across minor releases. |

---

## Releasing a new version (maintainers)

This repo (`github.com/prodxcloud/vxcloud`) is the SDK's home. Each language
publishes independently:

```bash
# Go — tagging IS the release (no registry)
git tag v0.2.0 && git push origin v0.2.0          # then: go get github.com/prodxcloud/vxcloud@v0.2.0

# TypeScript → npm
cd typescript && npm version 0.2.0 && npm publish --access public

# Python → PyPI (vxsdk first, then the vxcloud alias)
cd python         && python -m build && twine upload dist/*
cd ../python-vxcloud && python -m build && twine upload dist/*
```
Keep the three versions in lock-step. npm publishing requires a 2FA-bypass
(automation/granular) token; PyPI uses an API token.

## License

Apache-2.0.
