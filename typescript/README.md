# @vxcloud/sdk · TypeScript SDK

[![npm version](https://img.shields.io/npm/v/@vxcloud/sdk.svg)](https://www.npmjs.com/package/@vxcloud/sdk)
[![npm downloads](https://img.shields.io/npm/dm/@vxcloud/sdk.svg)](https://www.npmjs.com/package/@vxcloud/sdk)
[![node](https://img.shields.io/node/v/@vxcloud/sdk.svg)](https://www.npmjs.com/package/@vxcloud/sdk)
[![types](https://img.shields.io/npm/types/@vxcloud/sdk.svg)](https://www.npmjs.com/package/@vxcloud/sdk)
[![license](https://img.shields.io/npm/l/@vxcloud/sdk.svg)](https://www.apache.org/licenses/LICENSE-2.0)

**Provision infrastructure, deploy applications, and manage running services on the [vxcloud](https://vxcloud.io) platform — straight from TypeScript & Node.js.**

The official TypeScript SDK for the vxcloud / VxCloud platform. Fully typed, dual ESM + CJS, zero-config auth (API key with automatic refresh on 401), and the same wire contract as the [Go](https://github.com/prodxcloud/vxcloud) and [Python](https://pypi.org/project/vxcloud/) SDKs.

[Install](#install) · [Quick start](#quick-start) · [What you can do](#what-you-can-do) · [Auth](#authentication) · [Errors](#error-handling) · [Docs](https://vxcloud.io/docs/sdks)

> **Status: 0.1 — preview.** Pre-1.0 minor bumps (`0.X` → `0.X+1`) may change public surface. Pin tightly until 1.0.

---

## Install

```bash
npm install @vxcloud/sdk
# or
pnpm add @vxcloud/sdk
# or
yarn add @vxcloud/sdk
```

Requires **Node.js ≥18** (uses native `fetch` and `FormData`). Ships ESM + CommonJS builds and full `.d.ts` type definitions — no `@types` package needed.

## Quick start

```ts
import { VxCloud } from '@vxcloud/sdk';

// 1. Load credentials written by `vxcli auth login` (~/.vxcloud/credentials.json)
const c = await VxCloud.loadFromVxcli();
//    or pass them explicitly:
// const c = new VxCloud({ apiKey: 'xc_live_…', username: 'alice' });

// 2. Provision a VM
const vm = await c.cloud.vm.provision({
  name: 'api-vm',
  cloud: 'aws',
  instanceType: 't3.small',
  region: 'us-east-1',
  keyPairName: 'AWSPRODKEY2',
  tags: { app: 'studio-backend', env: 'staging' },
});

// 3. Deploy a Docker container onto it
const ssh = { host: vm.public_ip as string, sshUser: 'ubuntu', keyPairName: 'AWSPRODKEY1.PEM' };
const sess = await c.deploy.container({
  image: 'grafana/grafana:latest',
  name: 'grafana',
  ports: ['3000:3000'],
  ...ssh,
});
console.log('session:', sess.sessionId);

// 4. Manage it
console.log(await c.services.status('grafana', ssh));
await c.services.restart('grafana', ssh);
```

### Pick the entry-point name you like

All four resolve to the **same** class — there is no behavior difference:

```ts
import { VxCloud } from '@vxcloud/sdk';   // canonical (PascalCase)
import { vxcloud } from '@vxcloud/sdk';   // lowercase brand
import { Vxsdk }   from '@vxcloud/sdk';   // mirrors the Python `vxsdk` name
import { Client }  from '@vxcloud/sdk';   // mirrors `vxsdk.Client`

const c = await VxCloud.loadFromVxcli();
```

## What you can do

A thin, fully-typed wrapper over the vxcloud FastAPI control plane. Every
module is `await`-able and returns typed responses.

| Module | Highlights |
|---|---|
| `c.auth` | `whoami`, exchange, refresh |
| `c.cloud` | `vm.{provision,status,action}`, `createVm` (flat shortcut), `s3`, `iam.{createPolicy,createRole,createKeypair}`, `database`, `kubernetes`, `network.createVpc`, `serverless.createFunction` |
| `c.deploy` | `container` + all 12 stacks (`fastapi`, `react`, `nextjs`, `django`, `nodejs`, `python`, `golang`, `rust`, `cpp`, `php`, `static`) |
| `c.install` | `script` (run a remote installer), `compose` (apply a docker-compose) |
| `c.services` | `list/status/start/stop/restart/remove/logs` + `vm.{reboot,shutdown,diskCleanup,dockerCleanup,restartDocker,memory,disk,listServices,listContainers,killPort,stopService}` |
| `c.cicd` | `pipelines.{list,show,trigger}`, `builds.show`, `git.list` |
| `c.networks` | script catalog + remote-execute |
| `c.marketplace` | `agents/models/solutions.{list,show,deploy,provision}` |
| `c.metaldb` | `testConnection`, `provision` (self-managed PostgreSQL over SSH) |
| `c.agents` | `coding / devops / git / parallel / run / presets / tools / tool` |
| `c.agentcontrol` | `summary`, `fineTuning/training/knowledge.{list,get,create,wait}`, `datasets.{list,get,preview,upload}`, `agents.{list,execute}`, `github.{listRepos,importDataset}` |
| `c.chat` | `send(...)`, `quick(provider, model, question)` |
| `c.vxcomputer` | `info`, `health`, `classify`, `run`, `resolveApproval`, `auditVerify` |
| `c.workflow` | `list/get/create/save/publish/delete`, `validate`, `execute`, `testNode`, `listExecutions`, `cancelExecution`, `export` |
| `c.vxchrono` | `createGoal/listGoals/updateGoal`, `schedule`, `launchRun`, `pauseRun/resumeRun/stopRun`, `dispatchScheduler` |
| `c.robotic` | `listRobots/getRobot/registerRobot/sendCommand`, `telemetry`, `plan`, `emergencyStop`, `fleetCommand` |
| `c.observability` | `backups`, `migrations`, `sync` (backup CRUD, migration plan/execute, batch sync) |
| `c.billing` | `multicloud`, `optimization` |
| `c.workspace` | full `/api/v2/setup/*` — workspace + organization lifecycle, cloud/AI provider creds, API tokens, Git/payment/SMTP/SSL/OAuth/OKTA/CyberArk credentials |
| `c.nodes` | `list`, `current`, `setDefault` |
| `c.sessions` | `list`, `show`, `apply`, `pull`, `delete` |

```ts
// Deploy a language stack straight from a public git repo
await c.deploy.stack('golang', {
  host: '54.197.71.181', sshUser: 'ubuntu', keyPairName: 'AWSPRODKEY1.PEM',
  repoUrl: 'https://github.com/joelwembo/va-sample-golang.git', branch: 'main',
  gitProvider: 'github', appName: 'va-sample-golang',
  httpPort: '80', appPort: '8080', goVersion: '1.22',
});

// Trigger a CI/CD pipeline
for (const p of await c.cicd.pipelines.list()) console.log(p.id, p.name);
await c.cicd.pipelines.trigger({ pipelineId: 'abc…', branch: 'main' });
```

## Authentication

The SDK supports the full vxcloud auth model:

- **API key** — `xc_dev_…`, `xc_test_…`, `xc_live_…`. Exchanged lazily on
  the first protected call, refreshed on `401`, single-flight to avoid a
  thundering herd.
- **JWT** — pass `accessToken` directly if you already exchanged.
- **Vault key-pair name** — the server resolves the SSH key from your
  workspace Vault.
- **Local PEM file** — pass `keyPairLocation: '~/.ssh/id_rsa'` and the SDK
  reads it locally and attaches it as a `private_key_pem` multipart part.

```ts
const sess = await c.deploy.container({
  image: 'grafana/grafana:latest',
  name: 'grafana',
  ports: ['3000:3000'],
  host: '13.216.243.13',
  sshUser: 'ubuntu',
  keyPairLocation: '~/.ssh/awsprodkey1.pem',   // local PEM instead of Vault
});
```

## Error handling

Every error is a subclass of `VxError` — discriminate with `instanceof`:

```ts
import {
  VxAuthError, VxValidationError, VxRateLimitError,
  VxServerError, VxNetworkError, isRetryable,
} from '@vxcloud/sdk';

try {
  await c.deploy.container({ /* … */ });
} catch (err) {
  if (err instanceof VxAuthError) {
    // re-run `vxcli auth login`
  } else if (err instanceof VxValidationError) {
    // bad input — fix the call
  } else if (isRetryable(err)) {
    // VxRateLimit / VxServer / VxNetwork — back off + retry
  } else {
    throw err;
  }
}
```

The client retries transient failures (`VxRateLimitError`, `VxServerError`,
`VxNetworkError`) and transparently refreshes an expired API key on `401`
before replaying the request, so application code rarely sees token
expiration. Auth and validation errors surface immediately.

## SDKs for every stack

| Language | Package | Install |
|---|---|---|
| TypeScript / Node | [`@vxcloud/sdk`](https://www.npmjs.com/package/@vxcloud/sdk) | `npm install @vxcloud/sdk` |
| Python | [`vxcloud`](https://pypi.org/project/vxcloud/) / [`vxsdk`](https://pypi.org/project/vxsdk/) | `pip install vxcloud` |
| Go | [`github.com/prodxcloud/vxcloud`](https://github.com/prodxcloud/vxcloud) | `go get github.com/prodxcloud/vxcloud` |

## Development

```bash
npm install
npm run typecheck
npm test
npm run build
```

## Links

- 📦 npm: [npmjs.com/package/@vxcloud/sdk](https://www.npmjs.com/package/@vxcloud/sdk)
- 📖 Documentation: [vxcloud.io/docs/sdks](https://vxcloud.io/docs/sdks)
- 🛠️ Source & issues: [github.com/prodxcloud/vxcloud](https://github.com/prodxcloud/vxcloud)
- 📝 Changelog: [CHANGELOG.md](./CHANGELOG.md)

## License

Apache-2.0 © vxcloud / ProdXCloud
