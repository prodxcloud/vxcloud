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
import { SalesShift } from './salesshift.js';
import {
  SalesShiftBilling, SalesShiftSocial, SalesShiftOpportunities,
  SalesShiftTasks, SalesShiftCampaigns,
} from './salesshift-platform.js';
import {
  SalesShiftContacts, SalesShiftWorkflows, SalesShiftSequences,
} from './salesshift-crm.js';
import { Chat } from './chat.js';
import { CICD } from './cicd.js';
import { Cloud } from './cloud.js';
import { Deploy } from './deploy.js';
import { Install } from './install.js';
import { Leads } from './leads.js';
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
import { Connector } from './connector.js';
import { WebScraper } from './webscraper.js';
import { AgentCLI } from './agentcli.js';
import { validateApiKey } from './auth.js';

export const VERSION = '2026.8.28';

export interface VxCloudOptions {
  /** API key (xc_dev_*, xc_test_*, xc_live_*). Required unless `accessToken` is set. */
  apiKey?: string;
  /** Workspace username. Defaults to whatever is in the API-key claims. */
  username?: string;
  /** Pre-existing JWT (skip the exchange). */
  accessToken?: string;
  /** Pre-existing refresh token. */
  refreshToken?: string;
  /** Override the VxCloud (control plane) URL. Default: https://api.vxcloud.io */
  vxcloudURL?: string;
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
  readonly salesshift: SalesShift;
  /** CRM contacts — the only records that are mailable. */
  readonly contacts: SalesShiftContacts;
  /** The /automations canvas: build, validate, test-run, enroll. */
  readonly workflows: SalesShiftWorkflows;
  /** Multi-step outbound with delays and stop-on-reply. */
  readonly sequences: SalesShiftSequences;
  /** What the workspace pays for. Distinct from `billing`, which is the
   *  cloud-spend surface — these two bill in opposite directions. */
  readonly salesshiftBilling: SalesShiftBilling;
  readonly social: SalesShiftSocial;
  readonly opportunities: SalesShiftOpportunities;
  readonly tasks: SalesShiftTasks;
  readonly campaigns: SalesShiftCampaigns;
  readonly chat: Chat;
  readonly cicd: CICD;
  readonly cloud: Cloud;
  readonly deploy: Deploy;
  readonly install: Install;
  /** Prospect pool + saved leads. Deliberately separate from `salesshift`
   *  (the send surface): leads are NOT mailable until `convertLead` /
   *  `convertFromPool` turns one into a Contact. */
  readonly leads: Leads;
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
  readonly connector: Connector;
  readonly webscraper: WebScraper;
  readonly agentcli: AgentCLI;

  /** Default workspace tenant ID surfaced to resources that need it (agentcontrol). */
  readonly tenantId: string;
  /** Authed username — surfaced for resources (cloud, metaldb) that
   *  embed it in the request body the same way Python/Go do. */
  readonly username: string;

  private t: Transport;
  /** Cached tenant-node URL once resolved (or set explicitly). */
  private nodeUrlResolved = '';
  /** In-flight resolution, so concurrent callers share one lookup. */
  private nodeUrlPending?: Promise<string>;

  constructor(opts: VxCloudOptions) {
    if (!opts.apiKey && !opts.accessToken) {
      throw new Error('VxCloud: pass apiKey or accessToken');
    }
    if (opts.apiKey) validateApiKey(opts.apiKey);

    this.t = new Transport({
      vxcloudURL: opts.vxcloudURL ?? 'https://api.vxcloud.io',
      nodeURL: opts.nodeURL ?? opts.vxcloudURL ?? 'https://api.vxcloud.io',
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
    // An explicit nodeURL is the caller's decision and must win over discovery
    // — treat it as already resolved so ensureNodeUrl() never overrides it.
    if (opts.nodeURL) this.nodeUrlResolved = opts.nodeURL.replace(/\/+$/, '');

    this.auth = new AuthFacade(this.t);
    this.agents = new Agents(this.t);
    this.agentcontrol = new AgentControl(this.t, () => this.tenantId);
    this.billing = new Billing(this.t);
    this.salesshift = new SalesShift(this.t, () => this.ensureNodeUrl());
    this.contacts = new SalesShiftContacts(this.t);
    this.workflows = new SalesShiftWorkflows(this.t);
    this.sequences = new SalesShiftSequences(this.t);
    this.salesshiftBilling = new SalesShiftBilling(this.t);
    this.social = new SalesShiftSocial(this.t);
    this.opportunities = new SalesShiftOpportunities(this.t);
    this.tasks = new SalesShiftTasks(this.t);
    this.campaigns = new SalesShiftCampaigns(this.t);
    this.chat = new Chat(this.t);
    this.cicd = new CICD(this.t);
    this.cloud = new Cloud(this.t);
    this.deploy = new Deploy(this.t);
    this.install = new Install(this.t);
    this.leads = new Leads(this.t);
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
    this.connector = new Connector(this.t, () => this.username);
    this.webscraper = new WebScraper(this.t);
    this.agentcli = new AgentCLI(this.t, () => this.username);
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
      vxcloudURL: f.base_url || opts?.vxcloudURL,
      nodeURL: f.node_url || opts?.nodeURL,
      tenantId: f.tenant_id || opts?.tenantId,
      ...opts,
    });
  }

  /** Switch the active tenant node by URL. */
  setNodeURL(url: string): void {
    this.t.setNodeURL(url);
    this.nodeUrlResolved = url;
  }

  /**
   * Resolve the tenant node base URL the way the dashboard does, and point the
   * transport at it.
   *
   * Node-scoped endpoints (`/api/v2/*` — provisioning, sessions, services, the
   * SalesShift email worker) live on the caller's per-tenant node, not on the
   * VxCloud control plane. A client constructed without an explicit `nodeURL`
   * defaults to the control-plane URL, so those calls 404 — which reads as
   * "the service is down" rather than "you are asking the wrong host".
   *
   * Idempotent and cached; safe to await on every node-scoped call. Mirrors
   * `Client.ensure_node_url()` in the Python SDK, including the field
   * precedence: custom domain, then load balancer, then public IP.
   */
  async ensureNodeUrl(): Promise<string> {
    if (this.nodeUrlResolved) return this.nodeUrlResolved;
    if (!this.nodeUrlPending) {
      // Single-flight: concurrent node-scoped calls must not each fire their
      // own /auth/nodes/ request.
      this.nodeUrlPending = (async () => {
        const nodes = await this.nodes.list();
        const node = nodes.find((n) => n.isDefault) ?? nodes[0];
        if (!node) {
          throw new Error('VxCloud.ensureNodeUrl: no node records returned for this account');
        }
        const raw = node.raw ?? {};
        const addr = String(
          raw.custom_domain_name || raw.load_balancer || raw.public_ip || node.url || '',
        ).trim();
        if (!addr) {
          throw new Error('VxCloud.ensureNodeUrl: default node record has no resolvable address');
        }
        const url = (addr.startsWith('http') ? addr : `https://${addr}`).replace(/\/+$/, '');
        this.t.setNodeURL(url);
        this.nodeUrlResolved = url;
        return url;
      })().finally(() => { this.nodeUrlPending = undefined; });
    }
    return this.nodeUrlPending;
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
