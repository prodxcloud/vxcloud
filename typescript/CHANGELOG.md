# @vxcloud/sdk — Changelog

All notable changes to the TypeScript SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Pre-1.0 releases may break public API in any minor bump.

## [Unreleased]

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
