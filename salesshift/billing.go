package salesshift

// Platform billing — what a workspace pays SalesShift.
//
// Not to be confused with the customer's own quote-to-cash (`/subscriptions`,
// `/payments`, `/invoices`), which is money THEIR customers pay THEM. These
// endpoints are the other direction: our three published plans, priced per
// seat per month, charged on our Stripe account.
//
//	GET    /api/v1/salesshift/billing/plans
//	GET    /api/v1/salesshift/billing/subscription
//	GET    /api/v1/salesshift/billing/entitlements
//	GET    /api/v1/salesshift/billing/invoices
//	GET    /api/v1/salesshift/billing/events
//	POST   /api/v1/salesshift/billing/activate
//	GET    /api/v1/salesshift/billing/self-hosted
//	POST   /api/v1/salesshift/billing/self-hosted/node
//	DELETE /api/v1/salesshift/billing/self-hosted/node
//	POST   /api/v1/salesshift/billing/checkout
//	POST   /api/v1/salesshift/billing/checkout/confirm
//	POST   /api/v1/salesshift/billing/portal
//	POST   /api/v1/salesshift/billing/change
//	POST   /api/v1/salesshift/billing/cancel
//	POST   /api/v1/salesshift/billing/resume

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// PlanQuotas are per-seat monthly allowances. A nil pointer means unlimited —
// the API sends null rather than a sentinel, and collapsing that to 0 would
// read as "no allowance at all", which is the opposite of what it means.
type PlanQuotas struct {
	Emails    *int `json:"emails"`
	Reveals   *int `json:"reveals"`
	AI        *int `json:"ai"`
	Mailboxes *int `json:"mailboxes"`
	Contacts  *int `json:"contacts"`
}

// ManagedBy is what a plan buys FROM US, as opposed to how much of it.
//
// This is the distinction the free tier rests on. Self-Hosted is not a
// throttled Starter — it is the same product running on the tenant's own node,
// mailboxes and model key, which is exactly why it is free. Its Quotas.Emails
// is 0, and that does NOT mean "may not send": it means we are not the one
// sending, and Managed.Sending == false is what says so. Branch on these
// flags, never on a quota of zero or a price of zero.
type ManagedBy struct {
	Compute bool `json:"compute"`
	Sending bool `json:"sending"`
	AI      bool `json:"ai"`
}

// Plan is one published tier.
type Plan struct {
	ID              string     `json:"id"`
	Code            string     `json:"code"` // self_hosted | starter | professional | organization
	Name            string     `json:"name"`
	Tagline         string     `json:"tagline"`
	UnitAmountCents int        `json:"unit_amount_cents"`
	PriceDisplay    string     `json:"price_display"`
	Currency        string     `json:"currency"`
	Interval        string     `json:"interval"`
	Features        []string   `json:"features"`
	Quotas          PlanQuotas `json:"quotas"`
	IsFree          bool       `json:"is_free"`
	Managed         ManagedBy  `json:"managed"`
	IsPurchasable   bool       `json:"is_purchasable"`
	// A free plan is activated, not bought: there is no card to take and no
	// $0 Stripe Price to redirect to. Call ActivatePlan, not StartCheckout.
	IsActivatable bool `json:"is_activatable"`
}

// Subscription is the workspace's own plan.
type Subscription struct {
	ID                  string     `json:"id"`
	Status              string     `json:"status"` // active|trialing|past_due|canceled|incomplete|none
	Entitled            bool       `json:"entitled"`
	Source              string     `json:"source"` // stripe | comp | manual
	Seats               int        `json:"seats"`
	Plan                *Plan      `json:"plan"`
	MonthlyTotalCents   int        `json:"monthly_total_cents"`
	MonthlyTotalDisplay string     `json:"monthly_total_display"`
	CurrentPeriodStart  string     `json:"current_period_start"`
	CurrentPeriodEnd    string     `json:"current_period_end"`
	CancelAtPeriodEnd   bool       `json:"cancel_at_period_end"`
	CanceledAt          string     `json:"canceled_at"`
	Note                string     `json:"note"`
	HasStripeCustomer   bool       `json:"has_stripe_customer"`
	Allowance           PlanQuotas `json:"allowance"`
	Members             int        `json:"members"`
	SeatsShortfall      int        `json:"seats_shortfall"`
	// Always present, even on a workspace with no subscription row at all — an
	// unsubscribed workspace is on the free tier, not in limbo.
	Entitlements *Entitlements `json:"entitlements"`
}

// ListPlans returns the published tiers. `paymentsEnabled` is false when the
// deployment has no usable Stripe key — plans still render, checkout refuses.
func (c *Client) ListPlans(ctx context.Context) (plans []Plan, paymentsEnabled bool, err error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/plans")
	var raw struct {
		Plans           []Plan `json:"plans"`
		PaymentsEnabled bool   `json:"payments_enabled"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListPlans", "GET", url, nil, &raw); err != nil {
		return nil, false, fmt.Errorf("salesshift.ListPlans: %w", err)
	}
	return raw.Plans, raw.PaymentsEnabled, nil
}

// GetSubscription returns the workspace's current plan, seats and allowance.
func (c *Client) GetSubscription(ctx context.Context) (*Subscription, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/subscription")
	var out Subscription
	if err := c.T.JSON(ctx, "salesshift.GetSubscription", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetSubscription: %w", err)
	}
	return &out, nil
}

// Invoice mirrors a Stripe invoice. Amounts are in the smallest currency unit.
type Invoice struct {
	ID               string `json:"id"`
	Number           string `json:"number"`
	Status           string `json:"status"`
	AmountDue        int    `json:"amount_due"`
	AmountPaid       int    `json:"amount_paid"`
	Currency         string `json:"currency"`
	Created          int64  `json:"created"`
	PeriodStart      int64  `json:"period_start"`
	PeriodEnd        int64  `json:"period_end"`
	HostedInvoiceURL string `json:"hosted_invoice_url"`
	InvoicePDF       string `json:"invoice_pdf"`
}

// ListInvoices reads Stripe directly. A comped workspace has no Stripe
// customer and therefore no invoices — that is an empty slice with `reason`
// set, not an error.
func (c *Client) ListInvoices(ctx context.Context) (invoices []Invoice, reason string, err error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/invoices")
	var raw struct {
		Invoices []Invoice `json:"invoices"`
		Reason   string    `json:"reason"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListInvoices", "GET", url, nil, &raw); err != nil {
		return nil, "", fmt.Errorf("salesshift.ListInvoices: %w", err)
	}
	return raw.Invoices, raw.Reason, nil
}

// BillingEvent is one row of the append-only trail.
type BillingEvent struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	AmountCents *int   `json:"amount_cents"`
	CreatedAt   string `json:"created_at"`
}

// ListBillingEvents returns the workspace's billing history.
func (c *Client) ListBillingEvents(ctx context.Context, limit int) ([]BillingEvent, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/events")
	if limit > 0 {
		url += fmt.Sprintf("?limit=%d", limit)
	}
	var raw struct {
		Events []BillingEvent `json:"events"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListBillingEvents", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListBillingEvents: %w", err)
	}
	return raw.Events, nil
}

// StartCheckout opens a Stripe Checkout session and returns its hosted URL.
// Nothing is charged until the customer completes it there.
func (c *Client) StartCheckout(ctx context.Context, planCode string, seats int) (url string, sessionID string, err error) {
	if planCode == "" {
		return "", "", errors.New("salesshift.StartCheckout: planCode is required")
	}
	if seats <= 0 {
		seats = 1
	}
	endpoint := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/checkout")
	body := map[string]any{"plan_code": planCode, "seats": seats}
	var raw struct {
		URL       string `json:"url"`
		SessionID string `json:"session_id"`
	}
	if err := c.T.JSON(ctx, "salesshift.StartCheckout", "POST", endpoint, body, &raw); err != nil {
		return "", "", fmt.Errorf("salesshift.StartCheckout: %w", err)
	}
	return raw.URL, raw.SessionID, nil
}

// ConfirmCheckout re-reads a completed Checkout Session server-side and
// applies it. This is the activation path on deployments with no reachable
// webhook, and it verifies with Stripe rather than trusting the caller.
func (c *Client) ConfirmCheckout(ctx context.Context, sessionID string) (*Subscription, bool, error) {
	if sessionID == "" {
		return nil, false, errors.New("salesshift.ConfirmCheckout: sessionID is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/checkout/confirm")
	var raw struct {
		Applied      bool          `json:"applied"`
		Reason       string        `json:"reason"`
		Subscription *Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.ConfirmCheckout", "POST", url,
		map[string]any{"session_id": sessionID}, &raw); err != nil {
		return nil, false, fmt.Errorf("salesshift.ConfirmCheckout: %w", err)
	}
	return raw.Subscription, raw.Applied, nil
}

// BillingPortal returns a Stripe hosted portal URL for card and cancellation
// management. Fails on a workspace with no payment account (e.g. a comp).
func (c *Client) BillingPortal(ctx context.Context) (string, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/portal")
	var raw struct {
		URL string `json:"url"`
	}
	if err := c.T.JSON(ctx, "salesshift.BillingPortal", "POST", url, map[string]any{}, &raw); err != nil {
		return "", fmt.Errorf("salesshift.BillingPortal: %w", err)
	}
	return raw.URL, nil
}

// ChangeSubscription moves plan and/or seat count. Leave planCode empty to
// change only seats, or seats 0 to change only the plan.
func (c *Client) ChangeSubscription(ctx context.Context, planCode string, seats int) (*Subscription, error) {
	body := map[string]any{}
	if planCode != "" {
		body["plan_code"] = planCode
	}
	if seats > 0 {
		body["seats"] = seats
	}
	if len(body) == 0 {
		return nil, errors.New("salesshift.ChangeSubscription: nothing to change")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/change")
	var raw struct {
		Subscription Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.ChangeSubscription", "POST", url, body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ChangeSubscription: %w", err)
	}
	return &raw.Subscription, nil
}

// CancelSubscription ends the plan. atPeriodEnd=true keeps access until the
// period closes, which is almost always what a customer means by "cancel".
func (c *Client) CancelSubscription(ctx context.Context, atPeriodEnd bool) (*Subscription, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/cancel")
	var raw struct {
		Subscription Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.CancelSubscription", "POST", url,
		map[string]any{"at_period_end": atPeriodEnd}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.CancelSubscription: %w", err)
	}
	return &raw.Subscription, nil
}

// ResumeSubscription clears a pending cancellation.
func (c *Client) ResumeSubscription(ctx context.Context) (*Subscription, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/resume")
	var raw struct {
		Subscription Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.ResumeSubscription", "POST", url, map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ResumeSubscription: %w", err)
	}
	return &raw.Subscription, nil
}

// ── Entitlements ─────────────────────────────────────────────────────────
//
// What the workspace may actually do, as opposed to what it pays. The server
// refuses over-quota work with HTTP 402 Payment Required, so a caller that
// reads this first can say why before it tries.

// EntitlementQuotas is the workspace's live allowance, already pooled across
// seats — unlike PlanQuotas, which is the per-seat figure on the price list.
// A nil pointer is unlimited.
type EntitlementQuotas struct {
	Emails    *int `json:"emails"`
	Reveals   *int `json:"reveals"`
	AI        *int `json:"ai"`
	Mailboxes *int `json:"mailboxes"`
	Contacts  *int `json:"contacts"`
	Users     *int `json:"users"`
}

// SelfHostedState is the node half of the entitlement: whether this plan
// requires the tenant to run their own node, and whether they have.
type SelfHostedState struct {
	Required   bool   `json:"required"`
	NodeHost   string `json:"node_host"`
	VerifiedAt string `json:"verified_at"`
	// Ready is Required && a node is registered. False on a self-hosted plan
	// means sending and agents are refused until one is.
	Ready bool `json:"ready"`
}

// Entitlements is the answer to "what can this workspace do right now".
type Entitlements struct {
	PlanCode   string            `json:"plan_code"`
	PlanName   string            `json:"plan_name"`
	Status     string            `json:"status"`
	Source     string            `json:"source"` // stripe | comp | manual | free
	Seats      int               `json:"seats"`
	IsFree     bool              `json:"is_free"`
	Managed    ManagedBy         `json:"managed"`
	Allowance  EntitlementQuotas `json:"allowance"`
	SelfHosted SelfHostedState   `json:"self_hosted"`
}

// GetEntitlements returns what the workspace may do, without the billing
// detail. A workspace with no subscription is not an error and not an empty
// answer — it resolves to the free tier, deliberately, so this never reports
// "no plan" and leaves the caller to guess what works.
func (c *Client) GetEntitlements(ctx context.Context) (*Entitlements, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/entitlements")
	var out Entitlements
	if err := c.T.JSON(ctx, "salesshift.GetEntitlements", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetEntitlements: %w", err)
	}
	return &out, nil
}

// ActivatePlan puts the workspace on a free plan. No card, no Stripe.
//
// Free plans only — the server refuses any code not flagged free, so this
// cannot become a way to grant a paid tier. It also refuses to downgrade any
// live non-free plan, bought or granted: give that up through
// CancelSubscription, which tells Stripe when there is a Stripe to tell.
func (c *Client) ActivatePlan(ctx context.Context, planCode string) (*Subscription, error) {
	if planCode == "" {
		return nil, errors.New("salesshift.ActivatePlan: planCode is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/activate")
	var raw struct {
		Subscription Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.ActivatePlan", "POST", url,
		map[string]any{"plan_code": planCode}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ActivatePlan: %w", err)
	}
	return &raw.Subscription, nil
}

// ── The self-hosted node ─────────────────────────────────────────────────

// NodeProbe is the result of asking the registered node for its health right
// now. A node that is down is still the registered node, so an unreachable
// probe is reported, not raised.
type NodeProbe struct {
	Reachable  bool   `json:"reachable"`
	Version    string `json:"version"`
	TenantName string `json:"tenant_name"`
	Time       string `json:"time"`
	Error      string `json:"error"`
}

// NodeInstall is what the tenant needs to bring a node up and have it accepted.
type NodeInstall struct {
	// TenantID is the workspace UUID — always accepted, never ambiguous.
	TenantID string `json:"tenant_id"`
	// Accepts is every value the node may report as its tenant_id. The
	// workspace NAME is in here too when it is unique across all
	// organizations, because nodes provisioned before the handshake existed
	// carry TENANT_ID=<name>, not the UUID.
	Accepts    []string `json:"accepts"`
	Image      string   `json:"image"`
	HealthPath string   `json:"health_path"`
}

// SelfHosted is the node registration screen's whole state.
type SelfHosted struct {
	Required    bool        `json:"required"`
	Host        string      `json:"host"`
	VerifiedAt  string      `json:"verified_at"`
	Fingerprint string      `json:"fingerprint"`
	Live        *NodeProbe  `json:"live"` // nil when no node is registered
	Install     NodeInstall `json:"install"`
}

// GetSelfHosted returns the registered node, a live probe of it, and the
// identity values a node must report to be accepted.
func (c *Client) GetSelfHosted(ctx context.Context) (*SelfHosted, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/self-hosted")
	var out SelfHosted
	if err := c.T.JSON(ctx, "salesshift.GetSelfHosted", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetSelfHosted: %w", err)
	}
	return &out, nil
}

// NodeRegistration is what a successful handshake reports back.
type NodeRegistration struct {
	Host         string        `json:"host"`
	Verified     bool          `json:"verified"`
	Version      string        `json:"version"`
	TenantName   string        `json:"tenant_name"`
	Entitlements *Entitlements `json:"entitlements"`
}

// RegisterNode points the workspace at its own node — after the node proves
// whose it is.
//
// The server does not take the caller's word for it: it calls GET {host}/health
// and requires the node to report a tenant_id in SelfHosted.Install.Accepts.
// Setting that requires shell access to the machine, so a node that answers
// with this workspace's id is a node this workspace controls.
//
// HTTPS is required; http:// is allowed only for localhost/127.0.0.1.
// Failures are 400 (no tenant reported, or plaintext), 403 (the node
// identifies as another workspace) and 502 (unreachable).
func (c *Client) RegisterNode(ctx context.Context, host string) (*NodeRegistration, error) {
	if host == "" {
		return nil, errors.New("salesshift.RegisterNode: host is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/self-hosted/node")
	var out NodeRegistration
	if err := c.T.JSON(ctx, "salesshift.RegisterNode", "POST", url,
		map[string]any{"host": host}, &out); err != nil {
		return nil, fmt.Errorf("salesshift.RegisterNode: %w", err)
	}
	return &out, nil
}

// DetachNode unregisters the node. On a self-hosted plan, sending and agents
// stop until another one is registered — this is not a cosmetic change.
func (c *Client) DetachNode(ctx context.Context) error {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/billing/self-hosted/node")
	if err := c.T.JSON(ctx, "salesshift.DetachNode", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DetachNode: %w", err)
	}
	return nil
}
