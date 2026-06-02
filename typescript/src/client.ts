/**
 * VxCloud — entry-point client for the TypeScript SDK.
 *
 * Construct one for the lifetime of your process or request handler.
 * Acquire resource modules via the property accessors (`client.deploy`,
 * `client.services`, …). The client is safe for concurrent use.
 *
 *     // Explicit credentials
 *     const c = new VxCloud({ apiKey: 'xc_live_…', username: 'alice' });
 *
 *     // From `vxcli auth login`
 *     const c = await VxCloud.loadFromVxcli();
 *
 *     for (const p of await c.cicd.pipelines.list()) {
 *       console.log(p.id, p.name);
 *     }
 */

import { readFileSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { Transport } from './transport.js';
import { Agents } from './agents.js';
import { AgentControl } from './agentcontrol.js';
import { Billing } from './billing.js';
import { Chat } from './chat.js';
import { CICD } from './cicd.js';
import { Cloud } from './cloud.js';
import { Deploy } from './deploy.js';
import { Install } from './install.js';
import { Marketplace } from './marketplace.js';
import { MetalDB } from './metaldb.js';
import { Networks } from './networks.js';
import { Nodes } from './nodes.js';
import { Observability } from './observability.js';
import { Robotic } from './robotic.js';
import { Services } from './services.js';
import { Sessions } from './sessions.js';
import { VxChrono } from './vxchrono.js';
import { VxComputer } from './vxcomputer.js';
import { Workflow } from './workflow.js';
import { Workspace } from './workspace.js';
import { validateApiKey } from './auth.js';

export const VERSION = '0.1.0';

export interface VxCloudOptions {
  /** API key (xc_dev_*, xc_test_*, xc_live_*). Required unless `accessToken` is set. */
  apiKey?: string;
  /** Workspace username. Defaults to whatever is in the API-key claims. */
  username?: string;
  /** Pre-existing JWT (skip the exchange). */
  accessToken?: string;
  /** Pre-existing refresh token. */
  refreshToken?: string;
  /** Override the Infinity (control plane) URL. Default: https://api.vxcloud.io */
  infinityURL?: string;
  /** Override the active tenant node URL. Defaults to whatever the control plane returned. */
  nodeURL?: string;
  /** Custom User-Agent. */
  userAgent?: string;
  /** Total deadline for one request including retries (ms). */
  timeoutMs?: number;
  /** Inject a fetch implementation (for tests, or Node <18). */
  fetch?: typeof fetch;
  /** Default workspace tenant ID. Required for agentcontrol calls unless
   *  passed per-call. Mirrors Python `Client(tenant_id=…)`. */
  tenantId?: string;
}

export class VxCloud {
  static readonly VERSION = VERSION;

  readonly auth: AuthFacade;
  readonly agents: Agents;
  readonly agentcontrol: AgentControl;
  readonly billing: Billing;
  readonly chat: Chat;
  readonly cicd: CICD;
  readonly cloud: Cloud;
  readonly deploy: Deploy;
  readonly install: Install;
  readonly marketplace: Marketplace;
  readonly metaldb: MetalDB;
  readonly networks: Networks;
  readonly nodes: Nodes;
  readonly observability: Observability;
  readonly robotic: Robotic;
  readonly services: Services;
  readonly sessions: Sessions;
  readonly vxchrono: VxChrono;
  readonly vxcomputer: VxComputer;
  readonly workflow: Workflow;
  readonly workspace: Workspace;

  /** Default workspace tenant ID surfaced to resources that need it (agentcontrol). */
  readonly tenantId: string;
  /** Authed username — surfaced for resources (cloud, metaldb) that
   *  embed it in the request body the same way Python/Go do. */
  readonly username: string;

  private t: Transport;

  constructor(opts: VxCloudOptions) {
    if (!opts.apiKey && !opts.accessToken) {
      throw new Error('VxCloud: pass apiKey or accessToken');
    }
    if (opts.apiKey) validateApiKey(opts.apiKey);

    this.t = new Transport({
      infinityURL: opts.infinityURL ?? 'https://api.vxcloud.io',
      nodeURL: opts.nodeURL ?? opts.infinityURL ?? 'https://api.vxcloud.io',
      apiKey: opts.apiKey ?? '',
      username: opts.username ?? '',
      jwt: opts.accessToken ?? '',
      refreshToken: opts.refreshToken ?? '',
      userAgent: opts.userAgent ?? `@vxcloud/sdk/${VERSION}`,
      timeoutMs: opts.timeoutMs ?? 60_000,
      fetch: opts.fetch,
    });

    this.tenantId = opts.tenantId ?? '';
    this.username = opts.username ?? '';

    this.auth = new AuthFacade(this.t);
    this.agents = new Agents(this.t);
    this.agentcontrol = new AgentControl(this.t, () => this.tenantId);
    this.billing = new Billing(this.t);
    this.chat = new Chat(this.t);
    this.cicd = new CICD(this.t);
    this.cloud = new Cloud(this.t);
    this.deploy = new Deploy(this.t);
    this.install = new Install(this.t);
    this.marketplace = new Marketplace(this.t);
    this.metaldb = new MetalDB(this.t, () => this.username);
    this.networks = new Networks(this.t);
    this.nodes = new Nodes(this.t);
    this.observability = new Observability(this.t);
    this.robotic = new Robotic(this.t);
    this.services = new Services(this.t);
    this.sessions = new Sessions(this.t);
    this.vxchrono = new VxChrono(this.t);
    this.vxcomputer = new VxComputer(this.t);
    this.workflow = new Workflow(this.t);
    this.workspace = new Workspace(this.t);
  }

  /**
   * Load credentials from `~/.vxcloud/credentials.json` — the file
   * `vxcli auth login` writes. Mirrors `LoadFromVxcli` in Go and Python.
   */
  static loadFromVxcli(opts?: Partial<VxCloudOptions>): VxCloud {
    const homePath = path.join(os.homedir(), '.vxcloud', 'credentials.json');
    let raw: Buffer;
    try {
      raw = readFileSync(homePath);
    } catch (err) {
      throw new Error(`VxCloud.loadFromVxcli: cannot read ${homePath} — run \`vxcli auth login\` first (${(err as Error).message})`);
    }
    const f = JSON.parse(raw.toString('utf-8')) as Record<string, string>;
    return new VxCloud({
      apiKey: f.api_key,
      username: f.username,
      accessToken: f.access_token,
      refreshToken: f.refresh_token,
      infinityURL: f.base_url || opts?.infinityURL,
      nodeURL: f.node_url || opts?.nodeURL,
      tenantId: f.tenant_id || opts?.tenantId,
      ...opts,
    });
  }

  /** Switch the active tenant node by URL. */
  setNodeURL(url: string): void {
    this.t.setNodeURL(url);
  }
}

/** Thin facade: `client.auth.whoami()`, `client.auth.refresh()`. */
export class AuthFacade {
  constructor(private t: Transport) {}

  /** Read the current authenticated user. Triggers exchange if no JWT yet. */
  async whoami(): Promise<{ username: string; jwt: string; user?: unknown }> {
    // Triggers JWT exchange (lazy) on first protected call.
    const res = await this.t.get<{ user?: unknown }>('/api/v1/auth/me');
    const snap = this.t.getAuthSnapshot();
    return { username: snap.username, jwt: snap.jwt, user: (res.body as { user?: unknown })?.user };
  }
}
