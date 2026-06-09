# vxsdk (Python) — Changelog

All notable changes to the Python SDK. Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning is **CalVer** (`YYYY.M.D`) to stay aligned with the vxnode fleet
release tags (e.g. `v2026.6.10-1`). The 0.1.x preview line predates this switch.

## [Unreleased]

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
