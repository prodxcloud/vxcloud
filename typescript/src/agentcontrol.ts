/**
 * AgentControl — server-side fine-tuning, training, knowledge-base,
 * dataset, agent, and GitHub-import surfaces under
 * /api/v2/agentcontrol/*. Mirrors Python `vxsdk.AgentControl` and Go
 * `agentcontrol`.
 *
 * Every call requires an `X-Tenant-ID` header; the value comes from
 * `client.tenantId` (set on the VxCloud constructor) and can be
 * overridden per call via `tenantId`. Long-running jobs (fine-tune,
 * training, knowledge-base) return a `LongRunningJob` that polls until
 * the row reaches a terminal status.
 */

import type { Transport, MultipartFile } from './transport.js';

type TenantOpt = { tenantId?: string };
type AnyExtra = Record<string, unknown>;

const TERMINAL = new Set(['succeeded', 'failed', 'cancelled', 'ready', 'error']);

/** Shared poller for fine-tune / training / knowledge-base rows.
 *  Holds the latest server dict; `refresh()` re-fetches via the parent
 *  sub-resource's `get(id)`. */
export class LongRunningJob {
  data: Record<string, unknown>;

  constructor(
    private readonly fetcher: (id: string) => Promise<Record<string, unknown>>,
    initial: Record<string, unknown>,
    private readonly op: string,
  ) {
    this.data = { ...initial };
  }

  get id(): string {
    return String(this.data.id ?? '');
  }

  get status(): string {
    return String(this.data.status ?? '');
  }

  get progress(): number {
    const p = this.data.progress;
    const n = typeof p === 'number' ? p : Number(p ?? 0);
    return Number.isFinite(n) ? n : 0;
  }

  async refresh(): Promise<LongRunningJob> {
    if (!this.id) return this;
    const fresh = await this.fetcher(this.id);
    this.data = { ...this.data, ...fresh };
    return this;
  }

  /** Poll until status is terminal or `timeoutMs` elapses. */
  async waitForCompletion(opts?: {
    timeoutMs?: number;
    intervalMs?: number;
    onTick?: (job: LongRunningJob) => void;
  }): Promise<LongRunningJob> {
    const timeoutMs = opts?.timeoutMs ?? 1_800_000;
    const intervalMs = Math.max(500, opts?.intervalMs ?? 5_000);
    const deadline = Date.now() + Math.max(0, timeoutMs);
    // eslint-disable-next-line no-constant-condition
    while (true) {
      await this.refresh();
      if (opts?.onTick) {
        try { opts.onTick(this); } catch { /* swallow user-callback errors */ }
      }
      if (TERMINAL.has(this.status.toLowerCase())) return this;
      if (Date.now() >= deadline) {
        throw new Error(`${this.op}: timed out after ${timeoutMs}ms; last status=${this.status || '<empty>'}`);
      }
      await new Promise<void>((r) => setTimeout(r, intervalMs));
    }
  }
}

/** Resolve the effective tenant header — per-call override beats client default. */
function tenantHeader(getDefault: () => string, override?: string): Record<string, string> {
  const tid = (override || '').trim() || (getDefault() || '').trim();
  if (!tid) {
    throw new Error('agentcontrol: tenantId required (set on VxCloud constructor or pass per call)');
  }
  return { 'X-Tenant-ID': tid };
}

function unwrapItems(body: unknown): unknown[] {
  if (Array.isArray(body)) return body;
  if (body && typeof body === 'object') {
    const items = (body as { items?: unknown[] }).items;
    if (Array.isArray(items)) return items;
  }
  return [];
}

// ── sub-resources ───────────────────────────────────────────────────────

export class FineTuning {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/fine-tuning/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async get(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!jobId) throw new Error('fineTuning.get: jobId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/fine-tuning/${encodeURIComponent(jobId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: {
    name: string;
    baseModel: string;
    trainingFile: string;
    validationFile?: string;
    epochs?: number;
    batchSize?: number;
    learningRate?: number;
    tenantId?: string;
    extra?: AnyExtra;
  }): Promise<LongRunningJob> {
    if (!input.name || !input.baseModel || !input.trainingFile) {
      throw new Error('fineTuning.create: name, baseModel, trainingFile are required');
    }
    const body: Record<string, unknown> = {
      name: input.name,
      base_model: input.baseModel,
      training_file: input.trainingFile,
      epochs: input.epochs ?? 1,
      batch_size: input.batchSize ?? 4,
      learning_rate: input.learningRate ?? 5e-5,
      ...(input.validationFile ? { validation_file: input.validationFile } : {}),
      ...(input.extra ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/fine-tuning/', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return new LongRunningJob(
      (id) => this.get(id, { tenantId: input.tenantId }),
      res.body ?? {},
      'agentcontrol.fine-tuning.wait',
    );
  }

  /** Convenience: create then waitForCompletion. */
  async wait(input: Parameters<FineTuning['create']>[0] & { timeoutMs?: number; intervalMs?: number }): Promise<LongRunningJob> {
    const job = await this.create(input);
    return job.waitForCompletion({ timeoutMs: input.timeoutMs, intervalMs: input.intervalMs });
  }

  async delete(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!jobId) throw new Error('fineTuning.delete: jobId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/fine-tuning/${encodeURIComponent(jobId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>(
      '/api/v2/agentcontrol/fine-tuning?confirm=true',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Training {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/training/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async get(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!jobId) throw new Error('training.get: jobId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: {
    name: string;
    baseModel: string;
    datasetId: string;
    type?: string;
    totalEpochs?: number;
    gpuType?: string;
    tenantId?: string;
    extra?: AnyExtra;
  }): Promise<LongRunningJob> {
    if (!input.name || !input.baseModel || !input.datasetId) {
      throw new Error('training.create: name, baseModel, datasetId are required');
    }
    const body: Record<string, unknown> = {
      name: input.name,
      base_model: input.baseModel,
      dataset_id: input.datasetId,
      type: input.type ?? 'pre-training',
      total_epochs: input.totalEpochs ?? 1,
      ...(input.gpuType ? { gpu_type: input.gpuType } : {}),
      ...(input.extra ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/training/', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return new LongRunningJob(
      (id) => this.get(id, { tenantId: input.tenantId }),
      res.body ?? {},
      'agentcontrol.training.wait',
    );
  }

  async wait(input: Parameters<Training['create']>[0] & { timeoutMs?: number; intervalMs?: number }): Promise<LongRunningJob> {
    const job = await this.create(input);
    return job.waitForCompletion({ timeoutMs: input.timeoutMs, intervalMs: input.intervalMs });
  }

  async update(jobId: string, patch: AnyExtra, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!jobId) throw new Error('training.update: jobId is required');
    const res = await this.t.putJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}`, patch,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!jobId) throw new Error('training.delete: jobId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(input?: { typeFilter?: string; tenantId?: string }): Promise<Record<string, unknown>> {
    let path = '/api/v2/agentcontrol/training?confirm=true';
    if (input?.typeFilter) path += `&type=${encodeURIComponent(input.typeFilter)}`;
    const res = await this.t.delete<Record<string, unknown>>(
      path, { headers: tenantHeader(this.getTenant, input?.tenantId) },
    );
    return res.body ?? {};
  }

  async clone(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/clone`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async restart(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/restart`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async runTests(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/tests`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async runQA(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/qa`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async export(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/export`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async logs(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/logs`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async metrics(jobId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(jobId)}/metrics`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async chat(input: {
    jobId: string;
    message: string;
    sessionId?: string;
    modelId?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.jobId || !input.message) {
      throw new Error('training.chat: jobId and message are required');
    }
    const body: Record<string, unknown> = { message: input.message };
    if (input.sessionId) body.session_id = input.sessionId;
    if (input.modelId) body.model_id = input.modelId;
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/training/${encodeURIComponent(input.jobId)}/chat`, body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Knowledge {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/knowledge/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async get(kbId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!kbId) throw new Error('knowledge.get: kbId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/knowledge/${encodeURIComponent(kbId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: {
    name: string;
    type?: string;
    tenantId?: string;
    extra?: AnyExtra;
  }): Promise<LongRunningJob> {
    if (!input.name) throw new Error('knowledge.create: name is required');
    const body: Record<string, unknown> = {
      name: input.name,
      type: input.type ?? 'documents',
      ...(input.extra ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/knowledge/', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return new LongRunningJob(
      (id) => this.get(id, { tenantId: input.tenantId }),
      res.body ?? {},
      'agentcontrol.knowledge.wait',
    );
  }

  async wait(input: Parameters<Knowledge['create']>[0] & { timeoutMs?: number; intervalMs?: number }): Promise<LongRunningJob> {
    const job = await this.create(input);
    return job.waitForCompletion({ timeoutMs: input.timeoutMs, intervalMs: input.intervalMs });
  }

  async delete(kbId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!kbId) throw new Error('knowledge.delete: kbId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/knowledge/${encodeURIComponent(kbId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>(
      '/api/v2/agentcontrol/knowledge?confirm=true',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Datasets {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/datasets/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async get(dsId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!dsId) throw new Error('datasets.get: dsId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/datasets/${encodeURIComponent(dsId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async preview(dsId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!dsId) throw new Error('datasets.preview: dsId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/datasets/${encodeURIComponent(dsId)}/preview`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  /** Multipart upload of a CSV/JSONL dataset. Pass either `fileContent`
   *  (Uint8Array/Blob/string) or `fileText` for inline body bytes —
   *  reading from disk is the caller's job for portability. */
  async upload(input: {
    name: string;
    type?: string;
    format?: string;
    filename: string;
    fileContent?: Uint8Array | Blob;
    fileText?: string;
    contentType?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.name || !input.filename) {
      throw new Error('datasets.upload: name and filename are required');
    }
    if (!input.fileContent && input.fileText === undefined) {
      throw new Error('datasets.upload: pass fileContent or fileText');
    }
    const fields: Record<string, string> = {
      name: input.name,
      type: input.type ?? 'training',
      format: input.format ?? 'csv',
    };
    const file: MultipartFile = input.fileContent !== undefined
      ? { field: 'file', filename: input.filename, content: input.fileContent, contentType: input.contentType ?? 'text/csv' }
      : { field: 'file', filename: input.filename, text: input.fileText ?? '', contentType: input.contentType ?? 'text/csv' };
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/agentcontrol/datasets/upload', fields, [file],
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    const name = (input.spec as { name?: string })?.name;
    if (!name) throw new Error("datasets.create: spec.name is required");
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/datasets/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  /** Download a dataset as a Blob (caller handles save/stream). */
  async download(dsId: string, opts?: TenantOpt): Promise<Blob> {
    if (!dsId) throw new Error('datasets.download: dsId is required');
    return this.t.getBlob(
      `/api/v2/agentcontrol/datasets/${encodeURIComponent(dsId)}/download`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
  }

  async delete(dsId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!dsId) throw new Error('datasets.delete: dsId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/datasets/${encodeURIComponent(dsId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

/** AgentControl-scoped agents — distinct from the top-level `client.agents`
 *  (which is the prompt/orchestration surface). These are the server-side
 *  agent rows persisted in the agentcontrol DB. */
export class AgentControlAgents {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/agents/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async execute(input: {
    agentId: string;
    task?: string;
    tenantId?: string;
    extra?: AnyExtra;
  }): Promise<Record<string, unknown>> {
    if (!input.agentId) throw new Error('agents.execute: agentId is required');
    const body: Record<string, unknown> = {
      task: input.task ?? '',
      ...(input.extra ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/agents/${encodeURIComponent(input.agentId)}/execute`,
      body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async get(agentId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!agentId) throw new Error('agents.get: agentId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/agents/${encodeURIComponent(agentId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    const spec = input.spec as { name?: string; model_id?: string };
    if (!spec?.name || !spec?.model_id) {
      throw new Error("agents.create: spec.name and spec.model_id are required");
    }
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/agents/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async update(agentId: string, patch: AnyExtra, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!agentId) throw new Error('agents.update: agentId is required');
    const res = await this.t.putJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/agents/${encodeURIComponent(agentId)}`, patch,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(agentId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!agentId) throw new Error('agents.delete: agentId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/agents/${encodeURIComponent(agentId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  /** Node-mediated execute against a marketplace agent (mixed-content workaround). */
  async proxyExecute(input: {
    endpoint: string;
    message: string;
    sessionId?: string;
    path?: string;
    payloadMode?: 'auto' | 'message' | 'prompt' | 'query' | 'input' | 'common';
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.endpoint) throw new Error('agents.proxyExecute: endpoint is required');
    if (!input.message) throw new Error('agents.proxyExecute: message is required');
    const body: Record<string, unknown> = { endpoint: input.endpoint, message: input.message };
    if (input.sessionId) body.session_id = input.sessionId;
    if (input.path) body.path = input.path;
    if (input.payloadMode) body.payload_mode = input.payloadMode;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/agents/proxy-execute', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

export class AgentControlGitHub {
  constructor(private t: Transport, private getTenant: () => string) {}

  async listRepos(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/github/repos',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async importDataset(input: {
    repo: string;
    branch?: string;
    path?: string;
    name?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.repo) throw new Error('github.importDataset: repo is required');
    const body = {
      repo: input.repo,
      branch: input.branch ?? 'main',
      path: input.path ?? '',
      name: input.name || input.repo.split('/').pop() || input.repo,
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/github/import-dataset', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  /** Browse repo contents (used by the Programming tab's "Import from GitHub"). */
  async repoContents(input: {
    owner: string;
    repo: string;
    path?: string;
    ref?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.owner || !input.repo) {
      throw new Error('github.repoContents: owner + repo are required');
    }
    const qs = new URLSearchParams();
    if (input.path) qs.set('path', input.path);
    if (input.ref) qs.set('ref', input.ref);
    const suffix = qs.toString() ? `?${qs.toString()}` : '';
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/github/repos/${encodeURIComponent(input.owner)}/${encodeURIComponent(input.repo)}/contents${suffix}`,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

// ── New sub-resources for UI parity ─────────────────────────────────────

export class Embeddings {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/embeddings',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async query(input: { artifactId: string; question: string; topK?: number; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.artifactId) throw new Error('embeddings.query: artifactId is required');
    if (!input.question) throw new Error('embeddings.query: question is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/embeddings/${encodeURIComponent(input.artifactId)}/query`,
      { question: input.question, top_k: input.topK ?? 5 },
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async download(input: { artifactId: string; part: 'faiss' | 'chromadb' | string; tenantId?: string }): Promise<Blob> {
    if (!input.artifactId || !input.part) throw new Error('embeddings.download: artifactId + part required');
    const url = `/api/v2/agentcontrol/embeddings/${encodeURIComponent(input.artifactId)}/download?part=${encodeURIComponent(input.part)}`;
    return this.t.getBlob(url, { headers: tenantHeader(this.getTenant, input.tenantId) });
  }

  async visualize(input: { artifactId: string; maxPoints?: number; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.artifactId) throw new Error('embeddings.visualize: artifactId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/embeddings/${encodeURIComponent(input.artifactId)}/visualize?max_points=${input.maxPoints ?? 400}`,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async promote(artifactId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!artifactId) throw new Error('embeddings.promote: artifactId is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/embeddings/${encodeURIComponent(artifactId)}/promote`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async upload(input: { filename: string; contentBase64: string; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.filename || !input.contentBase64) throw new Error('embeddings.upload: filename + contentBase64 required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/embeddings/upload',
      { filename: input.filename, content_base64: input.contentBase64 },
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(artifactId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!artifactId) throw new Error('embeddings.delete: artifactId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/embeddings/${encodeURIComponent(artifactId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>(
      '/api/v2/agentcontrol/embeddings?confirm=true',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Tools {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/tools/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!(input.spec as { name?: string })?.name) throw new Error('tools.create: spec.name is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/tools/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async update(toolId: string, patch: AnyExtra, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!toolId) throw new Error('tools.update: toolId is required');
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/tools/${encodeURIComponent(toolId)}`, patch,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(toolId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!toolId) throw new Error('tools.delete: toolId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/tools/${encodeURIComponent(toolId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class MCP {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/mcp-servers/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    const spec = input.spec as { name?: string; url?: string };
    if (!spec?.name || !spec?.url) throw new Error("mcp.create: spec.name and spec.url are required");
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/mcp-servers/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async refresh(serverId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!serverId) throw new Error('mcp.refresh: serverId is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/mcp-servers/${encodeURIComponent(serverId)}/refresh`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(serverId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!serverId) throw new Error('mcp.delete: serverId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/mcp-servers/${encodeURIComponent(serverId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Evals {
  constructor(private t: Transport, private getTenant: () => string) {}

  async listRuns(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/evals/runs/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async createRun(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!(input.spec as { name?: string })?.name) throw new Error('evals.createRun: spec.name is required');
    const tid = (input.tenantId || '').trim() || (this.getTenant() || '').trim();
    const body: Record<string, unknown> = { tenant_id: tid, ...input.spec };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/evals/runs/', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteRun(runId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!runId) throw new Error('evals.deleteRun: runId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/evals/runs/${encodeURIComponent(runId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async submitFeedback(input: { requestId: string; feedback: string; comment?: string; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.requestId || !input.feedback) throw new Error('evals.submitFeedback: requestId + feedback required');
    const body: Record<string, unknown> = { request_id: input.requestId, feedback: input.feedback };
    if (input.comment) body.comment = input.comment;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/evals/feedback', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async feedbackStats(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/evals/stats',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Code {
  constructor(private t: Transport, private getTenant: () => string) {}

  async run(input: {
    language: string;
    content: string;
    filename?: string;
    env?: Record<string, string>;
    timeoutSecs?: number;
    args?: string[];
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.language) throw new Error('code.run: language is required');
    if (!input.content) throw new Error('code.run: content is required');
    const body: Record<string, unknown> = { language: input.language, content: input.content };
    if (input.filename) body.filename = input.filename;
    if (input.env) body.env = input.env;
    if (input.timeoutSecs) body.timeout_secs = input.timeoutSecs;
    if (input.args) body.args = input.args;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/code/run', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async save(input: { language: string; content: string; filename?: string; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.language || !input.content) throw new Error('code.save: language + content required');
    const body: Record<string, unknown> = { language: input.language, content: input.content };
    if (input.filename) body.filename = input.filename;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/code/save', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async listSaved(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/code/saved',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async getSaved(filename: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!filename) throw new Error('code.getSaved: filename is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/code/saved/${encodeURIComponent(filename)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteSaved(filename: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!filename) throw new Error('code.deleteSaved: filename is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/code/saved/${encodeURIComponent(filename)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Models {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: { state?: string; tenantId?: string }): Promise<unknown[]> {
    const qs = opts?.state ? `?state=${encodeURIComponent(opts.state)}` : '';
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/models/${qs}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async get(modelId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!modelId) throw new Error('models.get: modelId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/models/${encodeURIComponent(modelId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!(input.spec as { name?: string })?.name) throw new Error('models.create: spec.name is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/models/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(modelId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!modelId) throw new Error('models.delete: modelId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/models/${encodeURIComponent(modelId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>(
      '/api/v2/agentcontrol/models?confirm=true',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async setState(modelId: string, state: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!modelId || !state) throw new Error('models.setState: modelId + state required');
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/models/${encodeURIComponent(modelId)}/state`, { state },
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async exportTrainingData(modelId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!modelId) throw new Error('models.exportTrainingData: modelId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/models/${encodeURIComponent(modelId)}/export`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Deployments {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/deployments/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async summary(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/deployments/summary',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    const spec = input.spec as { name?: string; model_id?: string };
    if (!spec?.name || !spec?.model_id) throw new Error('deployments.create: spec.name + spec.model_id required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/deployments/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async sync(depId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!depId) throw new Error('deployments.sync: depId is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/deployments/${encodeURIComponent(depId)}/sync`, {},
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async setStatus(depId: string, status: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!depId || !status) throw new Error('deployments.setStatus: depId + status required');
    const res = await this.t.patchJSON<Record<string, unknown>>(
      `/api/v2/agentcontrol/deployments/${encodeURIComponent(depId)}/status`, { status },
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(depId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!depId) throw new Error('deployments.delete: depId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/deployments/${encodeURIComponent(depId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async deleteAll(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>(
      '/api/v2/agentcontrol/deployments?confirm=true',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class WebAssets {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/web-assets/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async create(input: { spec: AnyExtra; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!(input.spec as { name?: string })?.name) throw new Error('webAssets.create: spec.name is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/web-assets/', input.spec,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async delete(assetId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!assetId) throw new Error('webAssets.delete: assetId is required');
    const res = await this.t.delete<Record<string, unknown>>(
      `/api/v2/agentcontrol/web-assets/${encodeURIComponent(assetId)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Benchmarks {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/benchmarks/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async create(spec: AnyExtra, opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/benchmarks/', spec,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Catalog {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/catalog/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async summary(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/catalog/summary',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Health {
  constructor(private t: Transport, private getTenant: () => string) {}

  async allModels(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/health/models/status',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async model(modelId: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!modelId) throw new Error('health.model: modelId is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/health/models/${encodeURIComponent(modelId)}/status`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Events {
  constructor(private t: Transport, private getTenant: () => string) {}

  async status(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/events/status',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async publish(input: { topic: string; eventType: string; payload: unknown; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.topic || !input.eventType) throw new Error('events.publish: topic + eventType required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/events/publish',
      { topic: input.topic, event_type: input.eventType, payload: input.payload },
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

export class LLM {
  constructor(private t: Transport, private getTenant: () => string) {}

  async chat(input: {
    provider: string;
    model: string;
    message: string;
    agentType?: string;
    sessionId?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    if (!input.provider || !input.model || !input.message) {
      throw new Error('llm.chat: provider, model, message required');
    }
    const body: Record<string, unknown> = { provider: input.provider, model: input.model, message: input.message };
    if (input.agentType) body.agent_type = input.agentType;
    if (input.sessionId) body.session_id = input.sessionId;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/llm/chat', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async providers(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/llm/providers',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

export class DeployTargets {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<unknown[]> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/deploy-targets',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return unwrapItems(res.body);
  }

  async provision(input: {
    cloudProvider?: string;
    region?: string;
    instanceType?: string;
    os?: string;
    instanceName?: string;
    tenantId?: string;
  }): Promise<Record<string, unknown>> {
    const body: Record<string, unknown> = {};
    if (input.cloudProvider) body.cloud_provider = input.cloudProvider;
    if (input.region) body.region = input.region;
    if (input.instanceType) body.instance_type = input.instanceType;
    if (input.os) body.os = input.os;
    if (input.instanceName) body.instance_name = input.instanceName;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/deploy-targets/provision', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }

  async provisionStatus(input: { sessionId: string; username: string; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.sessionId || !input.username) throw new Error('deployTargets.provisionStatus: sessionId + username required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/deploy-targets/provision/${encodeURIComponent(input.sessionId)}?username=${encodeURIComponent(input.username)}`,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Workflows {
  constructor(private t: Transport, private getTenant: () => string) {}

  async list(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/workflows/',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  async trigger(input: { workflowId: string; input?: unknown; tenantId?: string }): Promise<Record<string, unknown>> {
    if (!input.workflowId) throw new Error('workflows.trigger: workflowId is required');
    const body: Record<string, unknown> = { workflow_id: input.workflowId };
    if (input.input !== undefined) body.input = input.input;
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/agentcontrol/workflows/trigger', body,
      { headers: tenantHeader(this.getTenant, input.tenantId) },
    );
    return res.body ?? {};
  }
}

export class Infra {
  constructor(private t: Transport, private getTenant: () => string) {}

  async endpoints(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/infra/endpoints',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}

// ── facade ──────────────────────────────────────────────────────────────

export class AgentControl {
  // Original sub-resources
  readonly fineTuning: FineTuning;
  readonly training: Training;
  readonly knowledge: Knowledge;
  readonly datasets: Datasets;
  readonly agents: AgentControlAgents;
  readonly github: AgentControlGitHub;
  // Sub-resources added for UI parity
  readonly embeddings: Embeddings;
  readonly tools: Tools;
  readonly mcp: MCP;
  readonly evals: Evals;
  readonly code: Code;
  readonly models: Models;
  readonly deployments: Deployments;
  readonly webAssets: WebAssets;
  readonly benchmarks: Benchmarks;
  readonly catalog: Catalog;
  readonly health: Health;
  readonly events: Events;
  readonly llm: LLM;
  readonly deployTargets: DeployTargets;
  readonly workflows: Workflows;
  readonly infra: Infra;

  constructor(private t: Transport, private getTenant: () => string) {
    this.fineTuning = new FineTuning(t, getTenant);
    this.training = new Training(t, getTenant);
    this.knowledge = new Knowledge(t, getTenant);
    this.datasets = new Datasets(t, getTenant);
    this.agents = new AgentControlAgents(t, getTenant);
    this.github = new AgentControlGitHub(t, getTenant);
    this.embeddings = new Embeddings(t, getTenant);
    this.tools = new Tools(t, getTenant);
    this.mcp = new MCP(t, getTenant);
    this.evals = new Evals(t, getTenant);
    this.code = new Code(t, getTenant);
    this.models = new Models(t, getTenant);
    this.deployments = new Deployments(t, getTenant);
    this.webAssets = new WebAssets(t, getTenant);
    this.benchmarks = new Benchmarks(t, getTenant);
    this.catalog = new Catalog(t, getTenant);
    this.health = new Health(t, getTenant);
    this.events = new Events(t, getTenant);
    this.llm = new LLM(t, getTenant);
    this.deployTargets = new DeployTargets(t, getTenant);
    this.workflows = new Workflows(t, getTenant);
    this.infra = new Infra(t, getTenant);
  }

  /** Top-level summary across all agentcontrol sub-resources. */
  async summary(opts?: TenantOpt): Promise<Record<string, unknown>> {
    const res = await this.t.get<Record<string, unknown>>(
      '/api/v2/agentcontrol/summary',
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }

  /** Proxy a Prometheus-style /metrics scrape through the node so the SDK
   *  can read marketplace agent metrics without mixed-content/CORS issues. */
  async runtimeMetrics(endpoint: string, opts?: TenantOpt): Promise<Record<string, unknown>> {
    if (!endpoint) throw new Error('agentcontrol.runtimeMetrics: endpoint is required');
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/agentcontrol/runtime/metrics?endpoint=${encodeURIComponent(endpoint)}`,
      { headers: tenantHeader(this.getTenant, opts?.tenantId) },
    );
    return res.body ?? {};
  }
}
