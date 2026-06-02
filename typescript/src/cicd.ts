/**
 * CI/CD resource — pipelines + builds + git provider connections.
 */

import type { Transport } from './transport.js';
import type { Pipeline, Build } from './types.js';

export class CICD {
  readonly pipelines: Pipelines;
  readonly builds: Builds;
  readonly git: GitProviders;

  constructor(t: Transport) {
    this.pipelines = new Pipelines(t);
    this.builds = new Builds(t);
    this.git = new GitProviders(t);
  }
}

export class Pipelines {
  constructor(private t: Transport) {}

  async list(): Promise<Pipeline[]> {
    const res = await this.t.get<{ pipelines?: Pipeline[] } | Pipeline[]>('/api/v2/cicd/pipelines');
    if (Array.isArray(res.body)) return res.body.map(wrapPipeline);
    return ((res.body as { pipelines?: Pipeline[] })?.pipelines ?? []).map(wrapPipeline);
  }

  async show(id: string): Promise<Pipeline> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/cicd/pipelines/${encodeURIComponent(id)}`,
    );
    return wrapPipeline(res.body);
  }

  async trigger(id: string, branch = 'main'): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/cicd/pipelines/${encodeURIComponent(id)}/trigger`,
      { branch },
    );
    return res.body ?? {};
  }
}

export class Builds {
  constructor(private t: Transport) {}

  async show(id: string): Promise<Build> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/cicd/builds/${encodeURIComponent(id)}`,
    );
    return wrapBuild(res.body);
  }
}

export class GitProviders {
  constructor(private t: Transport) {}

  async list(): Promise<unknown[]> {
    const res = await this.t.get<{ providers?: unknown[] } | unknown[]>('/api/v2/cicd/git');
    if (Array.isArray(res.body)) return res.body;
    return (res.body as { providers?: unknown[] })?.providers ?? [];
  }
}

function wrapPipeline(input: unknown): Pipeline {
  const b = (typeof input === 'object' && input !== null) ? input as Record<string, unknown> : {};
  return {
    id: (b.id as string) ?? '',
    name: b.name as string | undefined,
    repository: b.repository as string | undefined,
    branch: b.branch as string | undefined,
    status: b.status as string | undefined,
    raw: b,
  };
}

function wrapBuild(input: unknown): Build {
  const x = (typeof input === 'object' && input !== null) ? input as Record<string, unknown> : {};
  return {
    id: (x.id as string) ?? '',
    pipelineId: x.pipeline_id as string | undefined,
    status: x.status as string | undefined,
    startedAt: x.started_at as string | undefined,
    finishedAt: x.finished_at as string | undefined,
    raw: x,
  };
}
