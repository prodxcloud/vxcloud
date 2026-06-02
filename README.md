# vxsdk — vxcloud / VxCloud SDK suite

Three first-class SDKs that share one wire contract:

| Language | Package | Location | Install |
|---|---|---|---|
| **Go** | `vxsdk-go` | this directory | `go get github.com/prodxcloud/vxcloud@latest` |
| **Python** | `vxsdk` | [`./python`](./python) | `pip install vxsdk` (or drop `vxsdk.py` in) |
| **TypeScript** | `@vxcloud/sdk` | [`./typescript`](./typescript) | `npm install @vxcloud/sdk` |

All three speak the same JSON wire contract, share the same auth model
(`X-API-Key` + `Bearer JWT` with refresh-on-401), resolve the same
per-tenant node, and expose the same resource modules. They are kept at
parity with each other and with the [`vxcli`](../cli) command surface.
**No platform-side change is required to use any of them.**

> **Status: preview.** API surface stable across minor versions only after
> 1.0. Roadmap, milestones, and risk register live in
> [`BIG_PLAN.md`](./BIG_PLAN.md) and [`CHANGELOG.md`](./CHANGELOG.md).

## Why this exists

`vxcli` already speaks the vxcloud API end-to-end, but every command
rebuilds its own `http.NewRequest`, has its own timeout, and re-encodes
auth headers inline. Other services (the gateway, future microservices,
customer integrations) end up reinventing the same client layer. The SDK
extracts that contract into one place per language:

- One HTTP client per `Client`, with retry/backoff and typed errors.
- One auth contract — `Authorization: Bearer <jwt>` and `X-API-Key: xc_*`,
  the same pair the FastAPI backend already accepts. **No backend change
  required.**
- Resource modules per domain (`sessions`, `cicd`, `vxcomputer`, …)
  modeled after the existing FastAPI / node routers.
- Reads `~/.vxcloud/credentials.json` so anyone already logged in via
  `vxcli` can use the SDK without re-supplying credentials.

## Quick start

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
    c, err := vxsdk.LoadFromVxcli(ctx) // or vxsdk.New(ctx, vxsdk.WithAPIKey(...))
    if err != nil { panic(err) }

    pipelines, err := c.CICD().Pipelines().List(ctx)
    if err != nil { panic(err) }
    for _, p := range pipelines {
        fmt.Println(p.ID, p.Name, p.Repository)
    }

    // Control plane: run a governed agent objective on the node.
    res, err := c.VxComputer().Run(ctx, vxcomputer.RunInput{
        Objective: "Check disk usage and report",
        Channel:   "cloudshell",
    })
    if err != nil { panic(err) }
    fmt.Println(res["status"])
}
```

**Python**

```python
import vxsdk

c = vxsdk.Client.load_from_vxcli()          # reads ~/.vxcloud/credentials.json
print(c.cicd.pipelines.list())

# Control plane
print(c.vxcomputer.run("Check disk usage and report", channel="cloudshell"))
print(c.workflow.list())
```

**TypeScript**

```ts
import { VxCloud } from '@vxcloud/sdk';

const c = await VxCloud.loadFromVxcli();
console.log(await c.cicd.pipelines.list());

// Control plane
console.log(await c.vxcomputer.run({ objective: 'Check disk usage', channel: 'cloudshell' }));
console.log(await c.workflow.list());
```

## Resource modules

Every SDK exposes the same resource modules off the client. The control
plane resources (`vxcomputer`, `workflow`, `vxchrono`, `robotic`) mirror
the [`vxcli`](../cli) commands of the same name 1:1.

| Module | Go | Python | TS | What it covers |
|---|:--:|:--:|:--:|---|
| `auth` | ✓ | ✓ | ✓ | API-key → JWT exchange, whoami, refresh-on-401 |
| `sessions` | ✓ | ✓ | ✓ | Deploy/install sessions — list, show, apply, pull, delete |
| `cicd` | ✓ | ✓ | ✓ | Pipelines, builds, Git provider connections |
| `install` | ✓ | ✓ | ✓ | Run remote install scripts / docker-compose stacks over SSH |
| `deploy` | ✓ | ✓ | ✓ | Container + language-stack deploys to a VM |
| `marketplace` | ✓ | ✓ | ✓ | Agents, models, Terraform solutions |
| `cloud` | ✓ | ✓ | ✓ | Cloud provisioning — VM, S3, IAM, network, database, k8s, serverless |
| `nodes` | ✓ | ✓ | ✓ | Tenant-node management (Infinity control plane) |
| `services` | ✓ | ✓ | ✓ | Container/host lifecycle — start/stop/restart/remove, host ops |
| `networks` | ✓ | ✓ | ✓ | Network-diagnostic scripts (DNS, bandwidth, ports, audits) |
| `agents` | ✓ | ✓ | ✓ | AI agents — coding / devops / git / parallel / presets / tools |
| `chat` | ✓ | ✓ | ✓ | Multi-provider AI chat |
| `observability` | ✓ | ✓ | ✓ | Backups, migrations, resource synchronization |
| `billing` | ✓ | ✓ | ✓ | Multicloud cost / optimization reports |
| `workspace` | ✓ | ✓ | ✓ | `/api/v2/setup/*` — workspace + credential storage (incl. SSL certs) |
| `metaldb` | ✓ | ✓ | — | Self-managed PostgreSQL (MetalDB) — SSH test-connection + provision |
| `agentcontrol` | ✓ | ✓ | — | Fine-tuning, training, knowledge bases, datasets, server-side agents, GitHub import |
| **`vxcomputer`** | ✓ | ✓ | ✓ | Node-local policy-governed agent runtime (Plan→Act→Reflect) |
| **`workflow`** | ✓ | ✓ | ✓ | Visual DAG workflow orchestration (n8n-style) |
| **`vxchrono`** | ✓ | ✓ | ✓ | Autonomous goal executor & scheduler |
| **`robotic`** | ✓ | ✓ | ✓ | Robotic control cloud (robots, fleet, telemetry) |

`metaldb` and `agentcontrol` are not yet in the TypeScript SDK — Go and
Python have full coverage. The Python SDK also ships an async client
(`vxsdk_async.py`, `AsyncClient`) covering the same modules.

### Control Plane modules

These four modules target node-local control-plane services under
`/api/v2/*` on the resolved tenant node. Each is at parity across the
three SDKs and with the `vxcli vxcomputer | workflow | vxchrono | robotic`
commands.

```go
// VXCOMPUTER — governed agent loop
c.VxComputer().Info(ctx)
c.VxComputer().Classify(ctx, "rm -rf /tmp/x")
c.VxComputer().Run(ctx, vxcomputer.RunInput{Objective: "…", Channel: "cloudshell"})
c.VxComputer().ResolveApproval(ctx, vxcomputer.ApprovalInput{RunID: id, Command: cmd})
c.VxComputer().AuditVerify(ctx)

// Workflow — visual DAG engine
c.Workflow().List(ctx)
c.Workflow().Validate(ctx, definition)
c.Workflow().Execute(ctx, map[string]interface{}{"workflow_id": id})
c.Workflow().ListExecutions(ctx)
c.Workflow().CancelExecution(ctx, executionID)
c.Workflow().Export(ctx, definition, "yaml")

// VxChrono — autonomous goal scheduler
c.VxChrono().CreateGoal(ctx, goal)
c.VxChrono().Schedule(ctx, goalID, schedule)
c.VxChrono().LaunchRun(ctx, goalID, nil)
c.VxChrono().PauseRun(ctx, runID) // + ResumeRun / StopRun
c.VxChrono().DispatchScheduler(ctx)

// Robotic — robotic control cloud
c.Robotic().ListRobots(ctx)
c.Robotic().RegisterRobot(ctx, spec)
c.Robotic().SendCommand(ctx, robotID, payload)
c.Robotic().Telemetry(ctx, robotID, frame)
c.Robotic().EmergencyStop(ctx, robotID)
c.Robotic().FleetCommand(ctx, payload)
```

Python and TypeScript expose the same operations with idiomatic naming
(`c.vxcomputer.run(...)`, `await c.workflow.list()`, …).

## Core resource examples (Go)

```go
// list pipelines
pipelines, _ := c.CICD().Pipelines().List(ctx)

// trigger a build
build, _ := c.CICD().Pipelines().Trigger(ctx, pipelineID, "main")

// install a custom script via SSH+SCP
res, _ := c.Install().Script(ctx, install.ScriptOpts{
    SSH:        install.SSH{Host: "54.197.71.181", User: "ubuntu", KeyPairName: "AWSPRODKEY1.PEM"},
    ScriptName: "bootstrap.sh",
    Script:     scriptBytes,
    Args:       []string{"--tier=prod"},
})

// deploy a single Docker container
res, _ := c.Deploy().Container(ctx, deploy.ContainerOpts{
    SSH:           deploy.SSH{Host: ip, User: "ubuntu", KeyPairName: keyName},
    Image:         "grafana/grafana:latest",
    Name:          "grafana",
    Ports:         []string{"3000:3000"},
    RestartPolicy: "unless-stopped",
})

// deploy a language stack from a public git repo
res, _ := c.Deploy().Stack(ctx, deploy.StackGolang, deploy.StackOpts{
    SSH:         deploy.SSH{Host: ip, User: "ubuntu", KeyPairName: keyName},
    RepoURL:     "https://github.com/joelwembo/va-sample-golang.git",
    Branch:      "main",
    GitProvider: "github",
    AppName:     "va-sample-golang",
    HTTPPort:    "80",
    GoVersion:   "1.22",
})
```

### Domains, DNS & HTTPS

DNS records and CDN distributions are provisioned through `c.Cloud()`
(the same `/api/v2/tenant/provision/*` surface `vxcli deploy --service
route53|cname|cloudfront` uses). HTTPS for a deployed app is requested at
deploy time — the `deploy` container/stack options carry `EnableSSL`,
`Domain`, and `SSLEmail` fields, mirroring the `vxcli deploy
--enable-ssl --domain --ssl-email` flags. To **store** an
externally-issued certificate as a workspace credential, use the
`workspace` module (`/api/v2/setup/ssl-certificate-credentials`), which
matches `vxcli configure setup ssl`.

## Go module layout

```
services/sdk/
├── go.mod                 sub-module: github.com/prodxcloud/vxcloud
├── doc.go                 package overview
├── client.go              vxsdk.New, vxsdk.LoadFromVxcli, vxsdk.Authenticate
├── options.go             WithAPIKey, WithJWT, WithInfinityURL, WithNodeURL, …
├── auth/                  APIKey, Token, User, ExchangeResponse, Exchange()
├── transport/             one *http.Client; JSON + multipart; retry; auto-refresh on 401
├── errors/                typed error hierarchy; IsRetryable, FromHTTP
├── sessions/              c.Sessions()
├── cicd/                  c.CICD().Pipelines() / .Builds()
├── install/               c.Install().Script() / .Compose()
├── deploy/                c.Deploy().Container() / .Stack()
├── marketplace/           c.Marketplace().Agents() / .Models() / .Solutions()
├── cloud/                 c.Cloud().S3/IAM/VM/Network/Database/Kubernetes/Serverless()
├── nodes/                 c.Nodes() (Infinity control plane)
├── services/              c.Services() — container + host lifecycle
├── networks/              c.Networks() — diagnostic scripts
├── agents/                c.Agents() — AI agents
├── chat/                  c.Chat() — multi-provider AI chat
├── observability/         c.Observability() — backups / migrations / sync
├── billing/               c.Billing() — multicloud cost
├── workspace/             c.Workspace() — /api/v2/setup/* credentials
├── metaldb/               c.MetalDB() — self-managed PostgreSQL over SSH
├── agentcontrol/          c.AgentControl() — fine-tuning / training / datasets
├── vxcomputer/            c.VxComputer() — governed agent runtime
├── workflow/              c.Workflow() — visual workflow engine
├── vxchrono/              c.VxChrono() — goal scheduler
├── robotic/               c.Robotic() — robotic control cloud
├── vxsdktest/             stub HTTP server for SDK consumers' unit tests
├── internal/cred/         read ~/.vxcloud/credentials.json
└── examples/              basic / install_script / marketplace
```

## Design contracts (what stays stable across versions)

| Contract | Promise |
|---|---|
| **Auth headers** | The SDK only ever sets `Authorization: Bearer` and `X-API-Key`. No custom headers, no query-param tokens. |
| **Errors** | Every method returns one of the typed errors in `errors/`. New error categories may be added but never removed. Use `errors.As` to branch. |
| **Retries** | `NetworkError`, `ServerError`, `RateLimitError` are retried with exponential backoff up to `MaxRetries` (default 3). All others surface immediately. |
| **Wire format** | Struct tags use `json:"snake_case"` to match the FastAPI wire format verbatim. Go callers see normal CamelCase identifiers. |
| **Control-plane shapes** | Control-plane modules return the decoded JSON object as-is (`map[string]interface{}` / `dict` / `Record<string,unknown>`) — their response shapes are dynamic and intentionally not over-modeled. |
| **Tracing** | Every request carries a fresh `vx-request-id` header. A future `WithTracer(...)` option will hook OpenTelemetry; the existing header is forward-compatible. |
| **Versioning** | Sub-module tagged independently as `services/sdk/v0.1.0-preview` etc. v1.0 will guarantee API stability across minor releases. |

## Verified live

Core endpoints exercised against `node1.vxcloud.io` during preview:

| Module | Method | Result |
|---|---|---|
| `cicd.Pipelines.List` | `GET /api/v2/cicd/pipelines` | returned 4 pipelines |
| `install.Script` | `POST /api/v2/tenant/install/script` (multipart) | exit_code=0, stdout returned |
| `marketplace.Agents.List` | `GET /api/v2/marketplace/agents` | returned 7 agents |
| `marketplace.Solutions.List` | `GET /api/v2/marketplace/templates` | returned 30 solutions |
| `auth.Exchange` (auto-refresh) | `POST /api/v1/auth/developer/keys/login` | new JWT pair returned |

`go test ./vxsdktest/...` exercises happy path, auto-refresh on 401, and
typed `*AuthError` against an in-process stub server.

> The control-plane modules (`vxcomputer`, `workflow`, `vxchrono`,
> `robotic` — all three SDKs) and the Go `metaldb` / `agentcontrol`
> modules are newly added in this release. They are wired to the same
> transport/auth/error stack and compile/typecheck/`go vet` clean, but
> have not yet been exercised against a live node — treat them as preview
> until added to the table above.

## Tagging the SDK as a public Go module

The SDK lives at `services/sdk/` but ships as its own Go module
(`go.mod` already in place).

```bash
# 1. Replace the placeholder module path with your actual GitHub org.
sed -i 's|github.com/prodxcloud/vxcloud|github.com/<your-org>/<your-repo>|g' \
    services/sdk/go.mod services/sdk/**/*.go

# 2. Commit + tag with the sub-module prefix.
git add services/sdk
git commit -m "sdk: v0.1.0-preview"
git tag services/sdk/v0.1.0-preview
git push origin services/sdk/v0.1.0-preview

# 3. From any other Go module:
go get github.com/<your-org>/<your-repo>/services/sdk@v0.1.0-preview
```

## Status

**Preview**. Subject to change. Not yet linked to the frontend or the API
gateway. Production code should pin to a specific commit until v0.1.0 is
tagged.
