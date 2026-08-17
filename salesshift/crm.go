package salesshift

// Contacts — the CRM records that are actually mailable.
//
//	GET    /api/v1/salesshift/contacts
//	POST   /api/v1/salesshift/contacts
//	GET    /api/v1/salesshift/contacts/{id}
//	PUT    /api/v1/salesshift/contacts/{id}
//	DELETE /api/v1/salesshift/contacts/{id}
//	POST   /api/v1/salesshift/contacts/{id}/send-email
//	POST   /api/v1/salesshift/contacts/{id}/notes
//	GET    /api/v1/salesshift/contacts/{id}/activities
//	GET    /api/v1/salesshift/lists
//
// A pool lead is a masked snapshot and cannot be emailed. A Contact owns its
// address outright, which is why every send path in this package takes a
// contact id rather than an address.

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"strconv"

	"github.com/prodxcloud/vxcloud/transport"
)

// Contact is one CRM record. TotalScore is FitScore + IntentScore as computed
// server-side; recomputing it client-side would drift.
type Contact struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Email          string `json:"email"`
	Phone          string `json:"phone"`
	CompanyID      string `json:"company_id"`
	CompanyName    string `json:"company_name"`
	JobTitle       string `json:"job_title"`
	Seniority      string `json:"seniority"`
	LinkedInURL    string `json:"linkedin_url"`
	City           string `json:"city"`
	Country        string `json:"country"`
	Source         string `json:"source"`
	Status         string `json:"status"`
	LifecycleStage string `json:"lifecycle_stage"`
	FitScore       int    `json:"fit_score"`
	IntentScore    int    `json:"intent_score"`
	TotalScore     int    `json:"total_score"`
	LastContacted  string `json:"last_contacted"`
	LastActivity   string `json:"last_activity"`
	EmailsSent     int    `json:"emails_sent_count"`
	EmailOpens     int    `json:"email_opens_count"`
	EmailsReplied  int    `json:"emails_replied_count"`
	CreatedAt      string `json:"created_at"`
}

// Name is the display name, falling back to the address so a contact that was
// imported with only an email still renders as something.
func (c Contact) Name() string {
	switch {
	case c.FirstName != "" && c.LastName != "":
		return c.FirstName + " " + c.LastName
	case c.FirstName != "":
		return c.FirstName
	case c.LastName != "":
		return c.LastName
	}
	return c.Email
}

// Pagination is the list envelope. Total is the count across all pages, not
// the length of Data — a caller that conflates the two under-reports.
type Pagination struct {
	Total      int `json:"total"`
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	TotalPages int `json:"total_pages"`
}

// ContactFilter narrows the list. Search matches name, email, company or title.
type ContactFilter struct {
	Search         string
	LifecycleStage string
	CompanyID      string
	MinScore       int
	// HasEmail restricts to contacts with an address — the only ones a send
	// can reach.
	HasEmail  bool
	Page      int
	Limit     int
	SortBy    string
	SortOrder string
}

func (f ContactFilter) query() string {
	v := neturl.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("search", f.Search)
	set("lifecycle_stage", f.LifecycleStage)
	set("company_id", f.CompanyID)
	set("sort_by", f.SortBy)
	set("sort_order", f.SortOrder)
	if f.MinScore > 0 {
		v.Set("min_score", strconv.Itoa(f.MinScore))
	}
	if f.HasEmail {
		v.Set("email_status", "has_email")
	}
	if f.Page > 0 {
		v.Set("page", strconv.Itoa(f.Page))
	}
	if f.Limit > 0 {
		v.Set("limit", strconv.Itoa(f.Limit))
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// ListContacts returns one page plus the pagination envelope.
func (c *Client) ListContacts(ctx context.Context, f ContactFilter) ([]Contact, Pagination, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts") + f.query()
	var raw struct {
		Data       []Contact  `json:"data"`
		Pagination Pagination `json:"pagination"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListContacts", "GET", url, nil, &raw); err != nil {
		return nil, Pagination{}, fmt.Errorf("salesshift.ListContacts: %w", err)
	}
	return raw.Data, raw.Pagination, nil
}

// GetContact fetches one record.
func (c *Client) GetContact(ctx context.Context, id string) (*Contact, error) {
	if id == "" {
		return nil, errors.New("salesshift.GetContact: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+id)
	var out Contact
	if err := c.T.JSON(ctx, "salesshift.GetContact", "GET", url, nil, &out); err != nil {
		return nil, fmt.Errorf("salesshift.GetContact: %w", err)
	}
	return &out, nil
}

// NewContact is the create payload.
type NewContact struct {
	Email          string `json:"email"`
	FirstName      string `json:"first_name,omitempty"`
	LastName       string `json:"last_name,omitempty"`
	Phone          string `json:"phone,omitempty"`
	CompanyName    string `json:"company_name,omitempty"`
	JobTitle       string `json:"job_title,omitempty"`
	LinkedInURL    string `json:"linkedin_url,omitempty"`
	City           string `json:"city,omitempty"`
	Country        string `json:"country,omitempty"`
	Source         string `json:"source,omitempty"`
	LifecycleStage string `json:"lifecycle_stage,omitempty"`
}

// CreateContact adds a contact. Email is required — a contact without an
// address is not mailable and every downstream feature would skip it.
func (c *Client) CreateContact(ctx context.Context, in NewContact) (*Contact, error) {
	if in.Email == "" {
		return nil, errors.New("salesshift.CreateContact: Email is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts")
	var out Contact
	if err := c.T.JSON(ctx, "salesshift.CreateContact", "POST", url, in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.CreateContact: %w", err)
	}
	return &out, nil
}

// ContactPatch is a partial update — only non-nil fields are sent.
type ContactPatch struct {
	FirstName      *string `json:"first_name,omitempty"`
	LastName       *string `json:"last_name,omitempty"`
	Email          *string `json:"email,omitempty"`
	Phone          *string `json:"phone,omitempty"`
	CompanyName    *string `json:"company_name,omitempty"`
	JobTitle       *string `json:"job_title,omitempty"`
	LifecycleStage *string `json:"lifecycle_stage,omitempty"`
	Status         *string `json:"status,omitempty"`
}

// UpdateContact applies a partial update.
func (c *Client) UpdateContact(ctx context.Context, id string, p ContactPatch) (*Contact, error) {
	if id == "" {
		return nil, errors.New("salesshift.UpdateContact: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+id)
	var out Contact
	if err := c.T.JSON(ctx, "salesshift.UpdateContact", "PUT", url, p, &out); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateContact: %w", err)
	}
	return &out, nil
}

// DeleteContact removes a contact and its activity.
func (c *Client) DeleteContact(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("salesshift.DeleteContact: id is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+id)
	if err := c.T.JSON(ctx, "salesshift.DeleteContact", "DELETE", url, nil, nil); err != nil {
		return fmt.Errorf("salesshift.DeleteContact: %w", err)
	}
	return nil
}

// ContactEmail is a one-off tracked send. The sender overrides are optional;
// with none set the org's default integration is used.
type ContactEmail struct {
	Subject         string `json:"subject"`
	BodyHTML        string `json:"body_html"`
	FromEmail       string `json:"from_email,omitempty"`
	FromName        string `json:"from_name,omitempty"`
	ReplyTo         string `json:"reply_to,omitempty"`
	SenderAccountID string `json:"sender_account_id,omitempty"`
	IntegrationID   string `json:"integration_id,omitempty"`
}

// SendContactEmail sends one tracked email through the full pipeline:
// suppression gate, sending pool, open pixel and unsubscribe footer.
//
// A refused send is NOT an error — it comes back with Success false and a
// reason, because "we declined to mail this person" is a normal outcome that
// the caller should surface rather than retry.
func (c *Client) SendContactEmail(ctx context.Context, contactID string, in ContactEmail) (*SendEmailResult, error) {
	if contactID == "" {
		return nil, errors.New("salesshift.SendContactEmail: contactID is required")
	}
	if in.Subject == "" || in.BodyHTML == "" {
		return nil, errors.New("salesshift.SendContactEmail: Subject and BodyHTML are required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+contactID+"/send-email")
	var out SendEmailResult
	if err := c.T.JSON(ctx, "salesshift.SendContactEmail", "POST", url, in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.SendContactEmail: %w", err)
	}
	return &out, nil
}

// AddContactNote attaches a note, which also stamps last_activity.
func (c *Client) AddContactNote(ctx context.Context, contactID, content string) error {
	if contactID == "" || content == "" {
		return errors.New("salesshift.AddContactNote: contactID and content are required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+contactID+"/notes")
	body := map[string]string{"content": content}
	if err := c.T.JSON(ctx, "salesshift.AddContactNote", "POST", url, body, nil); err != nil {
		return fmt.Errorf("salesshift.AddContactNote: %w", err)
	}
	return nil
}

// Activity is one entry on a contact's timeline.
type Activity struct {
	ID           string `json:"id"`
	ActivityType string `json:"activity_type"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	CreatedAt    string `json:"created_at"`
}

// ContactActivities returns the contact's timeline, newest first.
func (c *Client) ContactActivities(ctx context.Context, contactID string) ([]Activity, error) {
	if contactID == "" {
		return nil, errors.New("salesshift.ContactActivities: contactID is required")
	}
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/contacts/"+contactID+"/activities")
	var raw struct {
		Data []Activity `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ContactActivities", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ContactActivities: %w", err)
	}
	return raw.Data, nil
}

// ContactList is a named audience.
type ContactList struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
	CreatedAt   string `json:"created_at"`
}

// ListContactLists returns this workspace's contact lists.
func (c *Client) ListContactLists(ctx context.Context) ([]ContactList, error) {
	url := transport.JoinURL(c.VxCloudURL, "/api/v1/salesshift/lists")
	var raw struct {
		Data []ContactList `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListContactLists", "GET", url, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListContactLists: %w", err)
	}
	return raw.Data, nil
}
