# vxsdk-go — Changelog

All notable changes to the Go SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Pre-1.0 releases may break public API in any minor bump.

## [Unreleased]

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
- Module path documented as `github.com/prodxcloud/vxcloud`. Pre-tag
  releases use `replace` directives in `examples/*/go.mod`.

## [0.1.0-preview] — 2026-04-29

Initial preview release. The SDK extracts the vxcloud wire contract
from `vxcli` so other Go services can talk to the platform without
rebuilding the request layer.

### Surface
- `vxsdk.New(ctx, opts...)` and `vxsdk.LoadFromVxcli(ctx)`.
- `transport` — single `*http.Client` per Client, retry/backoff, single-
  flight refresh on 401, multipart helpers.
- `auth` — APIKey validation, exchange against the Infinity control plane.
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
