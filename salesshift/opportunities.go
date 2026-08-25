package salesshift

// Opportunities — the cross-tenant signal pool.
//
// Shared by every workspace, exactly like the lead pool: the rows are not
// scoped to an org, and the only per-org state (saved / dismissed / applied)
// lives in side tables. That is why Save and Dismiss are PATCHes that change
// nothing on the shared row.
//
//	GET   /api/v1/salesshift/opportunities
//	GET   /api/v1/salesshift/opportunities/{id}
//	POST  /api/v1/salesshift/opportunities
//	PATCH /api/v1/salesshift/opportunities/{id}
//	POST  /api/v1/salesshift/opportunities/{id}/push-to-lead
//	POST  /api/v1/salesshift/opportunities/{id}/convert

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// Opportunity is one signal in the shared pool.
type Opportunity struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// DescriptionTruncated is true when the list route cut Description at its
	// 600-character budget. Without it a cut body is indistinguishable from a
	// whole one — re-read the row with GetOpportunity to get the rest.
	DescriptionTruncated bool     `json:"description_truncated"`
	Category             string   `json:"category"`
	Skills               []string `json:"skills"`
	CompanyName          string   `json:"company_name"`
	ContactEmail         string   `json:"contact_email"`
	Location             string   `json:"location"`
	BudgetMin            *float64 `json:"budget_min"`
	BudgetMax            *float64 `json:"budget_max"`
	Currency             string   `json:"currency"`
	Duration             string   `json:"duration"`
	Status               string   `json:"status"`
	PostedBy             string   `json:"posted_by"`
	CreatedAt            string   `json:"created_at"`

	// Intelligence fields. `Source` names the site the row was scraped from —
	// hackernews, remoteok, remotive, manual…
	Source         string `json:"source"`
	SignalType     string `json:"signal_type"`
	SourceURL      string `json:"source_url"`
	ScrapedAt      string `json:"scraped_at"`
	RelevanceScore *int   `json:"relevance_score"`
	Industry       string `json:"industry"`
	CompanySize    string `json:"company_size"`
	FundingAmount  string `json:"funding_amount"`
	AssignedTo     string `json:"assigned_to"`

	// Per-org state, from ss_opportunity_saves — never from the shared row.
	IsSaved     bool `json:"is_saved"`
	IsDismissed bool `json:"is_dismissed"`
}

// OpportunityFilter narrows the pool. Zero values are omitted.
type OpportunityFilter struct {
	Q          string
	Category   string
	Source     string
	SignalType string
	Industry   string
	MinScore   int
	SavedOnly  bool
	Limit      int
	// Rows to skip. Without this the page reported HasMore == true and gave the
	// caller no way to reach the rest -- the exact inference problem HasMore
	// exists to remove. The route declares `offset` and computes has_more as
	// offset + len(rows) < total.
	Offset int
}

func (f OpportunityFilter) query() string {
	v := neturl.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("q", f.Q)
	set("category", f.Category)
	set("source", f.Source)
	set("signal_type", f.SignalType)
	set("industry", f.Industry)
	if f.MinScore > 0 {
		v.Set("min_score", strconv.Itoa(f.MinScore))
	}
	if f.SavedOnly {
		v.Set("saved_only", "true")
	}
	if f.Limit > 0 {
		v.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Offset > 0 {
		v.Set("offset", strconv.Itoa(f.Offset))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// SourceFacet is a per-source count over the WHOLE pool, not the filtered
// slice — so the counts stay stable when a source filter is applied.
type SourceFacet struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

// OpportunityPage is the envelope that comes back beside the rows.
//
// Total is the POOL under the current filters, Count is this page. They are
// different numbers — a 500-row request against 6,608 open signals returns
// Count 500 and Total 6608 — and HasMore is stated by the server rather than
// inferred from len(data) >= Limit, an inference that is wrong at 499 of 500
// and leaves the caller with no way to reach the rest.
type OpportunityPage struct {
	Total   int  `json:"total"`
	Count   int  `json:"count"`
	HasMore bool `json:"has_more"`
	Limit   int  `json:"limit"`

	Sources []SourceFacet `json:"sources"`
}

// ListOpportunities returns signals from the shared pool plus the pagination
// envelope. The envelope used to be discarded, so callers could not tell a
// full result from a truncated one.
func (c *Client) ListOpportunities(ctx context.Context, f OpportunityFilter) (opps []Opportunity, page OpportunityPage, err error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/opportunities") + f.query()
	var raw struct {
		Data []Opportunity `json:"data"`
		OpportunityPage
	}
	if err := c.T.JSON(ctx, "salesshift.ListOpportunities", "GET", url, nil, &raw); err != nil {
		return nil, OpportunityPage{}, fmt.Errorf("salesshift.ListOpportunities: %w", err)
	}
	return raw.Data, raw.OpportunityPage, nil
}

// GetOpportunity reads one signal, including this org's saved/dismissed state.
func (c *Client) GetOpportunity(ctx context.Context, id string) (*Opportunity, error) {
	if id == "" {
		return nil, errors.New("salesshift.GetOpportunity: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/opportunities/"+id)
	var raw struct {
		Data Opportunity `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetOpportunity", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetOpportunity: %w", err)
	}
	return &raw.Data, nil
}

// SaveOpportunity marks (or unmarks) a signal for THIS organization only.
func (c *Client) SaveOpportunity(ctx context.Context, id string, saved bool) (*Opportunity, error) {
	return c.patchOpportunity(ctx, "salesshift.SaveOpportunity", id, map[string]any{"is_saved": saved})
}

// DismissOpportunity hides a signal from this organization's feed. Other
// workspaces are unaffected — the row is shared.
func (c *Client) DismissOpportunity(ctx context.Context, id string, dismissed bool) (*Opportunity, error) {
	return c.patchOpportunity(ctx, "salesshift.DismissOpportunity", id, map[string]any{"is_dismissed": dismissed})
}

func (c *Client) patchOpportunity(ctx context.Context, op, id string, body map[string]any) (*Opportunity, error) {
	if id == "" {
		return nil, fmt.Errorf("%s: id is required", op)
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/opportunities/"+id)
	var raw struct {
		Data Opportunity `json:"data"`
	}
	if err := c.T.JSON(ctx, op, "PATCH", url, body, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &raw.Data, nil
}

// PushToLead copies the signal's contact into this workspace's CRM.
//
// Lighter than Convert: it does not mint a deal and does not require a
// configured pipeline. Fails with 422 when the signal published no email —
// there is nothing to create a contact from.
func (c *Client) PushToLead(ctx context.Context, id string) (contactID string, created bool, err error) {
	if id == "" {
		return "", false, errors.New("salesshift.PushToLead: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/opportunities/"+id+"/push-to-lead")
	var raw struct {
		LeadID  string `json:"lead_id"`
		Created bool   `json:"created"`
	}
	if err := c.T.JSON(ctx, "salesshift.PushToLead", "POST", url, map[string]any{}, &raw); err != nil {
		return "", false, fmt.Errorf("salesshift.PushToLead: %w", err)
	}
	return raw.LeadID, raw.Created, nil
}

// ConvertOpportunity creates contact + deal in the default pipeline.
// Idempotent: a second call returns the same ids with AlreadyConverted set.
func (c *Client) ConvertOpportunity(ctx context.Context, id string) (contactID, dealID string, already bool, err error) {
	if id == "" {
		return "", "", false, errors.New("salesshift.ConvertOpportunity: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/opportunities/"+id+"/convert")
	var raw struct {
		ContactID        string `json:"contact_id"`
		DealID           string `json:"deal_id"`
		AlreadyConverted bool   `json:"already_converted"`
	}
	if err := c.T.JSON(ctx, "salesshift.ConvertOpportunity", "POST", url, map[string]any{}, &raw); err != nil {
		return "", "", false, fmt.Errorf("salesshift.ConvertOpportunity: %w", err)
	}
	return raw.ContactID, raw.DealID, raw.AlreadyConverted, nil
}
