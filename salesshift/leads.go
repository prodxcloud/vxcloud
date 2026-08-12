package salesshift

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	vxerrors "github.com/prodxcloud/vxcloud/errors"
	"github.com/prodxcloud/vxcloud/transport"
)

// Leads — the global prospect pool, and this tenant's saved copies of it.
//
// Every endpoint below lives on the Infinity control plane, never the tenant
// node: the pool is global and the masking decision ("what has this org paid
// to see") is made there. The Go pool engine on the node is deliberately
// tenant-blind, so a client that talked to it directly would get rows nobody
// had paid for. c.InfinityURL is therefore absolute in every call here, the
// same way the email methods resolve theirs.
//
// Four things about this surface that are easy to get wrong, and expensive:
//
//  1. A Lead is NOT mailable. Nothing here may be handed to a send, sequence
//     or campaign call. ConvertLead / BulkConvertLeads / ConvertFromPool are
//     the only doors into a mailable Contact, and they are where consent
//     metadata is written. A scraped record reaching a send path is how a
//     tenant's sending domain dies.
//  2. An unrevealed address is a MASK ("j•••@acme.com"), not an address.
//     PoolPerson.HasEmail says one exists; PoolPerson.EmailRevealed says you
//     may see it. Use PoolPerson.MailableEmail to get a real address or
//     nothing — never read .Email and assume.
//  3. Reveal spends metered quota. RevealLeads makes the cost visible before
//     it acts; a hand-rolled loop over RevealLead does not.
//  4. ConvertFromPoolReport accounts for every id passed in. Print it with
//     String or Describe, which render every bucket — printing .Converted
//     alone hides a partial spend.

// leadsPrefix is shared by every route in this file. `/lead-searches` is a
// sibling of `/leads`, not a child, which is why it is joined separately.
const leadsPrefix = "/api/v1/salesshift"

const (
	// LeadsMaxPageSize is the server's cap on a search page. Asking for more
	// is silently clamped server-side; we clamp here too so the Limit a caller
	// reads back is the Limit that was sent.
	LeadsMaxPageSize = 100
	// LeadsMaxBatch is the server's per-call cap on save and convert-from-pool.
	// It exists because reveals are metered: a bigger batch would spend more
	// quota than anyone intends in one click.
	LeadsMaxBatch = 200
	// LeadWalkMaxPages bounds WalkLeads when the caller passes 0. A pool walk
	// with no ceiling is an unbounded spend of time and rate limit.
	LeadWalkMaxPages = 100
)

// Sortable fields. People accept score/name/title/company/location/employees/
// email; companies accept score/name/employees/industry/location. Anything
// else degrades to score descending SERVER-SIDE without complaint — which is
// why LeadSearchPage.Sort, the sort that was applied, is the one to render.
const (
	SortByScore     = "score"
	SortByName      = "name"
	SortByTitle     = "title"
	SortByCompany   = "company"
	SortByLocation  = "location"
	SortByEmployees = "employees"
	SortByEmail     = "email"
	SortByIndustry  = "industry" // companies only
)

func (c *Client) leadsURL(path string) string {
	return transport.JoinURL(c.InfinityURL, leadsPrefix+path)
}

// ---------------------------------------------------------------------------
// Request shapes
// ---------------------------------------------------------------------------

// LeadFilters narrows the pool. Every field is optional and an empty one is
// omitted from the wire, so the zero value means "the whole pool".
//
// MinScore, HasEmail and HasPhone are pointers on purpose: 0 and false are
// meaningful filter values, and a plain int/bool could not distinguish
// "score >= 0" from "no score filter".
type LeadFilters struct {
	// Q is free text over name, title and company.
	Q string `json:"q,omitempty"`
	// Titles match as substrings, OR'd together.
	Titles []string `json:"titles,omitempty"`
	// ExcludeTitles match as substrings and are AND'd as NOT.
	ExcludeTitles []string `json:"exclude_titles,omitempty"`
	// Seniorities: founder, c_suite, vp, director, manager, senior, entry.
	Seniorities []string `json:"seniorities,omitempty"`
	// Departments: engineering, sales, marketing, finance, hr, ops, legal,
	// executive.
	Departments []string `json:"departments,omitempty"`
	// Countries are ISO-2 and upper-cased by the server.
	Countries  []string `json:"countries,omitempty"`
	Industries []string `json:"industries,omitempty"`
	// EmailStatuses: verified, unverified, guessed, catch_all, invalid.
	EmailStatuses []string `json:"email_statuses,omitempty"`
	// EmployeeRanges: 1-10, 11-50, 51-200, 201-500, 501-1000, 1001-5000, 5000+.
	EmployeeRanges []string `json:"employee_ranges,omitempty"`
	CompanyDomains []string `json:"company_domains,omitempty"`
	// Keywords match company keywords as a SUBSTRING, so "saas" also finds a
	// company tagged "saas management".
	Keywords []string `json:"keywords,omitempty"`
	MinScore *int     `json:"min_score,omitempty"`
	// HasEmail filters on an address EXISTING, not on your having revealed it.
	HasEmail *bool `json:"has_email,omitempty"`
	HasPhone *bool `json:"has_phone,omitempty"`
}

// LeadSort names a column and a direction. Use the SortBy* constants — an
// unrecognised field is not an error, it is a silent degrade to score desc.
type LeadSort struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// SearchLeadsInput is one page request against the pool.
//
// Cursor is OPAQUE. Do not parse it, do not build one, and do not carry one
// across a change of Filters or Sort: a keyset position is only meaningful
// inside the result set AND the ordering it came from, so reusing it after a
// re-sort compares the wrong column and silently drops or repeats rows. Start
// a new search with an empty Cursor.
type SearchLeadsInput struct {
	Filters LeadFilters `json:"filters"`
	Cursor  string      `json:"cursor,omitempty"`
	// Limit defaults to 25 server-side and is capped at LeadsMaxPageSize.
	Limit int       `json:"limit,omitempty"`
	Sort  *LeadSort `json:"sort,omitempty"`
}

// leadSearchRequest is the wire shape. result_type is set by the method rather
// than by the caller, so SearchLeads cannot return company rows into a
// []PoolPerson and vice versa.
type leadSearchRequest struct {
	Filters    LeadFilters `json:"filters"`
	ResultType string      `json:"result_type"`
	Cursor     string      `json:"cursor"`
	Limit      int         `json:"limit"`
	Sort       *LeadSort   `json:"sort,omitempty"`
}

func (in SearchLeadsInput) wire(resultType string) leadSearchRequest {
	limit := in.Limit
	if limit > LeadsMaxPageSize {
		limit = LeadsMaxPageSize
	}
	if limit < 0 {
		limit = 0
	}
	return leadSearchRequest{
		Filters:    in.Filters,
		ResultType: resultType,
		Cursor:     in.Cursor,
		Limit:      limit,
		Sort:       in.Sort,
	}
}

// ---------------------------------------------------------------------------
// Response shapes
// ---------------------------------------------------------------------------

// LeadScore wraps the fit score as an object.
//
// It is an object rather than an int because the two backends disagree: the Go
// pool engine emits a flat number, the ORM fallback an object, and Infinity
// normalises both to {value} before replying. Decoding into an int would work
// against one backend and quietly render every row as zero against the other.
type LeadScore struct {
	Value int `json:"value"`
}

// LeadPage is the pagination envelope shared by people and company searches.
type LeadPage struct {
	ResultType string `json:"result_type"` // person | company
	// Total is CAPPED AT 10000. Read DisplayTotal instead of formatting this.
	Total int `json:"total"`
	// TotalDisplay is the humanised count — "260", "1.4K", or "10,000+".
	TotalDisplay string `json:"total_display"`
	// TotalIsEstimate is true once the count hit the cap. Printing Total then
	// tells the user there are exactly 10,000 matches when there may be
	// millions.
	TotalIsEstimate bool `json:"total_is_estimate"`
	// NextCursor is empty on the last page. Opaque — feed it back verbatim.
	NextCursor string `json:"next_cursor"`
	// SearchBackend is "go-node" normally, "fastapi-orm" during a node outage.
	// Surfaced so a drift between the two shows up in the payload rather than
	// in a support ticket.
	SearchBackend string `json:"search_backend"`
	// Sort is the sort the server APPLIED, which is not necessarily the one
	// requested. Render this one.
	Sort LeadSort `json:"sort"`
}

// DisplayTotal is the string to put in front of a user. It honours the cap:
// past 10,000 the server stops counting and the honest answer is "10,000+".
func (p LeadPage) DisplayTotal() string {
	if p.TotalDisplay != "" {
		return p.TotalDisplay
	}
	if p.TotalIsEstimate {
		return strconv.Itoa(p.Total) + "+"
	}
	return strconv.Itoa(p.Total)
}

// HasMore reports whether another page exists.
func (p LeadPage) HasMore() bool { return p.NextCursor != "" }

// LeadSearchPage is a page of pool PEOPLE.
type LeadSearchPage struct {
	LeadPage
	Items []PoolPerson `json:"items"`
}

// CompanySearchPage is a page of pool COMPANIES. Company search is unpaged
// server-side, so NextCursor is always empty here.
type CompanySearchPage struct {
	LeadPage
	Items []PoolCompanyRow `json:"items"`
}

// PoolPerson is one person row in a pool search.
//
// Email is the DISPLAY value: the real address when EmailRevealed is true, the
// mask ("j•••@acme.com") when it is false. Never feed it to anything that
// sends — call MailableEmail instead, which returns nothing when the address
// has not been paid for.
type PoolPerson struct {
	PoolID     string `json:"pool_id"`
	FullName   string `json:"full_name"`
	Title      string `json:"title"`
	Seniority  string `json:"seniority"`
	Department string `json:"department"`
	// Email is masked unless EmailRevealed. See MailableEmail.
	Email string `json:"email"`
	// EmailMasked is the mask itself. Only the node-backed path sends it; the
	// ORM fallback puts the mask straight into Email.
	EmailMasked string `json:"email_masked,omitempty"`
	// EmailRevealed says whether you may SEE the address.
	EmailRevealed bool   `json:"email_revealed"`
	EmailStatus   string `json:"email_status"`
	// HasEmail says an address EXISTS. It is not permission to read one.
	HasEmail bool `json:"has_email"`
	// Phone is empty unless revealed.
	Phone          string `json:"phone,omitempty"`
	PhoneAvailable bool   `json:"phone_available"`
	PhoneCount     int    `json:"phone_count"`
	// LinkedInURL is empty unless revealed.
	LinkedInURL string           `json:"linkedin_url,omitempty"`
	Location    string           `json:"location"`
	Score       LeadScore        `json:"score"`
	Company     PoolCompanyBrief `json:"company"`
	// SavedLeadID is set once this org has saved the row.
	SavedLeadID string `json:"saved_lead_id,omitempty"`
}

// MailableEmail returns the real address, and true, only when this org has
// revealed it.
//
// This is the accessor to use anywhere an address might travel: reading .Email
// directly hands back a mask that looks close enough to an address to survive
// a copy-paste, a CSV export, or an import into a sending tool — and a mask in
// a send path is a hard bounce with the tenant's domain on it.
func (p PoolPerson) MailableEmail() (string, bool) {
	if !p.EmailRevealed || p.Email == "" {
		return "", false
	}
	return p.Email, true
}

// NeedsReveal reports that an address exists but has not been paid for — the
// state where offering a reveal is useful and claiming "no email" is wrong.
func (p PoolPerson) NeedsReveal() bool { return p.HasEmail && !p.EmailRevealed }

// PoolCompanyBrief is the company summary carried inside a person row.
// IndustriesMore and KeywordsMore are the counts truncated away, so a UI can
// render "design, saas +2" without a second request.
type PoolCompanyBrief struct {
	PoolID         string   `json:"pool_id,omitempty"`
	Name           string   `json:"name"`
	Domain         string   `json:"domain"`
	LogoURL        string   `json:"logo_url,omitempty"`
	EmployeeCount  int      `json:"employee_count,omitempty"`
	EmployeeRange  string   `json:"employee_range,omitempty"`
	Industry       string   `json:"industry,omitempty"`
	IndustriesMore int      `json:"industries_more"`
	Keywords       []string `json:"keywords"`
	KeywordsMore   int      `json:"keywords_more"`
}

// PoolCompanyRow is one company row in a company search.
type PoolCompanyRow struct {
	PoolID         string    `json:"pool_id"`
	Name           string    `json:"name"`
	Domain         string    `json:"domain"`
	LogoURL        string    `json:"logo_url,omitempty"`
	Industry       string    `json:"industry,omitempty"`
	IndustriesMore int       `json:"industries_more"`
	EmployeeCount  int       `json:"employee_count,omitempty"`
	EmployeeRange  string    `json:"employee_range,omitempty"`
	Location       string    `json:"location,omitempty"`
	Keywords       []string  `json:"keywords"`
	KeywordsMore   int       `json:"keywords_more"`
	Score          LeadScore `json:"score"`
}

// FacetBucket is one value and its count.
type FacetBucket struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// LeadFacetsResult holds the counts rendered beside each filter.
//
// Facets are pool-wide and carry no tenant overlay — the number of Directors
// in the pool is the same for everyone. Industry is a company facet and is
// absent from the ORM fallback's reply, so treat a nil slice as "not
// available", not as "zero".
type LeadFacetsResult struct {
	Seniority     []FacetBucket `json:"seniority"`
	Department    []FacetBucket `json:"department"`
	Country       []FacetBucket `json:"country"`
	EmailStatus   []FacetBucket `json:"email_status"`
	Industry      []FacetBucket `json:"industry"`
	SearchBackend string        `json:"search_backend"`
}

// RevealQuotaStatus is the metered reveal allowance for the current period.
// Remaining is sent by the server rather than derived, because a client that
// computes it will eventually compute it wrong at exactly the moment the
// number is being read.
// RevealQuotaStatus is the reveal meter as returned by RevealQuota.
//
// NOTE: GetRevealQuota (tasks.go) returns RevealQuota for the SAME endpoint,
// /api/v1/salesshift/leads/quota. The two are kept because both are public API
// and removing either would break callers; they now carry the same fields.
type RevealQuotaStatus struct {
	Used int `json:"used"`
	// -1 when the org is uncapped. Branch on Unlimited rather than testing this
	// for -1 — a caller that renders Allowance verbatim shows "-1".
	Allowance int `json:"allowance"`
	// True when the org has no cap. Without it a caller sees Allowance == -1
	// and cannot tell "uncapped" from "broken", so uncapped orgs get told their
	// allowance is spent. The server has always sent this; the field was simply
	// missing here, so encoding/json discarded it.
	Unlimited bool `json:"unlimited"`
	// Large finite number when uncapped (never negative), so integer
	// comparisons and arithmetic keep working.
	Remaining int    `json:"remaining"`
	Display   string `json:"display"` // "3 / 200", or "9 revealed" when uncapped
}

// RevealResult is one un-masked person plus the meter after the spend.
type RevealResult struct {
	PoolID      string            `json:"pool_id"`
	Email       string            `json:"email"`
	Phone       string            `json:"phone,omitempty"`
	LinkedInURL string            `json:"linkedin_url,omitempty"`
	Quota       RevealQuotaStatus `json:"quota"`
}

// SaveLeadsResult reports a snapshot copy into the tenant's saved leads.
// AlreadySaved counts the ids this org was already holding — they cost
// nothing and are not re-snapshotted.
type SaveLeadsResult struct {
	Saved        int `json:"saved"`
	AlreadySaved int `json:"already_saved"`
}

// PoolPersonDetail is everything the pool knows about one person, plus this
// org's relationship to them. Masking applies exactly as it does in search — a
// detail view is not a back door around the meter.
type PoolPersonDetail struct {
	PoolID        string `json:"pool_id"`
	FullName      string `json:"full_name"`
	FirstName     string `json:"first_name"`
	LastName      string `json:"last_name"`
	Title         string `json:"title"`
	Seniority     string `json:"seniority"`
	Department    string `json:"department"`
	Email         string `json:"email"` // masked unless EmailRevealed
	EmailRevealed bool   `json:"email_revealed"`
	EmailStatus   string `json:"email_status"`
	// EmailConfidence is 0-100, and is the honest counterweight to a
	// confident-looking record.
	EmailConfidence    int                `json:"email_confidence,omitempty"`
	HasEmail           bool               `json:"has_email"`
	Phone              string             `json:"phone,omitempty"`
	PhoneCount         int                `json:"phone_count"`
	PhoneAvailable     bool               `json:"phone_available"`
	LinkedInURL        string             `json:"linkedin_url,omitempty"`
	LinkedInAvailable  bool               `json:"linkedin_available"`
	City               string             `json:"city"`
	Country            string             `json:"country"`
	Location           string             `json:"location"`
	Score              LeadScore          `json:"score"`
	Source             string             `json:"source"`
	FirstSeenAt        string             `json:"first_seen_at,omitempty"`
	LastVerifiedAt     string             `json:"last_verified_at,omitempty"`
	Company            PoolCompanyProfile `json:"company"`
	SavedLeadID        string             `json:"saved_lead_id,omitempty"`
	SavedStatus        string             `json:"saved_status,omitempty"`
	ConvertedContactID string             `json:"converted_contact_id,omitempty"`
	// ExistingContactID is set when this org already holds the person as a
	// Contact. It is what stops someone spending a reveal on data they own.
	ExistingContactID string `json:"existing_contact_id,omitempty"`
}

// MailableEmail returns the real address, and true, only when revealed.
// See PoolPerson.MailableEmail — the same rule applies to the detail view.
func (p PoolPersonDetail) MailableEmail() (string, bool) {
	if !p.EmailRevealed || p.Email == "" {
		return "", false
	}
	return p.Email, true
}

// NeedsReveal reports that an address exists but has not been paid for.
func (p PoolPersonDetail) NeedsReveal() bool { return p.HasEmail && !p.EmailRevealed }

// PoolCompanyProfile is the company as carried on a person detail record.
type PoolCompanyProfile struct {
	PoolID        string    `json:"pool_id,omitempty"`
	Name          string    `json:"name"`
	Domain        string    `json:"domain"`
	Website       string    `json:"website,omitempty"`
	LinkedInURL   string    `json:"linkedin_url,omitempty"`
	LogoURL       string    `json:"logo_url,omitempty"`
	Description   string    `json:"description,omitempty"`
	Industry      string    `json:"industry,omitempty"`
	Industries    []string  `json:"industries"`
	EmployeeCount int       `json:"employee_count,omitempty"`
	EmployeeRange string    `json:"employee_range,omitempty"`
	RevenueRange  string    `json:"revenue_range,omitempty"`
	FoundedYear   int       `json:"founded_year,omitempty"`
	City          string    `json:"city,omitempty"`
	Country       string    `json:"country,omitempty"`
	Keywords      []string  `json:"keywords"`
	TechStack     []string  `json:"tech_stack"`
	Score         LeadScore `json:"score"`
}

// PoolCompany is a company plus the people behind it, split by what this org
// already owns.
//
// NewProspects and ExistingContacts are the split an account page actually
// needs — "what is left to work" rather than a raw headcount — and the server
// computes it so two endpoints do not have to be cross-referenced by hand.
// People holds both, in score order, capped at 100 rows.
type PoolCompany struct {
	PoolCompanyProfile
	LegalName        string          `json:"legal_name,omitempty"`
	StateProvince    string          `json:"state_province,omitempty"`
	Location         string          `json:"location,omitempty"`
	Source           string          `json:"source,omitempty"`
	FirstSeenAt      string          `json:"first_seen_at,omitempty"`
	LastVerifiedAt   string          `json:"last_verified_at,omitempty"`
	IsActive         bool            `json:"is_active"`
	People           []CompanyPerson `json:"people"`
	NewProspects     []CompanyPerson `json:"new_prospects"`
	ExistingContacts []CompanyPerson `json:"existing_contacts"`
	PeopleTotal      int             `json:"people_total"`
	Departments      []FacetBucket   `json:"departments"`
}

// CompanyPerson is one person on a company page. Same masking rules as
// everywhere else.
type CompanyPerson struct {
	PoolID         string    `json:"pool_id"`
	FullName       string    `json:"full_name"`
	Title          string    `json:"title"`
	Seniority      string    `json:"seniority"`
	Department     string    `json:"department"`
	Email          string    `json:"email"` // masked unless EmailRevealed
	EmailRevealed  bool      `json:"email_revealed"`
	EmailStatus    string    `json:"email_status"`
	HasEmail       bool      `json:"has_email"`
	PhoneAvailable bool      `json:"phone_available"`
	PhoneCount     int       `json:"phone_count"`
	Location       string    `json:"location"`
	Score          LeadScore `json:"score"`
	SavedLeadID    string    `json:"saved_lead_id,omitempty"`
	// ExistingContactID is set when this org already holds them as a Contact.
	ExistingContactID string `json:"existing_contact_id,omitempty"`
}

// MailableEmail returns the real address, and true, only when revealed.
func (p CompanyPerson) MailableEmail() (string, bool) {
	if !p.EmailRevealed || p.Email == "" {
		return "", false
	}
	return p.Email, true
}

// Lead is one of this tenant's saved leads — a SNAPSHOT taken at save time,
// not a live view, because the pool is re-crawled continuously and a qualified
// list must not mutate under the person who qualified it.
//
// A Lead is NOT mailable. Passing one to a send, sequence or campaign call is
// the failure this whole surface exists to prevent; ConvertLead is the only
// route to a Contact, and that is where consent metadata is written.
type Lead struct {
	ID           string `json:"id"`
	PoolPersonID string `json:"pool_person_id,omitempty"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	FullName     string `json:"full_name"`
	Title        string `json:"title"`
	Seniority    string `json:"seniority"`
	Department   string `json:"department"`
	// Email is populated only once this org has revealed it. Empty with
	// HasEmail true means "not paid for yet", not "no address" — see
	// NeedsReveal.
	Email       string `json:"email,omitempty"`
	EmailStatus string `json:"email_status"`
	// EmailMasked is the live pool mask, present when the pool row was loaded
	// alongside the snapshot.
	EmailMasked    string      `json:"email_masked,omitempty"`
	HasEmail       bool        `json:"has_email"`
	Phone          string      `json:"phone,omitempty"`
	LinkedInURL    string      `json:"linkedin_url,omitempty"`
	PhoneAvailable bool        `json:"phone_available"`
	Company        LeadCompany `json:"company"`
	Location       string      `json:"location"`
	Status         string      `json:"status"` // new | contacted | qualified | converted | …
	Score          LeadScore   `json:"score"`
	Source         string      `json:"source"`
	Notes          string      `json:"notes,omitempty"`
	Tags           []string    `json:"tags"`
	OwnerID        int         `json:"owner_id,omitempty"`
	// ErasurePending is true once the person has exercised their right to be
	// forgotten. The contact fields have been stripped and conversion is
	// refused — this is terminal, not a transient state to retry through.
	ErasurePending     bool   `json:"erasure_pending"`
	ConvertedContactID string `json:"converted_contact_id,omitempty"`
	ConvertedAt        string `json:"converted_at,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
	// Pool is the live pool row behind the snapshot. Set by GetLead only.
	Pool *LeadPoolSnapshot `json:"pool,omitempty"`
	// Drift is non-empty when the pool has moved on since the snapshot was
	// taken — shown so someone working a list learns of a title or company
	// change before a bounce teaches them. Set by GetLead only.
	Drift []string `json:"drift,omitempty"`
}

// NeedsReveal reports that the pool holds an address for this lead that this
// org has not paid to see. Revealing it back-fills the snapshot server-side,
// which is what unblocks ConvertLead.
func (l Lead) NeedsReveal() bool {
	return l.HasEmail && l.Email == "" && !l.ErasurePending
}

// Convertible reports whether ConvertLead would be accepted, and why not when
// it would not. Checking here saves a round trip that can only ever 400.
func (l Lead) Convertible() (bool, string) {
	switch {
	case l.ErasurePending:
		return false, "this record was removed at the person's request and cannot be converted"
	case l.ConvertedContactID != "":
		return false, "already converted"
	case l.Email == "" && !l.HasEmail:
		// The pool has no address at all — revealing would find nothing, so
		// telling someone to "reveal it first" sends them to spend quota on a
		// dead end. HasEmail is what distinguishes the two cases.
		return false, "the pool has no address for this person, so there is nothing to convert"
	case l.Email == "":
		return false, "reveal this lead's email before converting — a contact without an address cannot be emailed"
	}
	return true, ""
}

// LeadCompany is the company as frozen into a saved lead.
type LeadCompany struct {
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	EmployeeRange string `json:"employee_range,omitempty"`
	Industry      string `json:"industry,omitempty"`
}

// LeadPoolSnapshot is the live pool row shown beside a saved lead so the two
// can be compared.
type LeadPoolSnapshot struct {
	PoolID         string `json:"pool_id"`
	IsActive       bool   `json:"is_active"`
	EmailRevealed  bool   `json:"email_revealed"`
	EmailStatus    string `json:"email_status"`
	Title          string `json:"title"`
	CompanyName    string `json:"company_name"`
	QualityScore   int    `json:"quality_score"`
	LastVerifiedAt string `json:"last_verified_at,omitempty"`
}

// ListLeadsInput filters the tenant's saved leads.
type ListLeadsInput struct {
	Status string // new | contacted | qualified | converted | disqualified
	Limit  int    // server default 100, server max 500
}

// UpdateLeadInput is a partial update: only non-nil fields are sent, and only
// sent fields are written. Tags replaces the whole list when non-nil.
type UpdateLeadInput struct {
	Status           *string   `json:"status,omitempty"`
	Score            *int      `json:"score,omitempty"`
	Notes            *string   `json:"notes,omitempty"`
	DisqualifyReason *string   `json:"disqualify_reason,omitempty"`
	OwnerID          *int      `json:"owner_id,omitempty"`
	Tags             *[]string `json:"tags,omitempty"`
}

// ConvertLeadInput carries the optional lifecycle stage for the new Contact.
type ConvertLeadInput struct {
	LifecycleStage string `json:"lifecycle_stage,omitempty"` // default "lead"
}

// ConvertLeadResult is the outcome of a single conversion.
//
// ReusedExistingContact matters: an address this org already held is linked
// rather than duplicated, so a "converted" result does not always mean a new
// Contact was created.
type ConvertLeadResult struct {
	ContactID             string `json:"contact_id"`
	AlreadyConverted      bool   `json:"already_converted"`
	ReusedExistingContact bool   `json:"reused_existing_contact"`
}

// BulkConvertResult accounts for every saved lead passed to BulkConvertLeads.
// Print it with String — the skipped buckets are the interesting half.
type BulkConvertResult struct {
	Requested        int `json:"-"`
	Converted        int `json:"converted"`
	SkippedNoEmail   int `json:"skipped_no_email"`
	AlreadyConverted int `json:"already_converted"`
}

// String renders every bucket, including the zeros, so that reporting the
// whole outcome is easier than reporting only the successes.
func (r BulkConvertResult) String() string {
	return fmt.Sprintf(
		"bulk-convert: %d requested — converted %d, already converted %d, skipped (no email) %d",
		r.Requested, r.Converted, r.AlreadyConverted, r.SkippedNoEmail)
}

// ConvertFromPoolInput drives the one-step pool → Contact path.
//
// RevealIfNeeded has NO omitempty on purpose. The server defaults a missing
// key to true; if false were dropped from the payload, a caller who explicitly
// asked not to spend quota would spend it. The Go zero value is therefore the
// safe one: a zero-value input converts only already-revealed records and
// reports the rest as SkippedNoQuota, spending nothing.
type ConvertFromPoolInput struct {
	PoolPersonIDs  []string `json:"pool_person_ids"`
	RevealIfNeeded bool     `json:"reveal_if_needed"`
	LifecycleStage string   `json:"lifecycle_stage,omitempty"` // default "lead"
}

// ConvertFromPoolReport accounts for EVERY id passed in.
//
// The five terminal buckets — Converted, AlreadyConverted, SkippedNoQuota,
// SkippedNoEmail, SkippedErased — sum to the number of ids requested.
// RevealedNow is not a sixth bucket: it counts the reveals performed along the
// way and overlaps Converted.
//
// Render all of them. String and Describe exist so that doing so is the path
// of least resistance; a partial spend reported as a bare success count is how
// a tenant stops believing the meter.
type ConvertFromPoolReport struct {
	// Requested is filled in by the SDK from the ids actually sent, so
	// Unaccounted can be checked without the caller keeping its own count.
	Requested        int               `json:"-"`
	Converted        int               `json:"converted"`
	RevealedNow      int               `json:"revealed_now"`
	AlreadyConverted int               `json:"already_converted"`
	SkippedNoQuota   int               `json:"skipped_no_quota"`
	SkippedNoEmail   int               `json:"skipped_no_email"`
	SkippedErased    int               `json:"skipped_erased"`
	ContactIDs       []string          `json:"contact_ids"`
	Quota            RevealQuotaStatus `json:"quota"`
}

// Accounted is the sum of the terminal buckets.
func (r ConvertFromPoolReport) Accounted() int {
	return r.Converted + r.AlreadyConverted + r.SkippedNoQuota +
		r.SkippedNoEmail + r.SkippedErased
}

// Unaccounted is how many requested ids the server did not classify. It should
// always be zero; a non-zero value means the report and the request disagree
// and the difference must be surfaced, not rounded away.
func (r ConvertFromPoolReport) Unaccounted() int {
	if r.Requested == 0 {
		return 0
	}
	return r.Requested - r.Accounted()
}

// Partial reports whether anything was skipped — the signal that "converted N"
// alone would be a misleading thing to show.
func (r ConvertFromPoolReport) Partial() bool {
	return r.SkippedNoQuota+r.SkippedNoEmail+r.SkippedErased > 0 || r.Unaccounted() != 0
}

// String renders every bucket on one line, zeros included.
func (r ConvertFromPoolReport) String() string {
	return fmt.Sprintf(
		"convert-from-pool: %d requested — converted %d (revealed now %d), "+
			"already converted %d, skipped: no quota %d, no email %d, erased %d — quota %s",
		r.Requested, r.Converted, r.RevealedNow, r.AlreadyConverted,
		r.SkippedNoQuota, r.SkippedNoEmail, r.SkippedErased, r.Quota.Display)
}

// Describe renders every bucket as a block, with the reveals that were spent
// and a warning if the buckets do not add up to the request.
func (r ConvertFromPoolReport) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "convert-from-pool — %d requested\n", r.Requested)
	fmt.Fprintf(&b, "  converted           %d\n", r.Converted)
	fmt.Fprintf(&b, "  already converted   %d\n", r.AlreadyConverted)
	fmt.Fprintf(&b, "  skipped, no quota   %d\n", r.SkippedNoQuota)
	fmt.Fprintf(&b, "  skipped, no email   %d\n", r.SkippedNoEmail)
	fmt.Fprintf(&b, "  skipped, erased     %d\n", r.SkippedErased)
	fmt.Fprintf(&b, "  reveals spent       %d\n", r.RevealedNow)
	if r.Quota.Display != "" {
		fmt.Fprintf(&b, "  reveal quota        %s (%d remaining)\n",
			r.Quota.Display, r.Quota.Remaining)
	}
	if n := r.Unaccounted(); n != 0 {
		fmt.Fprintf(&b, "  UNACCOUNTED         %d — the server did not classify these ids\n", n)
	}
	return b.String()
}

// ErasureInput is a right-to-be-forgotten request. Supply Email or
// LinkedInURL (or both).
//
// Confirm is not sent to the server; it is a local gate. Erasure is GLOBAL —
// it removes the person from the pool for EVERY tenant, not only the caller's
// — and irreversible, so this SDK will not issue one that a caller did not
// explicitly acknowledge.
type ErasureInput struct {
	Email       string `json:"email,omitempty"`
	LinkedInURL string `json:"linkedin_url,omitempty"`
	Reason      string `json:"reason,omitempty"` // default "gdpr_erasure"
	Note        string `json:"note,omitempty"`
	Confirm     bool   `json:"-"`
}

// ErasureResult reports the blast radius. SavedLeadsFlagged counts copies
// stripped across ALL organisations, which is the point: an erasure that
// cleaned only the requester's copy would be theatre.
type ErasureResult struct {
	PoolRowsErased    int  `json:"pool_rows_erased"`
	SavedLeadsFlagged int  `json:"saved_leads_flagged"`
	AlreadyRecorded   bool `json:"already_recorded"`
}

// SavedSearch is a stored filter set.
type SavedSearch struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Filters  LeadFilters `json:"filters"`
	IsShared bool        `json:"is_shared"`
}

// SaveSearchInput creates a saved search.
type SaveSearchInput struct {
	Name     string      `json:"name"`
	Filters  LeadFilters `json:"filters"`
	IsShared bool        `json:"is_shared"`
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// SearchLeads returns a page of pool PEOPLE.
//
// Rows come back masked unless this org has revealed them; see
// PoolPerson.MailableEmail. Read the page's Sort, not the one you asked for —
// an unknown sort field degrades to score desc server-side. Read DisplayTotal,
// not Total, which is capped at 10,000.
//
// To page, feed NextCursor back verbatim with Filters and Sort unchanged, or
// use WalkLeads which does that correctly.
func (c *Client) SearchLeads(ctx context.Context, in SearchLeadsInput) (*LeadSearchPage, error) {
	var raw struct {
		Success bool           `json:"success"`
		Data    LeadSearchPage `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.SearchLeads", "POST",
		c.leadsURL("/leads/search"), in.wire("person"), &raw); err != nil {
		return nil, fmt.Errorf("salesshift.SearchLeads: %w", err)
	}
	return &raw.Data, nil
}

// SearchCompanies returns a page of pool COMPANIES.
//
// Separate from SearchLeads rather than a result_type flag on one method: the
// two shapes share nothing but the envelope, and a single method would force
// every caller to type-assert its way to the rows it asked for.
//
// Company search is unpaged server-side — NextCursor is always empty — so
// narrow with Filters rather than expecting to walk.
func (c *Client) SearchCompanies(ctx context.Context, in SearchLeadsInput) (*CompanySearchPage, error) {
	var raw struct {
		Success bool              `json:"success"`
		Data    CompanySearchPage `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.SearchCompanies", "POST",
		c.leadsURL("/leads/search"), in.wire("company"), &raw); err != nil {
		return nil, fmt.Errorf("salesshift.SearchCompanies: %w", err)
	}
	return &raw.Data, nil
}

// ErrWalkTruncated is returned by WalkLeads and SearchAllLeads when the page
// bound was reached with results still outstanding.
//
// It is deliberately an error rather than a silent stop: the caller is holding
// an INCOMPLETE set, and a nil error would invite them to treat it as the
// whole pool. Check with errors.Is and either raise the bound or narrow the
// filters.
var ErrWalkTruncated = errors.New("salesshift: lead walk stopped at the page limit — more results remain")

// WalkLeads pages a search to exhaustion, calling visit once per page.
//
// This exists because hand-rolled cursor loops go wrong in the same three ways
// every time: they mutate the sort or filters mid-walk (which makes the cursor
// compare the wrong column and silently drop rows), they run unbounded, and
// they spin forever if the server ever repeats a cursor. Filters and Sort are
// frozen for the whole walk here, the page count is bounded by maxPages
// (LeadWalkMaxPages when 0), and a repeated cursor aborts.
//
// in.Cursor is honoured as a starting position — useful for resuming — but it
// MUST have come from a search with identical filters and sort.
//
// visit's error stops the walk and is returned unwrapped, so sentinel values a
// caller defines still compare equal. Reaching maxPages with a cursor still
// outstanding returns ErrWalkTruncated after the last page was visited.
func (c *Client) WalkLeads(ctx context.Context, in SearchLeadsInput, maxPages int,
	visit func(page *LeadSearchPage) error) error {
	if visit == nil {
		return errors.New("salesshift.WalkLeads: visit is required")
	}
	if maxPages <= 0 {
		maxPages = LeadWalkMaxPages
	}
	// A walk at the default page size is four times the round trips for the
	// same rows, so ask for full pages unless the caller had a reason not to.
	if in.Limit <= 0 {
		in.Limit = LeadsMaxPageSize
	}

	seen := make(map[string]bool, maxPages)
	for page := 0; page < maxPages; page++ {
		res, err := c.SearchLeads(ctx, in)
		if err != nil {
			return fmt.Errorf("salesshift.WalkLeads: page %d: %w", page+1, err)
		}
		if err := visit(res); err != nil {
			return err
		}
		// An empty page with a cursor attached would loop forever; treat the
		// absence of rows as the end regardless of what the cursor says.
		if res.NextCursor == "" || len(res.Items) == 0 {
			return nil
		}
		if seen[res.NextCursor] {
			return fmt.Errorf(
				"salesshift.WalkLeads: server repeated a cursor after page %d — refusing to loop",
				page+1)
		}
		seen[res.NextCursor] = true
		in.Cursor = res.NextCursor
	}
	return fmt.Errorf("salesshift.WalkLeads: %w", ErrWalkTruncated)
}

// SearchAllLeads collects a whole search into one slice, bounded by maxPages
// (LeadWalkMaxPages when 0).
//
// It returns the rows gathered so far ALONGSIDE any error, including
// ErrWalkTruncated, so a truncated walk still hands back what it found rather
// than discarding it. Check errors.Is(err, ErrWalkTruncated) before treating
// the slice as the complete result.
func (c *Client) SearchAllLeads(ctx context.Context, in SearchLeadsInput, maxPages int) ([]PoolPerson, error) {
	var all []PoolPerson
	err := c.WalkLeads(ctx, in, maxPages, func(page *LeadSearchPage) error {
		all = append(all, page.Items...)
		return nil
	})
	return all, err
}

// LeadFacets returns the counts rendered beside each filter. Facets are
// pool-wide: they answer "how many Directors exist", not "how many you may
// see", so they carry no tenant overlay and cost no quota.
func (c *Client) LeadFacets(ctx context.Context, filters LeadFilters) (*LeadFacetsResult, error) {
	body := map[string]any{"filters": filters}
	var raw struct {
		Success bool             `json:"success"`
		Data    LeadFacetsResult `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.LeadFacets", "POST",
		c.leadsURL("/leads/facets"), body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.LeadFacets: %w", err)
	}
	return &raw.Data, nil
}

// ---------------------------------------------------------------------------
// Reveal — the quota point
// ---------------------------------------------------------------------------

// RevealQuota reports reveals used, the allowance, and what remains this
// period. Free to call, and the honest thing to show before any bulk action.
func (c *Client) RevealQuota(ctx context.Context) (*RevealQuotaStatus, error) {
	var raw struct {
		Success bool              `json:"success"`
		Data    RevealQuotaStatus `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.RevealQuota", "GET",
		c.leadsURL("/leads/quota"), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.RevealQuota: %w", err)
	}
	return &raw.Data, nil
}

// RevealLead un-masks one person and SPENDS ONE REVEAL from the metered
// allowance. Re-revealing a person this org already owns is free — the server
// will not charge twice for the same row.
//
// Revealing also back-fills any saved lead this org holds for the person that
// was saved while masked, which is what unblocks ConvertLead on it.
//
// Errors worth naming rather than swallowing:
//   - IsQuotaExhausted (402) — the allowance is gone and NOTHING was charged
//     for this attempt.
//   - IsErased (410) — removed at the person's request. Terminal, not an
//     outage, not retryable.
//   - IsLeadNotFound (404) — no such row in the pool.
func (c *Client) RevealLead(ctx context.Context, poolPersonID string) (*RevealResult, error) {
	if poolPersonID == "" {
		return nil, errors.New("salesshift.RevealLead: poolPersonID is required")
	}
	body := map[string]string{"pool_person_id": poolPersonID}
	var raw struct {
		Success bool         `json:"success"`
		Data    RevealResult `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.RevealLead", "POST",
		c.leadsURL("/leads/reveal"), body, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.RevealLead: %w", err)
	}
	return &raw.Data, nil
}

// Reasons a BulkRevealSkip can carry.
const (
	BulkRevealNoQuota  = "no_quota"  // the allowance ran out; nothing was charged
	BulkRevealErased   = "erased"    // 410, terminal
	BulkRevealNotFound = "not_found" // 404, not in the pool
)

// BulkRevealInput drives RevealLeads.
type BulkRevealInput struct {
	PoolPersonIDs []string
	// AllowPartial permits spending whatever is left when the batch is larger
	// than the remaining allowance. Without it the whole batch is refused
	// BEFORE anything is charged, because a half-completed metered spend the
	// caller did not ask for is worse than no spend at all.
	AllowPartial bool
}

// BulkRevealSkip is one id that was not revealed, and why.
type BulkRevealSkip struct {
	PoolID string
	Reason string // BulkRevealNoQuota | BulkRevealErased | BulkRevealNotFound
	Err    error
}

// BulkRevealReport accounts for every id passed to RevealLeads:
// len(Revealed) + len(Skipped) == Requested.
type BulkRevealReport struct {
	Requested int
	// Revealed includes rows this org already owned, which cost nothing.
	Revealed []RevealResult
	Skipped  []BulkRevealSkip
	// Quota is the meter as of the last call made — the pre-flight reading if
	// nothing was revealed.
	Quota RevealQuotaStatus
}

// CountBy counts skips with the given reason.
func (r BulkRevealReport) CountBy(reason string) int {
	n := 0
	for _, s := range r.Skipped {
		if s.Reason == reason {
			n++
		}
	}
	return n
}

// String renders every bucket, so the cost and the shortfall are reported
// together rather than the success count alone.
func (r BulkRevealReport) String() string {
	return fmt.Sprintf(
		"reveal: %d requested — revealed %d, skipped: no quota %d, erased %d, not found %d — quota %s",
		r.Requested, len(r.Revealed),
		r.CountBy(BulkRevealNoQuota), r.CountBy(BulkRevealErased),
		r.CountBy(BulkRevealNotFound), r.Quota.Display)
}

// RevealLeads reveals many people, one metered call each, and reports the cost.
//
// There is no bulk reveal endpoint; the value here is the accounting a
// hand-written loop does not do. Before spending anything it reads the meter,
// and refuses a batch bigger than the remaining allowance unless AllowPartial
// is set — the refusal names the cost and charges nothing. The check is
// deliberately conservative: it assumes every id costs one reveal, because
// finding out which are already owned would cost a round trip each.
//
// On 402 mid-batch the remaining ids are recorded as BulkRevealNoQuota and the
// walk stops rather than issuing a request per id that cannot succeed. Erased
// (410) and missing (404) ids are recorded and skipped. Any other error aborts
// and is returned with the partial report, never discarded.
func (c *Client) RevealLeads(ctx context.Context, in BulkRevealInput) (*BulkRevealReport, error) {
	ids := dedupeIDs(in.PoolPersonIDs)
	if len(ids) == 0 {
		return nil, errors.New("salesshift.RevealLeads: PoolPersonIDs is required")
	}

	quota, err := c.RevealQuota(ctx)
	if err != nil {
		return nil, fmt.Errorf("salesshift.RevealLeads: pre-flight quota: %w", err)
	}
	if len(ids) > quota.Remaining && !in.AllowPartial {
		return nil, fmt.Errorf(
			"salesshift.RevealLeads: %d reveals requested but only %d remain this period (%s) — "+
				"nothing was spent; set AllowPartial to spend what is left",
			len(ids), quota.Remaining, quota.Display)
	}

	report := &BulkRevealReport{Requested: len(ids), Quota: *quota}
	for i, id := range ids {
		res, err := c.RevealLead(ctx, id)
		switch {
		case err == nil:
			report.Revealed = append(report.Revealed, *res)
			report.Quota = res.Quota
		case IsQuotaExhausted(err):
			// The allowance is gone. Nothing was charged for this attempt and
			// nothing would be charged for the rest, so stop and account for
			// them instead of hammering a meter that is already empty.
			for _, rest := range ids[i:] {
				report.Skipped = append(report.Skipped, BulkRevealSkip{
					PoolID: rest, Reason: BulkRevealNoQuota, Err: err,
				})
			}
			return report, nil
		case IsErased(err):
			report.Skipped = append(report.Skipped, BulkRevealSkip{
				PoolID: id, Reason: BulkRevealErased, Err: err,
			})
		case IsLeadNotFound(err):
			report.Skipped = append(report.Skipped, BulkRevealSkip{
				PoolID: id, Reason: BulkRevealNotFound, Err: err,
			})
		default:
			return report, fmt.Errorf("salesshift.RevealLeads: %s: %w", id, err)
		}
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// Save & pool detail
// ---------------------------------------------------------------------------

// SaveLeads copies pool rows into this tenant's saved leads. Max
// LeadsMaxBatch ids per call.
//
// A snapshot, not a reference — the pool is re-crawled continuously and a
// qualified list must not change underneath whoever qualified it. Saving costs
// no quota, and an address that has not been revealed is NOT copied: a lead
// saved while masked stores no email until a reveal back-fills it.
func (c *Client) SaveLeads(ctx context.Context, poolPersonIDs []string) (*SaveLeadsResult, error) {
	ids := dedupeIDs(poolPersonIDs)
	if len(ids) == 0 {
		return nil, errors.New("salesshift.SaveLeads: poolPersonIDs is required")
	}
	if len(ids) > LeadsMaxBatch {
		return nil, fmt.Errorf("salesshift.SaveLeads: %d ids exceeds the server limit of %d per call",
			len(ids), LeadsMaxBatch)
	}
	body := map[string][]string{"pool_person_ids": ids}
	var out SaveLeadsResult
	if err := c.T.JSON(ctx, "salesshift.SaveLeads", "POST",
		c.leadsURL("/leads/save"), body, &out); err != nil {
		return nil, fmt.Errorf("salesshift.SaveLeads: %w", err)
	}
	return &out, nil
}

// GetPoolPerson returns everything the pool knows about one person plus this
// org's relationship to them — revealed, saved, or already a Contact.
//
// Masking applies here exactly as in search. IsErased (410) means the person
// exercised their right to be forgotten.
func (c *Client) GetPoolPerson(ctx context.Context, poolID string) (*PoolPersonDetail, error) {
	if poolID == "" {
		return nil, errors.New("salesshift.GetPoolPerson: poolID is required")
	}
	var raw struct {
		Success bool             `json:"success"`
		Data    PoolPersonDetail `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetPoolPerson", "GET",
		c.leadsURL("/leads/pool/"+url.PathEscape(poolID)), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetPoolPerson: %w", err)
	}
	return &raw.Data, nil
}

// GetPoolCompany returns a company and the people behind it, pre-split into
// NewProspects and ExistingContacts so an account page can say "you already
// have 4 of these" instead of inviting someone to re-buy them.
func (c *Client) GetPoolCompany(ctx context.Context, companyID string) (*PoolCompany, error) {
	if companyID == "" {
		return nil, errors.New("salesshift.GetPoolCompany: companyID is required")
	}
	var raw struct {
		Success bool        `json:"success"`
		Data    PoolCompany `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetPoolCompany", "GET",
		c.leadsURL("/leads/company/"+url.PathEscape(companyID)), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetPoolCompany: %w", err)
	}
	return &raw.Data, nil
}

// ---------------------------------------------------------------------------
// Saved leads
// ---------------------------------------------------------------------------

// ListLeads returns this tenant's saved leads, newest first.
func (c *Client) ListLeads(ctx context.Context, in ListLeadsInput) ([]Lead, error) {
	u := c.leadsURL("/leads")
	q := url.Values{}
	if in.Status != "" {
		q.Set("status", in.Status)
	}
	if in.Limit > 0 {
		q.Set("limit", strconv.Itoa(in.Limit))
	}
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var raw struct {
		Success bool   `json:"success"`
		Data    []Lead `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListLeads", "GET", u, nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListLeads: %w", err)
	}
	return raw.Data, nil
}

// GetLead returns one saved lead together with the live pool row behind it and
// the Drift between them.
//
// The two are shown side by side deliberately: the lead is a snapshot, the
// pool moves on, and someone working a list should learn of a job change from
// Drift rather than from a bounce. A 404 here means "not your lead", which is
// a different thing from the 404 GetPoolPerson returns for "not in the pool".
func (c *Client) GetLead(ctx context.Context, leadID string) (*Lead, error) {
	if leadID == "" {
		return nil, errors.New("salesshift.GetLead: leadID is required")
	}
	var raw struct {
		Success bool `json:"success"`
		Data    Lead `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.GetLead", "GET",
		c.leadsURL("/leads/"+url.PathEscape(leadID)), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.GetLead: %w", err)
	}
	return &raw.Data, nil
}

// UpdateLead patches status, score, notes, tags, owner or disqualify reason on
// a saved lead. Nil fields are not sent and are therefore not touched.
func (c *Client) UpdateLead(ctx context.Context, leadID string, in UpdateLeadInput) (*Lead, error) {
	if leadID == "" {
		return nil, errors.New("salesshift.UpdateLead: leadID is required")
	}
	var raw struct {
		Success bool `json:"success"`
		Data    Lead `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.UpdateLead", "PATCH",
		c.leadsURL("/leads/"+url.PathEscape(leadID)), in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.UpdateLead: %w", err)
	}
	return &raw.Data, nil
}

// ---------------------------------------------------------------------------
// Convert — the one-way gate into mailable Contacts
// ---------------------------------------------------------------------------

// ConvertLead turns a saved lead into a Contact. This is the moment a record
// becomes mailable, and the only supported route to one — nothing else in this
// file produces something a send, sequence or campaign may touch.
//
// The lead row is kept as an audit trail, never moved. An address is required:
// converting without one creates a dead Contact that drags every campaign
// metric down, so the server answers 400. Lead.Convertible predicts that
// locally.
//
// An erased lead is refused with 400, not 410 — the erasure was recorded
// against the tenant's copy, so check Lead.ErasurePending rather than IsErased
// here.
func (c *Client) ConvertLead(ctx context.Context, leadID string, in ConvertLeadInput) (*ConvertLeadResult, error) {
	if leadID == "" {
		return nil, errors.New("salesshift.ConvertLead: leadID is required")
	}
	var out ConvertLeadResult
	if err := c.T.JSON(ctx, "salesshift.ConvertLead", "POST",
		c.leadsURL("/leads/"+url.PathEscape(leadID)+"/convert"), in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.ConvertLead: %w", err)
	}
	return &out, nil
}

// BulkConvertLeads converts many saved leads. Costs no quota — these are rows
// this org already holds — but it still reports every id it could not convert.
// Print BulkConvertResult whole; the skipped buckets are the half that matters.
func (c *Client) BulkConvertLeads(ctx context.Context, leadIDs []string) (*BulkConvertResult, error) {
	ids := dedupeIDs(leadIDs)
	if len(ids) == 0 {
		return nil, errors.New("salesshift.BulkConvertLeads: leadIDs is required")
	}
	body := map[string][]string{"lead_ids": ids}
	var out BulkConvertResult
	if err := c.T.JSON(ctx, "salesshift.BulkConvertLeads", "POST",
		c.leadsURL("/leads/bulk-convert"), body, &out); err != nil {
		return nil, fmt.Errorf("salesshift.BulkConvertLeads: %w", err)
	}
	out.Requested = len(ids)
	return &out, nil
}

// ConvertFromPool goes pool → Contact in one step: save, reveal if permitted,
// convert. Max LeadsMaxBatch ids per call.
//
// This SPENDS QUOTA when RevealIfNeeded is true — one reveal per person not
// already owned. With it false nothing is charged and unrevealed records come
// back as SkippedNoQuota, which makes a dry run cheap.
//
// The returned report accounts for every id. Render all of its buckets with
// String or Describe: when the allowance runs out mid-batch the server
// converts what it can and says exactly how many it could not, and reporting
// only Converted turns that partial spend into a silent one.
func (c *Client) ConvertFromPool(ctx context.Context, in ConvertFromPoolInput) (*ConvertFromPoolReport, error) {
	ids := dedupeIDs(in.PoolPersonIDs)
	if len(ids) == 0 {
		return nil, errors.New("salesshift.ConvertFromPool: PoolPersonIDs is required")
	}
	if len(ids) > LeadsMaxBatch {
		return nil, fmt.Errorf(
			"salesshift.ConvertFromPool: %d ids exceeds the server limit of %d per call — "+
				"reveals are metered, so a bigger batch would spend more quota than one click intends",
			len(ids), LeadsMaxBatch)
	}
	in.PoolPersonIDs = ids

	var out ConvertFromPoolReport
	if err := c.T.JSON(ctx, "salesshift.ConvertFromPool", "POST",
		c.leadsURL("/leads/convert-from-pool"), in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.ConvertFromPool: %w", err)
	}
	out.Requested = len(ids)
	return &out, nil
}

// ---------------------------------------------------------------------------
// Erasure — right to be forgotten
// ---------------------------------------------------------------------------

// RequestErasure erases a person from the pool and strips every tenant's saved
// copy of them.
//
// GLOBAL AND IRREVERSIBLE. It is not scoped to the caller's organisation: the
// request is about the person, not about whoever happens to hold a copy, so
// scoping it would leave the data live everywhere else. The address is stored
// only as a hash — retaining the plaintext to enforce its deletion would be
// self-defeating — and the pool row is deactivated rather than deleted so the
// next crawl cannot resurrect them.
//
// Because none of that can be undone, in.Confirm must be set true. That field
// is never sent to the server; it is the explicit acknowledgement this SDK
// requires before issuing the call.
func (c *Client) RequestErasure(ctx context.Context, in ErasureInput) (*ErasureResult, error) {
	if in.Email == "" && in.LinkedInURL == "" {
		return nil, errors.New("salesshift.RequestErasure: Email or LinkedInURL is required")
	}
	if !in.Confirm {
		return nil, errors.New(
			"salesshift.RequestErasure: erasure is GLOBAL (it removes this person for every " +
				"tenant, not just yours) and irreversible — set Confirm to true to proceed")
	}
	var out ErasureResult
	if err := c.T.JSON(ctx, "salesshift.RequestErasure", "POST",
		c.leadsURL("/leads/erasure"), in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.RequestErasure: %w", err)
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Enrichment
// ---------------------------------------------------------------------------

// EnrichInput selects what to crawl. Exactly one of the two is required.
type EnrichInput struct {
	// CompanyID enriches a company already in the pool.
	CompanyID string `json:"company_id,omitempty"`
	// Domain enriches by hostname. If no company holds that domain, the crawl
	// CREATES it, along with any people it finds — this is how a domain the
	// pool has never seen ends up on file.
	Domain string `json:"domain,omitempty"`
}

// EnrichResult reports what a crawl read and what it wrote.
//
// Read Crawled first. When it is zero, Note says WHY — blocked by a CDN,
// nothing readable, a server error — and every other field is zero because
// nothing was written.
type EnrichResult struct {
	CompanyID      string `json:"company_id"`
	CompanyCreated bool   `json:"company_created"`
	// Crawled is pages successfully read; Attempted is pages tried, and is
	// only sent when Crawled is zero.
	Crawled   int `json:"crawled"`
	Attempted int `json:"attempted"`
	// Changed lists the company fields the crawl FILLED. A crawl never
	// overwrites a value that was already there, so this is always gaps.
	Changed             []string `json:"changed"`
	PeopleFound         int      `json:"people_found"`
	PeopleAdded         int      `json:"people_added"`
	PeopleAlreadyKnown  int      `json:"people_already_known"`
	PeopleSkippedErased int      `json:"people_skipped_erased"`
	StatusCodes         []int    `json:"status_codes"`
	ElapsedMS           int      `json:"elapsed_ms"`
	Note                string   `json:"note"`
}

// Enrich crawls a company's own website and folds what it finds into the pool.
//
// This is the only call in this package that WRITES to the shared pool, so it
// is worth knowing what it will and will not do:
//
//   - Gaps only. An existing description, keyword set or address is never
//     replaced by crawl output.
//   - Erasure is checked before every insert, so a crawl cannot resurrect
//     someone who asked to be forgotten.
//   - Shared mailboxes (sales@, info@, announce@) are not people and are not
//     ingested. A name is derived from an address only when the local part
//     plausibly is one.
//   - Everything found is recorded as UNVERIFIED, and no reveal quota is spent.
//
// It is slow by nature — it fetches up to a dozen pages — so pass a context
// with a generous deadline; 90s is the server's own ceiling.
func (c *Client) Enrich(ctx context.Context, in EnrichInput) (*EnrichResult, error) {
	if strings.TrimSpace(in.CompanyID) == "" && strings.TrimSpace(in.Domain) == "" {
		return nil, errors.New("salesshift.Enrich: CompanyID or Domain is required")
	}
	var out EnrichResult
	if err := c.T.JSON(ctx, "salesshift.Enrich", "POST",
		c.leadsURL("/leads/enrich"), in, &out); err != nil {
		return nil, fmt.Errorf("salesshift.Enrich: %w", err)
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// Saved searches
// ---------------------------------------------------------------------------

// ListSavedSearches returns this org's stored filter sets.
func (c *Client) ListSavedSearches(ctx context.Context) ([]SavedSearch, error) {
	var raw struct {
		Success bool          `json:"success"`
		Data    []SavedSearch `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.ListSavedSearches", "GET",
		transport.JoinURL(c.InfinityURL, leadsPrefix+"/lead-searches"), nil, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.ListSavedSearches: %w", err)
	}
	return raw.Data, nil
}

// SaveSearch stores a filter set under a name.
//
// A saved search stores FILTERS, never a cursor: a keyset position is only
// meaningful within the ordering it came from, so replaying one later would
// page from the wrong place.
//
// The server echoes only the new id and name, so the returned Filters and
// IsShared are the values that were sent.
func (c *Client) SaveSearch(ctx context.Context, in SaveSearchInput) (*SavedSearch, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("salesshift.SaveSearch: Name is required")
	}
	var raw struct {
		Success bool `json:"success"`
		Data    struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.T.JSON(ctx, "salesshift.SaveSearch", "POST",
		transport.JoinURL(c.InfinityURL, leadsPrefix+"/lead-searches"), in, &raw); err != nil {
		return nil, fmt.Errorf("salesshift.SaveSearch: %w", err)
	}
	return &SavedSearch{
		ID:       raw.Data.ID,
		Name:     raw.Data.Name,
		Filters:  in.Filters,
		IsShared: in.IsShared,
	}, nil
}

// ---------------------------------------------------------------------------
// Naming the statuses that mean something specific here
// ---------------------------------------------------------------------------

// IsQuotaExhausted reports whether err is the 402 the reveal meter returns
// when this period's allowance is spent.
//
// Worth naming rather than treating as a generic failure, because the correct
// message is specific: the reveal did not happen AND NOTHING WAS CHARGED for
// the attempt. Retrying will not help until the period rolls over or the
// tenant's allowance is raised.
func IsQuotaExhausted(err error) bool {
	return leadsStatus(err) == http.StatusPaymentRequired
}

// IsErased reports whether err is the 410 returned for a person who exercised
// their right to be forgotten.
//
// Terminal. Not an outage, not a transient failure, not retryable — and it
// must not be presented as one, because the record is gone by design.
func IsErased(err error) bool {
	return leadsStatus(err) == http.StatusGone
}

// IsLeadNotFound reports a 404. Note the server uses it for two distinct
// things — "not in the pool" and "not your lead" — which the SDK cannot tell
// apart from the status alone. Distinguish by which call produced it:
// GetPoolPerson / RevealLead mean the former, GetLead / UpdateLead the latter.
func IsLeadNotFound(err error) bool {
	return leadsStatus(err) == http.StatusNotFound
}

// IsMalformedCursor reports the 400 raised by an unreadable cursor — almost
// always a cursor reused across a change of sort or filters, or one a client
// built by hand. Both are the same mistake: the cursor is opaque and only
// valid inside the ordering that produced it. Start again with an empty cursor.
func IsMalformedCursor(err error) bool {
	f := failureOf(err)
	if f == nil || f.HTTPStatus != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(f.Detail), "cursor")
}

// failureOf digs the transport's typed failure out of a wrapped error so the
// helpers above can read the status and body the server actually sent. Every
// concrete error in the vxerrors hierarchy embeds *Failure, but the wrappers
// unwrap to their Cause rather than to the embedded value, so each is checked.
func failureOf(err error) *vxerrors.Failure {
	if err == nil {
		return nil
	}
	var f *vxerrors.Failure
	if errors.As(err, &f) {
		return f
	}
	var ae *vxerrors.AuthError
	if errors.As(err, &ae) {
		return ae.Failure
	}
	var ve *vxerrors.ValidationError
	if errors.As(err, &ve) {
		return ve.Failure
	}
	var nf *vxerrors.NotFoundError
	if errors.As(err, &nf) {
		return nf.Failure
	}
	var rl *vxerrors.RateLimitError
	if errors.As(err, &rl) {
		return rl.Failure
	}
	var se *vxerrors.ServerError
	if errors.As(err, &se) {
		return se.Failure
	}
	var ne *vxerrors.NetworkError
	if errors.As(err, &ne) {
		return ne.Failure
	}
	return nil
}

func leadsStatus(err error) int {
	if f := failureOf(err); f != nil {
		return f.HTTPStatus
	}
	return 0
}

// dedupeIDs drops blanks and repeats while preserving order.
//
// A repeated id in a metered batch is not harmless: convert-from-pool walks
// the list as given, so the same person would land in `converted` once and
// `already_converted` again, and a reveal batch would look twice as expensive
// as it is. Both make the report lie about how many people were touched.
func dedupeIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
