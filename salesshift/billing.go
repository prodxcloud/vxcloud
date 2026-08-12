package salesshift

// Platform billing — what a workspace pays SalesShift.
//
// Not to be confused with the customer's own quote-to-cash (`/subscriptions`,
// `/payments`, `/invoices`), which is money THEIR customers pay THEM. These
// endpoints are the other direction: our three published plans, priced per
// seat per month, charged on our Stripe account.
//
//	GET  /api/v1/salesshift/billing/plans
//	GET  /api/v1/salesshift/billing/subscription
//	GET  /api/v1/salesshift/billing/invoices
//	GET  /api/v1/salesshift/billing/events
//	POST /api/v1/salesshift/billing/checkout
//	POST /api/v1/salesshift/billing/checkout/confirm
//	POST /api/v1/salesshift/billing/portal
//	POST /api/v1/salesshift/billing/change
//	POST /api/v1/salesshift/billing/cancel
//	POST /api/v1/salesshift/billing/resume

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

// Plan is one published tier.
type Plan struct {
	ID              string     `json:"id"`
	Code            string     `json:"code"` // starter | professional | organization
	Name            string     `json:"name"`
	Tagline         string     `json:"tagline"`
	UnitAmountCents int        `json:"unit_amount_cents"`
	PriceDisplay    string     `json:"price_display"`
	Currency        string     `json:"currency"`
	Interval        string     `json:"interval"`
	Features        []string   `json:"features"`
	Quotas          PlanQuotas `json:"quotas"`
	IsPurchasable   bool       `json:"is_purchasable"`
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
}

// ListPlans returns the published tiers. `paymentsEnabled` is false when the
// deployment has no usable Stripe key — plans still render, checkout refuses.
func (c *Client) ListPlans(ctx context.Context) (plans []Plan, paymentsEnabled bool, err error) {
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/plans")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/subscription")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/invoices")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/events")
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
	endpoint := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/checkout")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/checkout/confirm")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/portal")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/change")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/cancel")
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
	url := transport.JoinURL(c.InfinityURL, "/api/v1/salesshift/billing/resume")
	var raw struct {
		Subscription Subscription `json:"subscription"`
	}
	if err := c.T.JSON(ctx, "salesshift.ResumeSubscription", "POST", url, map[string]any{}, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ResumeSubscription: %w", err)
	}
	return &raw.Subscription, nil
}
