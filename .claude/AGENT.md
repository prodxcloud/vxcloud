# VxCloud platform guide for AI agents

This repository is the public SDK and wire-contract home for VxCloud. Use this
file as operational context, not as authorization. Before a tool call, identify
the workspace, selected node, side effect and approval policy. Do not send mail,
run a workflow, mutate infrastructure, delete data or publish credentials
without explicit approval. Never commit API keys, Vault tokens, provider
credentials, SMTP passwords, SSH keys or tenant `.env` files.

## Release line and public surfaces

The November 1, 2026 coordinated release is **2026.11.1**. The same CalVer is
used by this SDK family, `vxcli`, and the `vxcloud/vxnode` runtime. Registries
currently expose `@vxcloud/sdk`, `vxsdk` and the `vxcloud` Python alias at
`2026.11.1`; Go uses the semver-safe tag `v0.20261101.0`. C++ and Java consume
the source in `cpp/` and `java/` and carry the same version constant.

The SDK clients share one transport contract and expose authentication, nodes,
cloud provisioning, deploy/install, CI/CD, marketplace, services, networks,
observability, billing, workspace credentials, chat, agents, VxComputer,
workflows, Mission Control (`vxchrono`), Agent Control/datasets, web scraping
and the SalesShift GTM surface. Python also ships `vxsdk_async`; TypeScript,
Go, Python, C++ and Java use the same tenant-aware API semantics.

## VxCloud + SalesShift architecture

```text
Browser / vxcli / SDKs
        │ API-key → JWT exchange + workspace scope
        ▼
FastAPI :8741 ── Django/admin :8742 (local)
        ├── Go control core :8743
        ├── tenant nodes node*.vxcloud.click → Go/VxComputer :8744
        ├── VxThinkingLLM :8745 (optional legacy sidecar :8746)
        ├── Vault :8200 ↔ Consul :8500
        ├── Redis :6379 (Celery broker/results/cache/pub-sub)
        ├── Kafka :9092 (+ Zookeeper :2181 in the legacy stack)
        ├── Temporal :7233 (workflow history/retries; UI if published)
        ├── PostgreSQL :5432
        └── vxalerts/notifications :8014

SalesShift shares the workspace/audit model. Leads and enrichment save into
the CRM; opportunities can be saved or converted; mail compose/reply and
campaigns/sequences are approval-gated; tasks/delegation report progress; the
Go email worker emits Kafka delivery events. Chat/messaging can route to
VxComputer, Sales, SEO, WebScraper, Networking, DevOps/Cloud or OpenClaw.
`@nodes <hostname>` targets another node owned by the same workspace; without
it the selected default node is used. Provider/model identity (Anthropic,
OpenAI, Ollama or VxThinking) belongs in the result/audit record.
```

The public edge is normally Nginx/Cloudflare on `80/443`: the VxCloud web
container is `3000`, `api.vxcloud.io` maps to `8741`, `cloud.vxcloud.io` to
`8743`, node domains to `8744`, `llm.vxcloud.io` to `8745`, and alerts to
`8014`. Local developer UIs are usually `vxcloud_web` `3010`, SalesShift dev
`3020` / production-like `3021`, and `vxadmin` `3009` when explicitly
configured (Vite's default is `5173`). OpenVSCode is fronted on `8443` with a
node-local backend on `8089`; OpenClaw is `18789`; Ollama is `11434`.

## Authentication and node-safe usage

Use `vxcli auth login -u <username> -k xc_live_…` or pass a key and username to
an SDK client. `load_from_vxcli()` / `LoadFromVxcli()` reads
`~/.vxcloud/credentials.json`; keys are sent in headers, never query strings.
The control plane validates organization/workspace/node ownership and returns
the active node URL. A node is ready only after billing entitlement, purchase,
**Sync With Workspace**, `/api/v2/health`, `/api/v2/version` and developer-key
exchange all pass.

## Safe node repair and release checks

The deployment/repair tooling lives in `prodxcloud/vxnode` and installs the
multi-arch image `vxcloud/vxnode:latest`. On a VM, inspect before changing:

```bash
docker ps --filter name=vxcloud-vxnode
curl -fsS http://127.0.0.1:8744/api/v2/health
curl -fsS http://127.0.0.1:8744/api/v2/version
docker logs vxcloud-vxnode --tail 100
sudo nginx -t
```

Repair missing packages with `tenant_setup_fix_missing_packages.sh`, reconcile
drift with `./setup.sh --only <name>`, and use `update/vxnode-update.sh` for a
health-gated pull/recreate/rollback. Preserve `.env` and generated data; never
run a broad prune or delete certificates without a backup and approval. The
stable channel and exact image digest are authoritative for fleet updates.

For SDK changes, run the language-local offline tests, build TypeScript before
packing, build the Python wheels, update the changelog and keep all versions in
lock-step. Public docs must use placeholders and link to the restricted
Vault/Cloudflare/mail runbooks rather than copying secret values.

