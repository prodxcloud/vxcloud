package vxsdk

import (
	"net/http"
	"time"
)

// Option configures a Client. Use WithAPIKey, WithJWT, WithBaseURL, etc.
type Option func(*config)

type config struct {
	apiKey      string
	username    string
	jwt         string
	refreshJWT  string
	infinityURL string
	nodeURL     string
	tenantID    string
	httpClient  *http.Client
	timeout     time.Duration
	userAgent   string
}

// WithAPIKey authenticates the client using a developer API key
// ("xc_dev_*", "xc_test_*", or "xc_live_*"). The key is exchanged for
// a short-lived JWT on first call and cached for the lifetime of the Client.
func WithAPIKey(apiKey string) Option {
	return func(c *config) { c.apiKey = apiKey }
}

// WithUsername pins the workspace username. If unset, the SDK derives it
// from the API key exchange response.
func WithUsername(u string) Option {
	return func(c *config) { c.username = u }
}

// WithJWT authenticates with an existing JWT pair. Skips the API key exchange.
func WithJWT(access, refresh string) Option {
	return func(c *config) { c.jwt = access; c.refreshJWT = refresh }
}

// WithInfinityURL overrides the control-plane base URL (default https://api.vxcloud.io).
func WithInfinityURL(u string) Option {
	return func(c *config) { c.infinityURL = u }
}

// WithNodeURL overrides the tenant-node base URL. If unset, it is resolved
// from the API key exchange response (one round trip on first call).
func WithNodeURL(u string) Option {
	return func(c *config) { c.nodeURL = u }
}

// WithHTTPClient supplies a custom *http.Client. Use this to inject a
// transport with mTLS, custom proxies, or test fakes. The SDK still applies
// its own retry/timeout policy on top.
func WithHTTPClient(h *http.Client) Option {
	return func(c *config) { c.httpClient = h }
}

// WithTimeout sets the per-request timeout (default 30s).
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithUserAgent overrides the User-Agent header (default "vxsdk-go/<version>").
func WithUserAgent(ua string) Option {
	return func(c *config) { c.userAgent = ua }
}

// WithTenantID pins the tenant id used for the agentcontrol surface
// (the X-Tenant-ID header). LoadFromVxcli populates this automatically
// from credentials.json when present.
func WithTenantID(id string) Option {
	return func(c *config) { c.tenantID = id }
}
