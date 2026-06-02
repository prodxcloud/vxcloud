// Package auth holds the authentication contract for the SDK.
//
// Two credentials are supported:
//
//   - APIKey: a developer API key issued by the vxcloud dashboard, prefixed
//     "xc_dev_" / "xc_test_" / "xc_live_". Exchanged for a JWT on first call.
//   - Token: an existing JWT pair (access + refresh) — useful when the SDK
//     is embedded in a process that has already authenticated by other means.
//
// The contract matches what FastAPI accepts on the backend today, so this
// SDK does not require any server-side change to be useful.
package auth

import (
	"errors"
	"strings"
	"time"
)

// APIKey is a developer API key.
type APIKey string

// Validate reports whether the key has the expected shape. It does NOT
// contact the server — the network check happens during the exchange call.
func (k APIKey) Validate() error {
	s := string(k)
	if !strings.HasPrefix(s, "xc_") {
		return errors.New("api key must start with xc_")
	}
	parts := strings.SplitN(s, "_", 3)
	if len(parts) != 3 {
		return errors.New("api key format: xc_<env>_<token>")
	}
	switch parts[1] {
	case "dev", "test", "live":
	default:
		return errors.New("api key environment must be dev, test, or live")
	}
	if len(parts[2]) < 16 {
		return errors.New("api key token segment too short")
	}
	return nil
}

// Environment returns "dev", "test", or "live" for a well-formed key.
func (k APIKey) Environment() string {
	parts := strings.SplitN(string(k), "_", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// Token is a JWT pair returned by the Infinity control plane.
type Token struct {
	Access    string    `json:"access"`
	Refresh   string    `json:"refresh"`
	IssuedAt  time.Time `json:"issued_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// IsExpired returns true when the access token is past its expiration with
// a small safety margin. Zero ExpiresAt is treated as not-expired (caller
// will discover via 401).
func (t Token) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(30 * time.Second).After(t.ExpiresAt)
}

// User describes the authenticated principal. Populated from the API key
// exchange response.
type User struct {
	Username     string `json:"username"`
	Email        string `json:"email,omitempty"`
	Organization string `json:"organization,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
}

// ExchangeResponse is the wire shape of POST /api/v1/auth/developer/keys/login.
type ExchangeResponse struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	KeyName string `json:"key_name"`
	User    struct {
		Username     string `json:"username"`
		Email        string `json:"email"`
		Organization *struct {
			Name string `json:"name"`
		} `json:"organization,omitempty"`
		Workspace *struct {
			Name string `json:"name"`
		} `json:"workspace,omitempty"`
	} `json:"user"`
}

// State is the in-memory authentication state held by the Client.
// It is concurrency-safe via the surrounding sync.RWMutex in client.go.
type State struct {
	APIKey APIKey
	Token  Token
	User   User
	// NodeURL is the per-tenant node base URL resolved during exchange.
	NodeURL string
}
