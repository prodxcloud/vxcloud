// Package salesshift wraps the SalesShift email service — tracked sends
// through the tenant's BYOK providers with suppression gating, daily caps,
// open/click tracking, and the SendGrid-style Kafka event stream.
//
// Endpoints (VxCloud control plane — FastAPI):
//
//	POST /api/v1/salesshift/email/send
//	GET  /api/v1/salesshift/emails
//	GET  /api/v1/salesshift/stats
//
// Endpoint (tenant node — Go email worker):
//
//	GET  /api/v2/salesshift/email/health
package salesshift

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client — construct via c.SalesShift().
type Client struct {
	T           *transport.Transport
	VxCloudURL string
	NodeURL     string
}

// SendEmailInput is one tracked email. Subject/body accept merge tags
// ({{first_name}}, {{company_name}}) resolved against the contact record.
type SendEmailInput struct {
	ToEmail   string `json:"to_email"`
	Subject   string `json:"subject"`
	BodyHTML  string `json:"body_html"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// SendEmailResult reports the delivery outcome and tracking handle.
type SendEmailResult struct {
	Success    bool   `json:"success"`
	Status     string `json:"status"` // sent | failed
	TrackingID string `json:"tracking_id"`
	Provider   string `json:"provider"` // node-smtp | smtp | sendgrid | mailgun | platform | sink
	ContactID  string `json:"contact_id"`
	Error      string `json:"error,omitempty"`
}

// SendEmail delivers one tracked email through the org's BYOK provider
// (tenant node worker preferred). Suppressed/unsubscribed recipients are
// rejected with an error — that gate is not optional.
func (c *Client) SendEmail(ctx context.Context, in SendEmailInput) (*SendEmailResult, error) {
	if in.ToEmail == "" || in.Subject == "" || in.BodyHTML == "" {
		return nil, errors.New("salesshift.SendEmail: ToEmail, Subject and BodyHTML are required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/email/send")
	var out SendEmailResult
	if err := c.T.JSON(ctx, "salesshift.SendEmail", "POST", url, in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.SendEmail: %w", err)
	}
	return &out, nil
}

// TrackedEmail is one row of the org's outbound email feed.
type TrackedEmail struct {
	ID         string `json:"id"`
	ToEmail    string `json:"to_email"`
	Subject    string `json:"subject"`
	Status     string `json:"status"`
	Provider   string `json:"provider"`
	OpenCount  int    `json:"open_count"`
	ClickCount int    `json:"click_count"`
	SentAt     string `json:"sent_at"`
}

// ListEmails returns tracked emails, optionally filtered by status
// (sent, opened, clicked, replied, bounced, unsubscribed, failed).
func (c *Client) ListEmails(ctx context.Context, status string) ([]TrackedEmail, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/emails")
	if status != "" {
		url += "?status=" + status
	}
	var raw struct {
		Success bool           `json:"success"`
		Data    []TrackedEmail `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListEmails", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListEmails: %w", err)
	}
	return raw.Data, nil
}

// Stats is the SalesShift dashboard aggregate.
type Stats struct {
	Contacts        int                    `json:"contacts"`
	Companies       int                    `json:"companies"`
	OpenDeals       int                    `json:"open_deals"`
	ActiveSequences int                    `json:"active_sequences"`
	EmailStats      map[string]int         `json:"email_stats"`
	Raw             map[string]interface{} `json:"-"`
}

// GetStats returns the org's live dashboard stats.
func (c *Client) GetStats(ctx context.Context) (*Stats, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/stats")
	var out Stats
	if err := c.T.JSON(ctx, "salesshift.GetStats", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetStats: %w", err)
	}
	return &out, nil
}

// WorkerHealth reports the tenant node email worker's status.
type WorkerHealth struct {
	Status          string   `json:"status"`
	Service         string   `json:"service"`
	Providers       []string `json:"providers"`
	RedisConnected  bool     `json:"redis_connected"`
	RateLimitDomain int      `json:"rate_limit_domain"`
}

// GetWorkerHealth probes the Go email worker on the tenant node.
func (c *Client) GetWorkerHealth(ctx context.Context) (*WorkerHealth, error) {
	if c.NodeURL == "" {
		return nil, errors.New("salesshift.GetWorkerHealth: no tenant node resolved")
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/salesshift/email/health")
	var out WorkerHealth
	if err := c.T.JSON(ctx, "salesshift.GetWorkerHealth", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetWorkerHealth: %w", err)
	}
	return &out, nil
}
