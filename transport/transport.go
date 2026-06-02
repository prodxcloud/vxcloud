// Package transport is the SDK's HTTP layer. It is internal — callers
// should use the resource modules on the Client (client.Sessions().List(), etc.)
// rather than constructing requests directly.
//
// Responsibilities:
//   - One *http.Client per SDK Client (connection reuse).
//   - Inject Authorization: Bearer + X-API-Key headers when present.
//   - Inject vx-request-id and traceparent for end-to-end tracing.
//   - Convert non-2xx responses into the typed error hierarchy in
//     errors/errors.go.
//   - Bounded retry on NetworkError / 5xx / 429 (exponential backoff).
package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	vxerrors "github.com/prodxcloud/vxcloud/errors"
)

// Doer is the minimal HTTP client interface the transport layer needs.
// *http.Client satisfies it; tests can substitute a fake.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Transport wraps a Doer with auth header injection and typed error mapping.
type Transport struct {
	Doer          Doer
	UserAgent     string
	Authorization func() (bearer, apiKey string) // called per-request; nil means anonymous
	// NodeURL, when set, identifies the tenant-node base URL. For requests
	// whose URL starts with NodeURL and where a Bearer JWT is present, the
	// X-API-Key header is suppressed. The node developer-key middleware
	// accepts a lone Bearer, but a stale or cross-workspace X-API-Key sent
	// alongside the JWT makes it strict-compare and 403 ("not valid for
	// this workspace"). Control-plane (Infinity) requests keep X-API-Key.
	NodeURL string
	// RefreshOn401, if non-nil, is invoked exactly once per request when a
	// 401 response is received. On success the request is replayed with the
	// updated Authorization headers. Use this to wire automatic token
	// refresh from a long-lived API key.
	RefreshOn401 func(ctx context.Context) error
	MaxRetries   int           // default 3 if zero
	BaseDelay    time.Duration // default 200ms if zero
}

// JSON performs an HTTP request with a JSON body (if body != nil) and
// decodes the JSON response into out (if out != nil). The op string is
// used in error messages and traces.
func (t *Transport) JSON(ctx context.Context, op, method, url string, body, out any) error {
	return t.JSONWithHeaders(ctx, op, method, url, body, out, nil)
}

// JSONWithHeaders is JSON with extra request headers merged in — e.g.
// X-Tenant-ID for the agentcontrol surface. The transport's own headers
// (Authorization, X-API-Key, Content-Type, Accept, User-Agent,
// vx-request-id) are authoritative and must not be supplied here.
func (t *Transport) JSONWithHeaders(ctx context.Context, op, method, url string, body, out any, headers map[string]string) error {
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return &vxerrors.Failure{Op: op, Message: "marshal request body", Cause: err}
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &vxerrors.Failure{Op: op, Message: "build request", Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if t.UserAgent != "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}
	req.Header.Set("vx-request-id", newRequestID())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Authorization headers are set per-attempt in the loop below so
	// they pick up any rotation done by RefreshOn401.

	maxRetries := t.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	base := t.BaseDelay
	if base == 0 {
		base = 200 * time.Millisecond
	}

	var lastErr error
	refreshed := false
	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Rewind body for retries.
		if attempt > 0 && body != nil {
			buf, _ := json.Marshal(body)
			req.Body = io.NopCloser(bytes.NewReader(buf))
		}
		// Refresh authorization headers each attempt — they may have been
		// rotated by a RefreshOn401 callback.
		if t.Authorization != nil {
			bearer, apiKey := t.Authorization()
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			} else {
				req.Header.Del("Authorization")
			}
			// Drop X-API-Key for node-targeted requests when a Bearer JWT is
			// present (see NodeURL doc). This avoids the node middleware
			// strict-comparing a stale X-API-Key against its Vault-cached key.
			targetsNode := t.NodeURL != "" && strings.HasPrefix(req.URL.String(), t.NodeURL)
			if apiKey != "" && !(bearer != "" && targetsNode) {
				req.Header.Set("X-API-Key", apiKey)
			} else {
				req.Header.Del("X-API-Key")
			}
		}
		resp, err := t.Doer.Do(req)
		if err != nil {
			lastErr = &vxerrors.NetworkError{Failure: &vxerrors.Failure{Op: op, Message: "transport", Cause: err}}
			if !sleepOrCtx(ctx, backoff(attempt, base)) {
				return ctx.Err()
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			if out == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				return nil
			}
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
				return &vxerrors.Failure{Op: op, Message: "decode response", Cause: err}
			}
			return nil
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		// One-shot token refresh on 401. We only attempt this once per
		// JSON() call to avoid loops if refresh itself returns a stale key.
		if resp.StatusCode == http.StatusUnauthorized && t.RefreshOn401 != nil && !refreshed {
			refreshed = true
			if rerr := t.RefreshOn401(ctx); rerr == nil {
				continue
			}
			// fall through and surface the original 401
		}

		mapped := vxerrors.FromHTTP(op, resp.StatusCode, http.StatusText(resp.StatusCode), trim(string(bodyBytes), 800))

		// Apply Retry-After hint from rate-limit responses.
		if resp.StatusCode == 429 {
			if hdr := resp.Header.Get("Retry-After"); hdr != "" {
				if secs, err := strconv.Atoi(hdr); err == nil {
					if rl, ok := mapped.(*vxerrors.RateLimitError); ok {
						rl.RetryAfter = secs
					}
				}
			}
		}

		if !vxerrors.IsRetryable(mapped) || attempt == maxRetries {
			return mapped
		}
		lastErr = mapped
		if !sleepOrCtx(ctx, backoff(attempt, base)) {
			return ctx.Err()
		}
	}
	return lastErr
}

func backoff(attempt int, base time.Duration) time.Duration {
	d := base
	for i := 0; i < attempt; i++ {
		d *= 2
	}
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

func sleepOrCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// joinURL concatenates a base and path with exactly one slash separator.
// Exposed because resource modules call it.
func JoinURL(base, path string) string {
	if base == "" {
		return path
	}
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if len(path) == 0 || path[0] != '/' {
		return base + "/" + path
	}
	return base + path
}

// Sentinel for callers that want to hint a path is unsupported.
var ErrUnsupported = fmt.Errorf("vxsdk: operation not supported by this server")

// BytesWithHeaders performs a non-JSON HTTP request and returns the raw
// response body. Used by binary endpoints like dataset/embedding downloads
// where the body is a zip/tar/csv rather than JSON. Auth, retry, refresh,
// User-Agent and request-id behavior mirrors JSONWithHeaders.
func (t *Transport) BytesWithHeaders(ctx context.Context, op, method, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, &vxerrors.Failure{Op: op, Message: "build request", Cause: err}
	}
	req.Header.Set("Accept", "application/octet-stream, */*")
	if t.UserAgent != "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}
	req.Header.Set("vx-request-id", newRequestID())
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	maxRetries := t.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	base := t.BaseDelay
	if base == 0 {
		base = 200 * time.Millisecond
	}

	var lastErr error
	refreshed := false
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if t.Authorization != nil {
			bearer, apiKey := t.Authorization()
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			} else {
				req.Header.Del("Authorization")
			}
			targetsNode := t.NodeURL != "" && strings.HasPrefix(req.URL.String(), t.NodeURL)
			if apiKey != "" && !(bearer != "" && targetsNode) {
				req.Header.Set("X-API-Key", apiKey)
			} else {
				req.Header.Del("X-API-Key")
			}
		}
		resp, err := t.Doer.Do(req)
		if err != nil {
			lastErr = &vxerrors.NetworkError{Failure: &vxerrors.Failure{Op: op, Message: "transport", Cause: err}}
			if !sleepOrCtx(ctx, backoff(attempt, base)) {
				return nil, ctx.Err()
			}
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		if resp.StatusCode == http.StatusUnauthorized && t.RefreshOn401 != nil && !refreshed {
			refreshed = true
			if rerr := t.RefreshOn401(ctx); rerr == nil {
				continue
			}
		}
		mapped := vxerrors.FromHTTP(op, resp.StatusCode, http.StatusText(resp.StatusCode), trim(string(body), 800))
		if !vxerrors.IsRetryable(mapped) || attempt == maxRetries {
			return nil, mapped
		}
		lastErr = mapped
		if !sleepOrCtx(ctx, backoff(attempt, base)) {
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}
