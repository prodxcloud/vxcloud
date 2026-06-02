/**
 * Auth state + API-key validation + JWT exchange.
 *
 * Same wire contract as services/sdk/auth (Go) and vxsdk.py:
 *   POST {infinity}/api/v1/auth/developer/keys/login
 *     Headers: X-API-Key: xc_…
 *     Body:    { username, key }
 *   →
 *     { jwt, expires_at, user, refresh_token }
 *
 * The exchange runs lazily on first protected call and re-runs on a 401.
 * A single in-flight refresh is reused across concurrent callers
 * (single-flight) to avoid thundering-herd against the auth endpoint.
 */

import { VxAuthError } from './errors.js';

export interface AuthState {
  apiKey: string;
  username: string;
  jwt: string;
  refreshToken: string;
  expiresAt?: number; // unix seconds
  user?: { id?: string; username?: string; email?: string };
}

export interface ExchangeResponse {
  jwt?: string;
  access_token?: string;
  refresh_token?: string;
  expires_at?: number;
  user?: { id?: string; username?: string; email?: string };
}

/**
 * Validate the shape of an API key. The platform uses three envelopes:
 *   xc_dev_<token>, xc_test_<token>, xc_live_<token>.
 * Token segment must be at least 16 characters.
 */
export function validateApiKey(key: string): void {
  if (!key.startsWith('xc_')) {
    throw new VxAuthError({ status: 0, message: 'api key must start with xc_', source: 'auth.validateApiKey' });
  }
  const parts = key.split('_');
  if (parts.length < 3) {
    throw new VxAuthError({ status: 0, message: 'api key format: xc_<env>_<token>', source: 'auth.validateApiKey' });
  }
  if (!['dev', 'test', 'live'].includes(parts[1] ?? '')) {
    throw new VxAuthError({ status: 0, message: 'api key environment must be dev|test|live', source: 'auth.validateApiKey' });
  }
  const token = parts.slice(2).join('_');
  if (token.length < 16) {
    throw new VxAuthError({ status: 0, message: 'api key token segment too short', source: 'auth.validateApiKey' });
  }
}

/**
 * Build the headers attached to every request.
 * `X-API-Key` is always present; `Authorization: Bearer …` is added once
 * the JWT exchange has succeeded.
 */
export function authHeaders(state: AuthState): Record<string, string> {
  const h: Record<string, string> = {};
  if (state.apiKey) h['X-API-Key'] = state.apiKey;
  if (state.jwt) h['Authorization'] = `Bearer ${state.jwt}`;
  return h;
}

/**
 * One-shot JWT exchange. Caller owns single-flight semantics.
 */
export async function exchangeKey(
  fetchImpl: typeof fetch,
  infinityURL: string,
  apiKey: string,
  username: string,
  signal?: AbortSignal,
): Promise<{ jwt: string; refreshToken: string; expiresAt?: number; user?: AuthState['user'] }> {
  const res = await fetchImpl(`${infinityURL.replace(/\/+$/, '')}/api/v1/auth/developer/keys/login`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Accept': 'application/json',
      'X-API-Key': apiKey,
    },
    body: JSON.stringify({ username, key: apiKey }),
    signal,
  });

  if (!res.ok) {
    let body: unknown;
    try { body = await res.json(); } catch { body = await res.text(); }
    throw new VxAuthError({
      status: res.status, source: 'auth.exchangeKey',
      message: `auth exchange failed: HTTP ${res.status}`, body,
    });
  }

  const data = (await res.json()) as ExchangeResponse;
  const jwt = data.jwt ?? data.access_token ?? '';
  const refreshToken = data.refresh_token ?? '';
  if (!jwt) {
    throw new VxAuthError({
      status: res.status, source: 'auth.exchangeKey',
      message: 'auth exchange returned empty jwt', body: data,
    });
  }
  return { jwt, refreshToken, expiresAt: data.expires_at, user: data.user };
}
