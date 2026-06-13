/**
 * AgentCLI — install and manage AI agent CLIs on a remote VM over SSH:
 * Claude Code, OpenAI Codex, Google Gemini CLI, the Hermes Agent, and OpenClaw.
 * Mirrors `agentcli/agentcli.go` and the services/agentcli + services/openclaw
 * HTTP contract.
 *
 * Every call SSHes into the target VM (the node resolves the SSH key referenced
 * by `keyPairName` from the workspace vault) and runs the agent's installer or
 * a configure/health/test action. Four operations per agent:
 *
 *   install         POST /api/v2/infrastructure/services/{agent}/install
 *   configure       POST /api/v2/infrastructure/services/{agent}/configure
 *   health          POST /api/v2/infrastructure/services/{agent}/health
 *   testConnection  POST /api/v2/infrastructure/services/{agent}/test-connection
 *
 * All requests are multipart/form-data with the shared SSH fields plus the
 * per-operation options.
 */

import type { Transport } from './transport.js';
import { sshFields } from './internal.js';
import type { SSHTarget } from './types.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

/** Which agent CLI to act on — the value is the backend URL segment. */
export type Agent = 'claudecode' | 'codex' | 'gemini' | 'hermesagent' | 'openclaw';

/** Map friendly names ("claude", "hermes", …) to the canonical {@link Agent}.
 *  Returns undefined for unknown names. */
export function resolveAgent(name: string): Agent | undefined {
  switch (name) {
    case 'claude': case 'claudecode': case 'claude-code': return 'claudecode';
    case 'codex': case 'openai-codex': return 'codex';
    case 'gemini': case 'google-gemini': return 'gemini';
    case 'hermes': case 'hermesagent': case 'hermes-agent': return 'hermesagent';
    case 'openclaw': return 'openclaw';
    default: return undefined;
  }
}

/** Optional knobs for {@link AgentCLI.install}. */
export interface AgentInstallOpts {
  /** "npm" | "tarball" (default npm). */
  installMethod?: string;
  /** Node.js major version (default 24). */
  nodeVersion?: string;
  skipDocker?: boolean;
  skipCleanup?: boolean;
  /** Gateway agents only (hermes/openclaw): front via Traefik on this domain. */
  domain?: string;
  sslEmail?: string;
}

/** API keys / model / gateway settings for {@link AgentCLI.configure}. */
export interface AgentConfigureOpts {
  anthropicKey?: string;
  openaiKey?: string;
  geminiKey?: string;
  model?: string;
  gatewayPort?: string;
  startGateway?: boolean;
}

export class AgentCLI {
  constructor(private t: Transport, private getUsername: () => string) {}

  private async post(agent: Agent, action: string, fields: Record<string, string>): Promise<Result> {
    const res = await this.t.postMultipart<Result>(
      `/api/v2/infrastructure/services/${agent}/${action}`, fields, [],
    );
    return res.body ?? {};
  }

  /** Run the agent's installer on the target VM. */
  async install(agent: Agent, ssh: SSHTarget, opts: AgentInstallOpts = {}): Promise<Result> {
    const f = sshFields(ssh, this.getUsername());
    if (opts.installMethod) f.install_method = opts.installMethod;
    if (opts.nodeVersion) f.node_version = opts.nodeVersion;
    if (opts.skipDocker) f.skip_docker = 'true';
    if (opts.skipCleanup) f.skip_cleanup = 'true';
    if (opts.domain) {
      f.domain = opts.domain;
      if (opts.sslEmail) f.ssl_email = opts.sslEmail;
    }
    return this.post(agent, 'install', f);
  }

  /** Write API keys / model / gateway settings on the target VM. */
  async configure(agent: Agent, ssh: SSHTarget, opts: AgentConfigureOpts = {}): Promise<Result> {
    const f = sshFields(ssh, this.getUsername());
    const m: Record<string, string | undefined> = {
      anthropic_key: opts.anthropicKey,
      openai_key: opts.openaiKey,
      gemini_key: opts.geminiKey,
      model: opts.model,
      gateway_port: opts.gatewayPort,
    };
    for (const [k, v] of Object.entries(m)) if (v) f[k] = v;
    if (opts.startGateway) f.start_gateway = 'true';
    return this.post(agent, 'configure', f);
  }

  /** Run a health action: "status" (default), "logs", "doctor", "restart".
   *  `lines` controls how many log lines to tail (default 50 server-side). */
  async health(agent: Agent, ssh: SSHTarget, action = '', lines = 0): Promise<Result> {
    const f = sshFields(ssh, this.getUsername());
    if (action) f.action = action;
    if (lines > 0) f.log_lines = String(lines);
    return this.post(agent, 'health', f);
  }

  /** Verify SSH reachability and whether the agent binary is already installed. */
  async testConnection(agent: Agent, ssh: SSHTarget): Promise<Result> {
    const f = sshFields(ssh, this.getUsername());
    return this.post(agent, 'test-connection', f);
  }
}
