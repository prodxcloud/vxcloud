/**
 * Agents resource — coding/devops/git/parallel + tool dispatch.
 * Mirrors `vxcli agent {coding, devops, git, parallel, presets, tool, tools}`.
 */

import type { Transport } from './transport.js';

export type AgentKind = 'coding' | 'devops' | 'git' | 'parallel';

export interface AgentRunInput {
  kind?: AgentKind;
  task: string;
  lang?: string;
  provider?: string;
  model?: string;
  context?: Record<string, string>;
}

export interface AgentRunOutput {
  output: string;
  provider?: string;
  model?: string;
  tokens?: number;
  raw: Record<string, unknown>;
}

export class Agents {
  constructor(private t: Transport) {}

  async run(input: AgentRunInput): Promise<AgentRunOutput> {
    if (!input.task) throw new Error('agents.run: task is required');
    const body = {
      kind: input.kind ?? 'coding',
      task: input.task,
      lang: input.lang ?? '',
      provider: input.provider ?? '',
      model: input.model ?? '',
      context: input.context ?? {},
    };
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/agents/run', body);
    const r = res.body ?? {};
    return {
      output: (r.output as string) ?? '',
      provider: r.provider as string | undefined,
      model: r.model as string | undefined,
      tokens: typeof r.tokens === 'number' ? r.tokens : undefined,
      raw: r,
    };
  }

  coding(task: string, lang = 'python'): Promise<AgentRunOutput> {
    return this.run({ kind: 'coding', task, lang });
  }
  devops(task: string): Promise<AgentRunOutput> {
    return this.run({ kind: 'devops', task });
  }
  git(task: string): Promise<AgentRunOutput> {
    return this.run({ kind: 'git', task });
  }
  parallel(preset: string, task: string): Promise<AgentRunOutput> {
    return this.run({ kind: 'parallel', task, context: { preset } });
  }

  async presets(): Promise<unknown[]> {
    const res = await this.t.get<{ presets?: unknown[] }>('/api/v2/agents/presets');
    return (res.body as { presets?: unknown[] })?.presets ?? [];
  }

  async tools(kind?: AgentKind): Promise<unknown[]> {
    const path = kind ? `/api/v2/agents/tools?kind=${kind}` : '/api/v2/agents/tools';
    const res = await this.t.get<{ tools?: unknown[] }>(path);
    return (res.body as { tools?: unknown[] })?.tools ?? [];
  }

  async tool(name: string, args?: Record<string, unknown>): Promise<Record<string, unknown>> {
    if (!name) throw new Error('agents.tool: name is required');
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/agents/tool', {
      tool: name, args: args ?? {},
    });
    return res.body ?? {};
  }
}
