package salesshift

// The Lead Verifier — explainable, on-demand email verification.
//
// Endpoints (VxCloud control plane — FastAPI):
//
//	GET  /api/v1/salesshift/verifier/status   engine health (cached 60 s server-side)
//	POST /api/v1/salesshift/verifier/check    {"email"} → full report
//
// (POST /verifier/stream serves the same report as Server-Sent Events; the
// SDK uses the plain JSON route. `vxcli salesshift verify` streams.)
//
// Each check runs syntax, domain, MX, provider, disposable/role, SPF/DMARC,
// a live SMTP dialogue that stops at RCPT TO (no message is ever created) and
// a catch-all canary. It takes 2–20 s, so keep the client timeout (default
// 30 s, see vxcloud.WithTimeout) above that.
//
// Limits: 12 checks per user per minute, 150 per workspace per hour. A
// refusal is HTTP 429 with a `detail` saying which window is full; it
// surfaces as *errors.RateLimitError. Batch callers should space checks
// about 5.1 s apart rather than discover the limit.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/prodxcloud/vxcloud/transport"
)

// Verdicts a VerifyReport can carry.
const (
	VerdictVerified     = "verified"     // the mailbox exists and accepts mail
	VerdictRisky        = "risky"        // deliverable with caveats (catch-all, role, full, disposable)
	VerdictNotVerified  = "not_verified" // mail to it would bounce
	VerdictInconclusive = "inconclusive" // the server would not give a straight answer
)

// Verifier limits enforced by the server.
const (
	VerifierPerUserPerMinute    = 12
	VerifierPerWorkspacePerHour = 150
)

// VerifyFact is one piece of evidence behind a step ("RCPT TO" → "250 OK").
type VerifyFact struct {
	K string `json:"k"`
	V string `json:"v"`
}

// VerifyStep is one check in the pipeline, in the order it ran.
type VerifyStep struct {
	Key     string       `json:"key"`    // syntax | domain | mx | provider | hygiene | auth | smtp | catch_all
	Label   string       `json:"label"`  // human label, e.g. "Live mailbox check (SMTP)"
	Status  string       `json:"status"` // pass | warn | fail | info | skip
	Summary string       `json:"summary"`
	Facts   []VerifyFact `json:"facts"`
	Ms      int          `json:"ms"`
}

// VerifyProvider is who hosts the mailbox.
type VerifyProvider struct {
	Name string `json:"name"` // "Google Workspace", "Outlook.com (Microsoft consumer)", …
	Kind string `json:"kind"` // mailbox | gateway
	MX   string `json:"mx"`
}

// VerifyAuth is the domain's published sender authentication.
type VerifyAuth struct {
	SPF   bool   `json:"spf"`
	DMARC string `json:"dmarc"` // the DMARC policy (none | quarantine | reject), "" when unpublished
}

// VerifySMTP is the live dialogue's endpoint.
type VerifySMTP struct {
	Host     string `json:"host"`
	Ms       int    `json:"ms"`
	Via      string `json:"via"`
	EgressIP string `json:"egress_ip"`
}

// VerifyReport is the full, explained verdict for one address. Provider,
// Auth and SMTP are nil when the pipeline stopped before reaching them (a
// malformed address never gets an MX lookup).
type VerifyReport struct {
	Email        string          `json:"email"`
	Normalized   string          `json:"normalized"`
	Domain       string          `json:"domain"`
	Provider     *VerifyProvider `json:"provider"`
	FreeProvider bool            `json:"free_provider"`
	Role         bool            `json:"role"`
	Disposable   bool            `json:"disposable"`
	// CatchAll is nil when the canary got no answer either way.
	CatchAll   *bool        `json:"catch_all"`
	Auth       *VerifyAuth  `json:"auth"`
	SMTP       *VerifySMTP  `json:"smtp"`
	Status     string       `json:"status"`  // valid | invalid | risky | unknown
	Detail     string       `json:"detail"`  // mailbox_accepted | mailbox_not_found | accept_all_domain | blocked_by_policy | …
	Verdict    string       `json:"verdict"` // see the Verdict* constants
	Score      int          `json:"score"`   // 0–100
	Grade      string       `json:"grade"`   // A–F
	Headline   string       `json:"headline"`
	Reasons    []string     `json:"reasons"`
	Steps      []VerifyStep `json:"steps"`
	DurationMs int          `json:"duration_ms"`
	CheckedAt  string       `json:"checked_at"`
}

// Deliverable reports whether the verdict is safe to send to (verified or
// risky). Inconclusive is NOT deliverable — it is unproven, not good.
func (r *VerifyReport) Deliverable() bool {
	return r != nil && (r.Verdict == VerdictVerified || r.Verdict == VerdictRisky)
}

// VerifyEmail runs one explained verification. It talks to the real mail
// server, takes 2–20 s and spends one of the per-minute / per-hour checks.
func (c *Client) VerifyEmail(ctx context.Context, email string) (*VerifyReport, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, errors.New("salesshift.VerifyEmail: email is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/verifier/check")
	var raw struct {
		Success bool          `json:"success"`
		Data    *VerifyReport `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.VerifyEmail", "POST", url, map[string]string{"email": email}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.VerifyEmail: %w", err)
	}
	if raw.Data == nil {
		return nil, errors.New("salesshift.VerifyEmail: the server answered without a report")
	}
	return raw.Data, nil
}

// VerifierCheck is one engine-health probe.
type VerifierCheck struct {
	Key     string `json:"key"` // dns | google | microsoft
	Label   string `json:"label"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Ms      int    `json:"ms"`
	Blocked bool   `json:"blocked"` // the probe IP is on a blocklist at that provider
}

// VerifierLimits are the server's metering windows.
type VerifierLimits struct {
	PerUserPerMinute    int `json:"per_user_per_minute"`
	PerWorkspacePerHour int `json:"per_workspace_per_hour"`
}

// VerifierStatus says whether the engine can give real answers right now.
// When a provider check is not OK, addresses hosted there come back
// "inconclusive" rather than wrong.
type VerifierStatus struct {
	ProbeFrom string          `json:"probe_from"`
	Egress    bool            `json:"egress"`
	Healthy   bool            `json:"healthy"`
	Checks    []VerifierCheck `json:"checks"`
	CheckedAt string          `json:"checked_at"`
	Limits    VerifierLimits  `json:"limits"`
	// Cached is true when the server answered from its 60 s cache.
	Cached bool `json:"-"`
}

// VerifierStatus probes DNS and port-25 reachability at Google and
// Microsoft from the probing network. A fresh run takes up to ~15 s.
func (c *Client) VerifierStatus(ctx context.Context) (*VerifierStatus, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/verifier/status")
	var raw struct {
		Success bool            `json:"success"`
		Data    *VerifierStatus `json:"data"`
		Cached  bool            `json:"cached"`
	}
	if err := c.T.JSON(ctx, "salesshift.VerifierStatus", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.VerifierStatus: %w", err)
	}
	if raw.Data == nil {
		return nil, errors.New("salesshift.VerifierStatus: the server answered without a status")
	}
	raw.Data.Cached = raw.Cached
	return raw.Data, nil
}
