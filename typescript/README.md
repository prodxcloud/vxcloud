# @vxcloud/sdk

[![npm](https://img.shields.io/npm/v/@vxcloud/sdk.svg)](https://www.npmjs.com/package/@vxcloud/sdk)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Official TypeScript SDK for the [vxcloud / VxCloud](https://vxcloud.io)
platform. Provision infrastructure, deploy applications, and manage
running services from Node.js (and, with caveats, the browser).

> **Status: 0.1 — preview.** API surface stable across minor versions
> only after 1.0. Track [`BIG_PLAN.md`](../BIG_PLAN.md) for the roadmap.

## Install

```bash
npm install @vxcloud/sdk
# or
pnpm add @vxcloud/sdk
# or
yarn add @vxcloud/sdk
```

Requires **Node.js ≥18** (uses native `fetch` and `FormData`).

## Entry-point styles

All four below resolve to the same class — pick the one your team prefers:

```ts
import { VxCloud }   from '@vxcloud/sdk';   // canonical (PascalCase)
import { vxcloud }   from '@vxcloud/sdk';   // lowercase brand
import { Vxsdk }     from '@vxcloud/sdk';   // mirrors the Python `vxsdk` name
import { Client }    from '@vxcloud/sdk';   // mirrors `vxsdk.Client`

const c = await VxCloud.loadFromVxcli();
// or: const c = await vxcloud.loadFromVxcli();
// or: const c = await Vxsdk.loadFromVxcli();
// or: const c = await Client.loadFromVxcli();
```

## Quick start

```ts
import { VxCloud } from '@vxcloud/sdk';

// 1. Load credentials from `vxcli auth login`
const c = await VxCloud.loadFromVxcli();
//    or pass them explicitly:
// const c = new VxCloud({ apiKey: 'xc_live_…', username: 'alice' });

// 2. Provision a VM — pass `name` to use the prod-verified body shape.
//    `c.cloud.createVm(...)` is also available as a flat shortcut that
//    mirrors the Python SDK's `c.cloud.create_vm(...)`.
const vm = await c.cloud.vm.provision({
  name: 'api-vm',
  cloud: 'aws',
  instanceType: 't3.small',
  region: 'us-east-1',
  keyPairName: 'AWSPRODKEY2',
  tags: { app: 'studio-backend', env: 'staging' },
});

// 3. Deploy a Docker container onto it
const ssh = {
  host: vm.public_ip as string,
  sshUser: 'ubuntu',
  keyPairName: 'AWSPRODKEY1.PEM',
};
const sess = await c.deploy.container({
  image: 'grafana/grafana:latest',
  name: 'grafana',
  ports: ['3000:3000'],
  ...ssh,
});
console.log('session:', sess.sessionId);

// 4. Manage it
console.log(await c.services.list(ssh));
console.log(await c.services.status('grafana', ssh));
await c.services.restart('grafana', ssh);

// 5. Host-level diagnostics
console.log((await c.services.vm.memory(ssh)).output);
```

## Authentication

The SDK supports the full vxcloud auth model:

- **API key** — `xc_dev_…`, `xc_test_…`, `xc_live_…`. Exchange happens
  lazily on the first protected call, refreshes on 401, single-flight
  to avoid thundering-herd.
- **JWT** — pass `accessToken` directly if you've already exchanged.
- **Vault key-pair name** — server resolves the SSH key from your
  workspace Vault.
- **Local PEM file** — pass `keyPairLocation: '~/.ssh/id_rsa'` and the
  SDK reads it locally and attaches as a `private_key_pem` multipart
  part. Server-side support is rolling out (see
  [`BIG_PLAN.md`](../BIG_PLAN.md) Open decision #7).

```ts
// Read a local PEM instead of the Vault entry
const sess = await c.deploy.container({
  image: 'grafana/grafana:latest',
  name: 'grafana',
  ports: ['3000:3000'],
  host: '13.216.243.13',
  sshUser: 'ubuntu',
  keyPairLocation: '~/.ssh/awsprodkey1.pem',
});
```

## Modules

| Module | Highlights |
|---|---|
| `client.auth` | `whoami` |
| `client.cicd` | `pipelines.list/show/trigger`, `builds.show`, `git.list` |
| `client.cloud` | `vm.{provision,status,action}`, `createVm` (flat shortcut), `s3`, `iam.{createPolicy,createRole,createKeypair}`, `database`, `kubernetes`, `network.createVpc`, `serverless.createFunction` |
| `client.deploy` | `container`, plus all 12 stacks (`fastapi`, `react`, `nextjs`, `django`, `nodejs`, `python`, `golang`, `rust`, `cpp`, `php`, `static`) |
| `client.install` | `script` (run a remote installer), `compose` (apply a docker-compose) |
| `client.services` | `list/status/start/stop/restart/remove/logs` + `vm.{reboot, shutdown, diskCleanup, dockerCleanup, restartDocker, memory, disk, listServices, listContainers, killPort, stopService}` |
| `client.marketplace` | `agents/models/solutions.list/show/deploy/provision` |
| `client.metaldb` | `testConnection`, `provision` (self-managed PostgreSQL over SSH) |
| `client.agentcontrol` | `summary`, `fineTuning/training/knowledge.{list,get,create,wait}`, `datasets.{list,get,preview,upload}`, `agents.{list,execute}`, `github.{listRepos,importDataset}` + `LongRunningJob` helper |
| `client.nodes` | `list`, `current`, `setDefault` |
| `client.sessions` | `list`, `show`, `apply`, `pull`, `delete` |
| `client.vxcomputer` | `info`, `health`, `classify`, `run`, `resolveApproval`, `auditVerify` |
| `client.workflow` | `list/get/create/save/publish/delete`, `validate`, `execute`, `testNode`, `listExecutions`, `cancelExecution`, `export` |
| `client.vxchrono` | `init`, `createGoal`, `listGoals`, `updateGoal`, `schedule`, `launchRun`, `pauseRun/resumeRun/stopRun`, `dispatchScheduler` |
| `client.robotic` | `listRobots`, `getRobot`, `registerRobot`, `sendCommand`, `telemetry`, `plan`, `emergencyStop`, `fleetCommand` |

## Errors

All errors are subclasses of `VxError` — discriminate with `instanceof`:

```ts
import { VxAuthError, VxValidationError, VxRateLimitError, VxServerError, VxNetworkError, isRetryable } from '@vxcloud/sdk';

try {
  await c.deploy.container({ /* … */ });
} catch (err) {
  if (err instanceof VxAuthError) {
    // re-run vxcli auth login
  } else if (err instanceof VxValidationError) {
    // bad input — fix the call
  } else if (isRetryable(err)) {
    // VxRateLimit / VxServer / VxNetwork — back off + retry
  } else {
    throw err;
  }
}
```

## Development

```bash
pnpm install
pnpm typecheck
pnpm test
pnpm build
```

## Versioning

Pre-1.0 releases follow [SemVer 0.x rules](https://semver.org/#spec-item-4):
any `0.X.Y` → `0.X+1.0` may break public surface. Pin tightly until 1.0.

## License

Apache-2.0. See [`LICENSE`](LICENSE).
