/**
 * Typed error tree. Mirrors `errors/errors.go` and `vxsdk.VxError` in
 * the Python SDK so consumers can `instanceof`-discriminate the same
 * categories everywhere.
 *
 * The transport layer maps HTTP status codes → these classes:
 *   401, 403         → VxAuthError
 *   400, 422         → VxValidationError
 *   404              → VxNotFoundError
 *   429              → VxRateLimitError
 *   5xx              → VxServerError
 *   network failure  → VxNetworkError
 *
 * IsRetryable returns true for VxNetworkError, VxServerError, and
 * VxRateLimitError. The transport's retry policy uses it.
 */

export interface VxErrorPayload {
  /** HTTP status code, or 0 for network errors. */
  status: number;
  /** Server-supplied error code, if any (e.g. 'invalid_request'). */
  code?: string;
  /** Human-readable message. */
  message: string;
  /** Where in the SDK the error originated (module name). */
  source?: string;
  /** Raw response body (string or parsed JSON), for debugging. */
  body?: unknown;
  /** Underlying cause (e.g. fetch TypeError). */
  cause?: unknown;
}

export class VxError extends Error {
  status: number;
  code?: string;
  source?: string;
  body?: unknown;
  override cause?: unknown;

  constructor(payload: VxErrorPayload) {
    super(payload.message);
    this.name = 'VxError';
    this.status = payload.status;
    this.code = payload.code;
    this.source = payload.source;
    this.body = payload.body;
    this.cause = payload.cause;
  }
}

export class VxAuthError extends VxError {
  constructor(p: VxErrorPayload) { super(p); this.name = 'VxAuthError'; }
}
export class VxValidationError extends VxError {
  constructor(p: VxErrorPayload) { super(p); this.name = 'VxValidationError'; }
}
export class VxNotFoundError extends VxError {
  constructor(p: VxErrorPayload) { super(p); this.name = 'VxNotFoundError'; }
}
export class VxRateLimitError extends VxError {
  retryAfterSeconds?: number;
  constructor(p: VxErrorPayload & { retryAfterSeconds?: number }) {
    super(p); this.name = 'VxRateLimitError';
    this.retryAfterSeconds = p.retryAfterSeconds;
  }
}
export class VxServerError extends VxError {
  constructor(p: VxErrorPayload) { super(p); this.name = 'VxServerError'; }
}
export class VxNetworkError extends VxError {
  constructor(p: VxErrorPayload) { super(p); this.name = 'VxNetworkError'; }
}

/** Map an HTTP response → typed VxError. */
export function fromHTTP(
  status: number,
  body: unknown,
  source?: string,
  retryAfterHeader?: string | null,
): VxError {
  const message = extractMessage(body) ?? `HTTP ${status}`;
  const code = extractCode(body);
  const base = { status, message, source, body, code };

  if (status === 401 || status === 403) return new VxAuthError(base);
  if (status === 404) return new VxNotFoundError(base);
  if (status === 429) {
    const ra = retryAfterHeader ? Number.parseInt(retryAfterHeader, 10) : undefined;
    return new VxRateLimitError({ ...base, retryAfterSeconds: Number.isFinite(ra) ? ra : undefined });
  }
  if (status >= 400 && status < 500) return new VxValidationError(base);
  if (status >= 500) return new VxServerError(base);
  return new VxError(base);
}

/** Whether a request that produced this error should be retried. */
export function isRetryable(err: unknown): boolean {
  return err instanceof VxNetworkError
    || err instanceof VxServerError
    || err instanceof VxRateLimitError;
}

function extractMessage(body: unknown): string | undefined {
  if (!body) return undefined;
  if (typeof body === 'string') return body;
  if (typeof body === 'object' && body !== null) {
    const b = body as Record<string, unknown>;
    return (typeof b.error === 'string' ? b.error : undefined)
      ?? (typeof b.message === 'string' ? b.message : undefined)
      ?? (typeof b.detail === 'string' ? b.detail : undefined);
  }
  return undefined;
}

function extractCode(body: unknown): string | undefined {
  if (typeof body !== 'object' || body === null) return undefined;
  const b = body as Record<string, unknown>;
  return typeof b.code === 'string' ? b.code : undefined;
}
