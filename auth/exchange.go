package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Doer is the minimal HTTP client interface needed for an exchange call.
// *http.Client satisfies it.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// ErrInvalidKey is returned when the API key is malformed or rejected.
var ErrInvalidKey = errors.New("auth: invalid api key")

// Exchange trades an API key for a JWT pair against the Infinity control
// plane. Endpoint: POST {infinityURL}/api/v1/auth/developer/keys/login.
//
// Returns the parsed response, including resolved Username / Organization
// / Workspace from the server (callers can omit username on input — the
// server identifies the key holder).
func Exchange(ctx context.Context, doer Doer, infinityURL, apiKey, username string) (*ExchangeResponse, error) {
	if err := APIKey(apiKey).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	body, _ := json.Marshal(map[string]string{
		"api_key":  apiKey,
		"username": username,
	})
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		strings.TrimRight(infinityURL, "/")+"/api/v1/auth/developer/keys/login",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth.Exchange: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		var out ExchangeResponse
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("auth.Exchange: decode response: %w", err)
		}
		if out.Access == "" {
			return nil, fmt.Errorf("auth.Exchange: server returned no access token")
		}
		return &out, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%w: rejected by control plane (HTTP %d)", ErrInvalidKey, resp.StatusCode)
	default:
		return nil, fmt.Errorf("auth.Exchange: unexpected status %d: %s", resp.StatusCode, trim(respBody, 200))
	}
}

func trim(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
