/**
 * Transport — one fetch wrapper per Client. Adds:
 *   - auth headers (X-API-Key + Bearer JWT) on every request
 *   - JSON + multipart helpers
 *   - 401 → single-flight refresh → retry once
 *   - retry/backoff for 5xx + network errors (with jitter)
 *   - typed error mapping (errors.fromHTTP)
 *
 * Mirrors `transport/transport.go` and `vxsdk.Client._request` in Python.
 */

import { type AuthState, authHeaders, exchangeKey } from './auth.js';
import { fromHTTP, isRetryable, VxNetworkError } from './errors.js';

export interface TransportOptions {
  infinityURL: string;
  nodeURL: string;
  apiKey: string;
  username: string;
  jwt?: string;
  refreshToken?: string;
  userAgent?: string;
  /** Total request deadline including retries. Default 60s. */
  timeoutMs?: number;
  /** Number of retry attempts after the initial request. Default 2. */
  maxRetries?: number;
  /** Base delay between retries (ms). Each retry doubles. Default 250ms. */
  retryBaseMs?: number;
  /** Inject your own fetch (e.g. node-fetch, msw). Default: globalThis.fetch. */
  fetch?: typeof fetch;
}

export interface JSONResponse<T> {
  status: number;
  body: T;
  raw: Record<string, unknown> | unknown;
}

export class Transport {
  private state: AuthState;
  private nodeURL: string;
  private infinityURL: string;
  private timeoutMs: number;
  private maxRetries: number;
  private retryBaseMs: number;
  private userAgent: string;
  private fetchImpl: typeof fetch;

  // Single-flight refresh: only one concurrent refresh per client.
  private inflightRefresh: Promise<void> | null = null;

  constructor(opts: TransportOptions) {
    this.state = {
      apiKey: opts.apiKey,
      username: opts.username,
      jwt: opts.jwt ?? '',
      refreshToken: opts.refreshToken ?? '',
    };
    this.nodeURL = (opts.nodeURL || opts.infinityURL).replace(/\/+$/, '');
    this.infinityURL = opts.infinityURL.replace(/\/+$/, '');
    this.timeoutMs = opts.timeoutMs ?? 60_000;
    this.maxRetries = opts.maxRetries ?? 2;
    this.retryBaseMs = opts.retryBaseMs ?? 250;
    this.userAgent = opts.userAgent ?? '@vxcloud/sdk';
    this.fetchImpl = opts.fetch ?? (globalThis.fetch?.bind(globalThis));
    if (!this.fetchImpl) {
      throw new Error('No fetch implementation available. Pass `fetch` via options on Node <18.');
    }
  }

  /** Update node URL after a node switch. */
  setNodeURL(url: string): void {
    this.nodeURL = url.replace(/\/+$/, '');
  }

  /** Get current auth state (read-only snapshot). */
  getAuthSnapshot(): Readonly<AuthState> {
    return { ...this.state };
  }

  /** POST JSON body. */
  async postJSON<T = unknown>(path: string, body: unknown, opts?: CallOptions): Promise<JSONResponse<T>> {
    return this.send<T>(path, {
      method: 'POST',
      body: JSON.stringify(body ?? {}),
      headers: { 'Content-Type': 'application/json' },
    }, opts);
  }

  /** GET. */
  async get<T = unknown>(path: string, opts?: CallOptions): Promise<JSONResponse<T>> {
    return this.send<T>(path, { method: 'GET' }, opts);
  }

  /** PATCH JSON body. */
  async patchJSON<T = unknown>(path: string, body: unknown, opts?: CallOptions): Promise<JSONResponse<T>> {
    return this.send<T>(path, {
      method: 'PATCH',
      body: JSON.stringify(body ?? {}),
      headers: { 'Content-Type': 'application/json' },
    }, opts);
  }

  /** DELETE. */
  async delete<T = unknown>(path: string, opts?: CallOptions): Promise<JSONResponse<T>> {
    return this.send<T>(path, { method: 'DELETE' }, opts);
  }

  /** PUT JSON body. */
  async putJSON<T = unknown>(path: string, body: unknown, opts?: CallOptions): Promise<JSONResponse<T>> {
    return this.send<T>(path, {
      method: 'PUT',
      body: JSON.stringify(body ?? {}),
      headers: { 'Content-Type': 'application/json' },
    }, opts);
  }

  /**
   * GET a binary endpoint and return the response as a Blob. Use this for
   * dataset/embedding download where the body is a zip/csv rather than JSON.
   * The standard `get()` would attempt JSON parsing and fail on raw bytes.
   */
  async getBlob(path: string, opts?: CallOptions): Promise<Blob> {
    const url = this.absoluteURL(path);
    const baseHeaders: Record<string, string> = {
      'Accept': 'application/octet-stream, */*',
      'User-Agent': this.userAgent,
      ...authHeaders(this.state),
    };
    const init: RequestInit = {
      method: 'GET',
      headers: { ...baseHeaders, ...(opts?.headers ?? {}) },
    };
    const res = await this.fetchImpl(url, init);
    if (!res.ok) {
      const body = await res.text().catch(() => '');
      throw new Error(`getBlob ${path}: HTTP ${res.status}: ${body.slice(0, 400)}`);
    }
    return res.blob();
  }

  /**
   * Multipart form POST. Pass plain string fields and optional file parts.
   * The browser/Node FormData handles boundary generation.
   *
   * `files` items can be either:
   *   - `{ field, filename, content: Uint8Array | Blob }`
   *   - `{ field, filename, text: string }`
   */
  async postMultipart<T = unknown>(
    path: string,
    fields: Record<string, string>,
    files: MultipartFile[] = [],
    opts?: CallOptions,
  ): Promise<JSONResponse<T>> {
    const fd = new FormData();
    for (const [k, v] of Object.entries(fields)) fd.append(k, v);
    for (const f of files) {
      if ('content' in f) {
        const blob = f.content instanceof Blob
          ? f.content
          : new Blob([f.content as BlobPart], { type: f.contentType ?? 'application/octet-stream' });
        fd.append(f.field, blob, f.filename);
      } else {
        fd.append(f.field, new Blob([f.text], { type: f.contentType ?? 'text/plain' }), f.filename);
      }
    }
    return this.send<T>(path, { method: 'POST', body: fd }, opts);
  }

  // ──────────────────────────────────────────────────────────────────
  // Internals
  // ──────────────────────────────────────────────────────────────────

  private absoluteURL(path: string): string {
    if (path.startsWith('http://') || path.startsWith('https://')) return path;
    // /api/v1/auth/* goes to the infinity (control-plane) URL; everything
    // else (deploys, sessions, services) goes to the active tenant node.
    if (path.startsWith('/api/v1/auth/')) return this.infinityURL + path;
    return this.nodeURL + path;
  }

  private async send<T>(
    path: string,
    init: RequestInit,
    opts?: CallOptions,
  ): Promise<JSONResponse<T>> {
    const url = this.absoluteURL(path);
    const baseHeaders: Record<string, string> = {
      'Accept': 'application/json',
      'User-Agent': this.userAgent,
      ...authHeaders(this.state),
    };
    const initHeaders = (init.headers as Record<string, string> | undefined) ?? {};
    // Extra per-call headers (e.g. X-Tenant-ID for agentcontrol) merge LAST
    // because they're caller-supplied overrides; auth headers re-merge on
    // 401 refresh below so they always win against stale auth.
    const extraHeaders = opts?.headers ?? {};
    init.headers = { ...baseHeaders, ...initHeaders, ...extraHeaders };

    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), this.timeoutMs);
    if (opts?.signal) opts.signal.addEventListener('abort', () => ctrl.abort(), { once: true });

    try {
      let attempt = 0;
      let didRefresh = false;
      // eslint-disable-next-line no-constant-condition
      while (true) {
        attempt += 1;
        let res: Response;
        try {
          res = await this.fetchImpl(url, { ...init, signal: ctrl.signal });
        } catch (err) {
          const netErr = new VxNetworkError({ status: 0, message: (err as Error).message ?? 'network error', source: 'transport', cause: err });
          if (attempt <= this.maxRetries && isRetryable(netErr)) {
            await sleep(this.retryDelay(attempt));
            continue;
          }
          throw netErr;
        }

        if (res.status === 401 && !didRefresh && this.state.apiKey) {
          await this.refresh(ctrl.signal);
          didRefresh = true;
          // Re-attach refreshed auth headers, retry the same request.
          init.headers = { ...(init.headers as Record<string, string>), ...authHeaders(this.state) };
          continue;
        }

        if (!res.ok) {
          const body = await this.readBody(res);
          const err = fromHTTP(res.status, body, 'transport', res.headers.get('Retry-After'));
          if (attempt <= this.maxRetries && isRetryable(err)) {
            await sleep(this.retryDelay(attempt));
            continue;
          }
          throw err;
        }

        const body = await this.readBody(res);
        return { status: res.status, body: body as T, raw: body };
      }
    } finally {
      clearTimeout(t);
    }
  }

  private async readBody(res: Response): Promise<unknown> {
    const ct = res.headers.get('Content-Type') ?? '';
    if (ct.includes('application/json')) {
      try { return await res.json(); } catch { return null; }
    }
    return await res.text();
  }

  private retryDelay(attempt: number): number {
    const base = this.retryBaseMs * 2 ** (attempt - 1);
    const jitter = Math.floor(Math.random() * (this.retryBaseMs / 2));
    return base + jitter;
  }

  /**
   * Single-flight refresh. If a refresh is already in-flight, wait for it.
   * Otherwise start one and store the promise so concurrent callers wait.
   */
  private async refresh(signal?: AbortSignal): Promise<void> {
    if (this.inflightRefresh) return this.inflightRefresh;
    this.inflightRefresh = (async () => {
      try {
        const fresh = await exchangeKey(
          this.fetchImpl, this.infinityURL,
          this.state.apiKey, this.state.username, signal,
        );
        this.state.jwt = fresh.jwt;
        this.state.refreshToken = fresh.refreshToken || this.state.refreshToken;
        this.state.expiresAt = fresh.expiresAt;
        this.state.user = fresh.user;
      } finally {
        this.inflightRefresh = null;
      }
    })();
    return this.inflightRefresh;
  }
}

/** Per-call options accepted by every transport method. */
export interface CallOptions {
  signal?: AbortSignal;
  /** Extra HTTP headers merged after auth + content-type. Used for
   *  X-Tenant-ID on agentcontrol calls and other resource-scoped headers. */
  headers?: Record<string, string>;
}

export type MultipartFile =
  | { field: string; filename: string; content: Uint8Array | Blob; contentType?: string }
  | { field: string; filename: string; text: string; contentType?: string };

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
