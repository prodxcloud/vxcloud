package transport

import (
	"strings"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	vxerrors "github.com/prodxcloud/vxcloud/errors"
)

// FilePart describes one in-memory file to attach to a multipart upload.
// Path-on-disk uploads are intentionally not supported — callers should
// read the bytes themselves so the SDK never touches the filesystem on
// behalf of the caller.
type FilePart struct {
	// Field is the form field name (e.g. "script_file", "compose_file").
	Field string
	// Filename is the basename surfaced to the server (e.g. "install.sh").
	Filename string
	// Content is the raw bytes of the file.
	Content []byte
	// ContentType is optional; defaults to application/octet-stream.
	ContentType string
}

// Multipart performs a multipart/form-data POST to url with the given
// string fields and file parts, then decodes the JSON response into out.
//
// Used by /api/v2/tenant/install/script, /api/v2/tenant/container/deploy,
// /api/v2/tenant/provision/docker-compose/custom — every backend handler
// that accepts SSH-flag form fields plus optional file uploads.
func (t *Transport) Multipart(
	ctx context.Context,
	op, url string,
	fields map[string]string,
	files []FilePart,
	out any,
) error {
	return t.MultipartWithHeaders(ctx, op, url, fields, files, out, nil)
}

// MultipartWithHeaders is Multipart with extra request headers merged in
// (e.g. X-Tenant-ID for agentcontrol dataset uploads). The transport's own
// headers are authoritative and must not be supplied here.
func (t *Transport) MultipartWithHeaders(
	ctx context.Context,
	op, url string,
	fields map[string]string,
	files []FilePart,
	out any,
	headers map[string]string,
) error {
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
		body, contentType, err := buildMultipart(fields, files)
		if err != nil {
			return &vxerrors.Failure{Op: op, Message: "build multipart body", Cause: err}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
		if err != nil {
			return &vxerrors.Failure{Op: op, Message: "build request", Cause: err}
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "application/json")
		if t.UserAgent != "" {
			req.Header.Set("User-Agent", t.UserAgent)
		}
		req.Header.Set("vx-request-id", newRequestID())
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		if t.Authorization != nil {
			bearer, apiKey := t.Authorization()
			if bearer != "" {
				req.Header.Set("Authorization", "Bearer "+bearer)
			}
			// Drop X-API-Key for node-targeted requests when a Bearer JWT is
			// present (see Transport.NodeURL). Avoids the node middleware
			// strict-comparing a stale X-API-Key against its Vault-cached key.
			targetsNode := t.NodeURL != "" && strings.HasPrefix(req.URL.String(), t.NodeURL)
			if apiKey != "" && !(bearer != "" && targetsNode) {
				req.Header.Set("X-API-Key", apiKey)
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

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusUnauthorized && t.RefreshOn401 != nil && !refreshed {
			refreshed = true
			if rerr := t.RefreshOn401(ctx); rerr == nil {
				continue
			}
		}

		mapped := vxerrors.FromHTTP(op, resp.StatusCode, http.StatusText(resp.StatusCode), trim(string(respBody), 800))
		if resp.StatusCode == http.StatusTooManyRequests {
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

// buildMultipart writes a multipart/form-data body. Returned io.Reader is
// safe to send once; for retries, this function is called again.
func buildMultipart(fields map[string]string, files []FilePart) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}
	for _, f := range files {
		fw, err := w.CreateFormFile(f.Field, f.Filename)
		if err != nil {
			return nil, "", err
		}
		if _, err := fw.Write(f.Content); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}
