package salesshift

// Workspace sending settings — the email footer (postal address + opt-out
// text) at workspace, mailbox and integration level, and the non-secret edit
// of a BYOK integration.
//
//	GET   /api/v1/salesshift/settings/mailing-address
//	PUT   /api/v1/salesshift/settings/mailing-address
//	GET   /api/v1/salesshift/settings/email-footer/preview
//	GET   /api/v1/salesshift/settings/mailboxes
//	POST  /api/v1/salesshift/settings/mailboxes
//	PATCH /api/v1/salesshift/settings/mailboxes/{id}
//	PATCH /api/v1/salesshift/settings/integrations/{id}
//
// Footer semantics, identical for a mailbox and an integration: a nil/absent
// value inherits the workspace default; an empty FooterAddress means "no
// address for this sender". Footer text is plain text in which the token
// {unsubscribe} becomes the opt-out link — the server appends it when a
// configured text leaves it out, so the link always exists.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	neturl "net/url"

	"github.com/prodxcloud/vxcloud/transport"
)

// BuiltinFooterText is the server's footer text when nothing is configured.
const BuiltinFooterText = "Don't want these emails? {unsubscribe}"

// FooterTextMax is the server's limit on a footer text.
const FooterTextMax = 200

// decodeData unmarshals `raw` into out, unwrapping a {"data": …} envelope
// when there is one. Routes in this package disagree on whether they wrap,
// and a new route's envelope is not part of its contract.
func decodeData(raw json.RawMessage, out any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] == '{' {
		var env map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &env); err == nil {
			if d, ok := env["data"]; ok && len(bytes.TrimSpace(d)) > 0 && !bytes.Equal(bytes.TrimSpace(d), []byte("null")) {
				return json.Unmarshal(d, out)
			}
		}
	}
	return json.Unmarshal(trimmed, out)
}

/* ── workspace default ─────────────────────────────────────────────── */

// EmailFooterSettings is the workspace default footer.
type EmailFooterSettings struct {
	MailingAddress string `json:"mailing_address"`
	// FooterText is the workspace's own text; "" means the built-in default.
	FooterText        string `json:"footer_text"`
	DefaultFooterText string `json:"default_footer_text"`
	// MaxLength is the postal address limit.
	MaxLength int  `json:"max_length"`
	CanEdit   bool `json:"can_edit"`
}

// GetEmailFooterSettings returns the workspace default address and text.
func (c *Client) GetEmailFooterSettings(ctx context.Context) (*EmailFooterSettings, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/mailing-address")
	var out EmailFooterSettings
	if err := c.T.JSON(ctx, "salesshift.GetEmailFooterSettings", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetEmailFooterSettings: %w", err)
	}
	if out.DefaultFooterText == "" {
		out.DefaultFooterText = BuiltinFooterText
	}
	return &out, nil
}

// EmailFooterUpdate changes either or both workspace defaults. A nil field
// is left alone; a pointer to "" clears it (no address / built-in text).
type EmailFooterUpdate struct {
	MailingAddress *string `json:"mailing_address,omitempty"`
	FooterText     *string `json:"footer_text,omitempty"`
}

// SetEmailFooterSettings writes the workspace default footer. Admin only.
func (c *Client) SetEmailFooterSettings(ctx context.Context, in EmailFooterUpdate) (*EmailFooterSettings, error) {
	if in.MailingAddress == nil && in.FooterText == nil {
		return nil, errors.New("salesshift.SetEmailFooterSettings: set MailingAddress and/or FooterText")
	}
	if in.FooterText != nil && len([]rune(*in.FooterText)) > FooterTextMax {
		return nil, fmt.Errorf("salesshift.SetEmailFooterSettings: FooterText is longer than %d characters", FooterTextMax)
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/mailing-address")
	var out EmailFooterSettings
	if err := c.T.JSON(ctx, "salesshift.SetEmailFooterSettings", "PUT", url, in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.SetEmailFooterSettings: %w", err)
	}
	return &out, nil
}

/* ── preview ───────────────────────────────────────────────────────── */

// FooterPreviewQuery selects the sender to preview. Any one field is enough;
// none previews the workspace default.
type FooterPreviewQuery struct {
	MailboxID     string
	IntegrationID string
	FromEmail     string
}

// FooterPreview is exactly what send appends (the unsubscribe href is "#").
type FooterPreview struct {
	HTML    string `json:"html"`
	Text    string `json:"text"`
	Address string `json:"address"`
	// AddressSource: mailbox | integration | org | none.
	AddressSource string `json:"address_source"`
	FooterText    string `json:"footer_text"`
	// FooterTextSource: mailbox | integration | org | default.
	FooterTextSource string `json:"footer_text_source"`
}

// PreviewEmailFooter renders the footer one sender would get right now.
func (c *Client) PreviewEmailFooter(ctx context.Context, q FooterPreviewQuery) (*FooterPreview, error) {
	v := neturl.Values{}
	if q.MailboxID != "" {
		v.Set("mailbox_id", q.MailboxID)
	}
	if q.IntegrationID != "" {
		v.Set("integration_id", q.IntegrationID)
	}
	if q.FromEmail != "" {
		v.Set("from_email", q.FromEmail)
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/email-footer/preview")
	if len(v) > 0 {
		url += "?" + v.Encode()
	}
	var raw json.RawMessage
	if err := c.T.JSON(ctx, "salesshift.PreviewEmailFooter", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.PreviewEmailFooter: %w", err)
	}
	var out FooterPreview
	if err := decodeData(raw, &out); err != nil {
		return nil, fmt.Errorf("salesshift.PreviewEmailFooter: decode: %w", err)
	}
	return &out, nil
}

/* ── mailboxes ─────────────────────────────────────────────────────── */

// Mailbox is one sending identity (ss_mailboxes) with its caps and footer.
type Mailbox struct {
	ID            string `json:"id"`
	FromEmail     string `json:"from_email"`
	FromName      string `json:"from_name"`
	DailyCap      int    `json:"daily_cap"`
	SentToday     int    `json:"sent_today"`
	WarmupEnabled bool   `json:"warmup_enabled"`
	WarmupStage   int    `json:"warmup_stage"`
	HealthStatus  string `json:"health_status"`
	// FooterAddress: nil inherits, "" is "no address".
	FooterAddress *string `json:"footer_address"`
	// FooterText: nil inherits.
	FooterText *string `json:"footer_text"`
}

// ListMailboxes returns the workspace's sending identities.
func (c *Client) ListMailboxes(ctx context.Context) ([]Mailbox, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/mailboxes")
	var raw struct {
		Data []Mailbox `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListMailboxes", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListMailboxes: %w", err)
	}
	return raw.Data, nil
}

// NewMailbox is the create payload. FooterAddress/FooterText nil = inherit.
//
// DailyCap 0 sends nothing and takes the server default (50 a day), which
// dispatch enforces on every send from FromEmail. When the row exists only to
// carry a footer for a mailbox the sending pool already caps, pass a cap that
// will not bind first.
type NewMailbox struct {
	FromEmail     string  `json:"from_email"`
	FromName      string  `json:"from_name,omitempty"`
	DailyCap      int     `json:"daily_cap,omitempty"`
	WarmupEnabled bool    `json:"warmup_enabled,omitempty"`
	FooterAddress *string `json:"footer_address,omitempty"`
	FooterText    *string `json:"footer_text,omitempty"`
}

// CreateMailbox adds a sending identity and returns its id.
func (c *Client) CreateMailbox(ctx context.Context, in NewMailbox) (string, error) {
	if in.FromEmail == "" {
		return "", errors.New("salesshift.CreateMailbox: FromEmail is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/mailboxes")
	var out struct {
		ID string `json:"id"`
	}
	if err := c.T.JSON(ctx, "salesshift.CreateMailbox", "POST", url, in, &out); err != nil {
		return "", fmt.Errorf("salesshift.CreateMailbox: %w", err)
	}
	return out.ID, nil
}

// FooterPatch is the footer half of a mailbox or integration update.
//
// A nil pointer leaves the field alone. To go back to inheriting the
// workspace default, set the matching Inherit flag — that sends an explicit
// JSON null, which a pointer cannot express. An empty FooterAddress means
// "no address for this sender".
type FooterPatch struct {
	FooterAddress        *string
	FooterText           *string
	InheritFooterAddress bool
	InheritFooterText    bool
}

func (p FooterPatch) into(body map[string]any) {
	switch {
	case p.InheritFooterAddress:
		body["footer_address"] = nil
	case p.FooterAddress != nil:
		body["footer_address"] = *p.FooterAddress
	}
	switch {
	case p.InheritFooterText:
		body["footer_text"] = nil
	case p.FooterText != nil:
		body["footer_text"] = *p.FooterText
	}
}

// MailboxPatch is PATCH /settings/mailboxes/{id}. Only set fields are sent.
type MailboxPatch struct {
	FromName *string
	FooterPatch
}

// UpdateMailbox changes a mailbox's From name and/or footer.
func (c *Client) UpdateMailbox(ctx context.Context, id string, p MailboxPatch) (*Mailbox, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateMailbox: id is required")
	}
	body := map[string]any{}
	if p.FromName != nil {
		body["from_name"] = *p.FromName
	}
	p.FooterPatch.into(body)
	if len(body) == 0 {
		return nil, errors.New("salesshift.UpdateMailbox: nothing to update")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/mailboxes/"+id)
	var raw json.RawMessage
	if err := c.T.JSON(ctx, "salesshift.UpdateMailbox", "PATCH", url, body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateMailbox: %w", err)
	}
	var out Mailbox
	if err := decodeData(raw, &out); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateMailbox: decode: %w", err)
	}
	return &out, nil
}

/* ── integrations ──────────────────────────────────────────────────── */

// Integration is one BYOK provider row. Credentials live in Vault and are
// never returned; MetadataJSON carries the non-secret settings (name,
// from_email, sender_name, reply_to, footer_address, footer_text, …).
type Integration struct {
	ID              string         `json:"id"`
	IntegrationType string         `json:"integration_type"`
	Provider        string         `json:"provider"`
	MetadataJSON    map[string]any `json:"metadata_json"`
	IsActive        bool           `json:"is_active"`
	IsDefault       bool           `json:"is_default"`
	LastTestedAt    string         `json:"last_tested_at"`
	LastTestStatus  string         `json:"last_test_status"`
	LastTestError   string         `json:"last_test_error"`
	CreatedAt       string         `json:"created_at"`
}

// IntegrationPatch is PATCH /settings/integrations/{id}: metadata only. It
// never needs or touches the Vault credentials, so changing a From name does
// not mean re-entering a password.
type IntegrationPatch struct {
	Name      *string
	FromEmail *string
	FromName  *string
	ReplyTo   *string
	FooterPatch
}

// UpdateIntegration edits an integration's non-secret fields.
func (c *Client) UpdateIntegration(ctx context.Context, id string, p IntegrationPatch) (*Integration, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateIntegration: id is required")
	}
	body := map[string]any{}
	set := func(k string, v *string) {
		if v != nil {
			body[k] = *v
		}
	}
	set("name", p.Name)
	set("from_email", p.FromEmail)
	set("from_name", p.FromName)
	set("reply_to", p.ReplyTo)
	p.FooterPatch.into(body)
	if len(body) == 0 {
		return nil, errors.New("salesshift.UpdateIntegration: nothing to update")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/settings/integrations/"+id)
	var raw json.RawMessage
	if err := c.T.JSON(ctx, "salesshift.UpdateIntegration", "PATCH", url, body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateIntegration: %w", err)
	}
	var out Integration
	if err := decodeData(raw, &out); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateIntegration: decode: %w", err)
	}
	return &out, nil
}
