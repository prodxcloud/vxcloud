// vxsdk.hpp — C++ SDK for the vxcloud platform (preview).
//
// Header for a small, libcurl-backed client that speaks the same wire
// contract as the Go / Python / TypeScript SDKs:
//   - auth exchange   POST {vxcloud}/api/v1/auth/developer/keys/login
//   - node discovery  GET  {vxcloud}/api/v1/auth/nodes/
//   - node ops        {node}/api/v2/...   (cicd, sessions, agentcontrol, health)
//
// Auth model (mirrors python/vxsdk.py Client._auth_headers):
//   * Authorization: Bearer <access_token>  when an access token is held.
//   * X-API-Key: <api_key>  UNLESS a Bearer token is present AND the request
//     targets the tenant node — dropping it there avoids the node middleware's
//     403 "not valid for this workspace".
//   * AgentControl requests additionally need X-Tenant-ID.
//   * A single lazy refresh runs on the first 401 (api-key clients only).
//
// Only two things are parsed internally (tokens on login, address on node
// discovery); every data method returns the raw JSON response as a string
// so the caller can plug in whatever JSON library it prefers. Zero JSON
// dependency; the only link dependency is libcurl.
//
// Build: see CMakeLists.txt.  Requires C++17 and libcurl.

#ifndef VXSDK_HPP
#define VXSDK_HPP

#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <vector>

namespace vx {

constexpr const char* kDefaultVxCloudUrl = "https://api.vxcloud.io";
constexpr long        kDefaultTimeout     = 30;   // seconds
constexpr long        kLongTimeout        = 600;  // installs / deploys

// ── Errors ───────────────────────────────────────────────────────────────

// VxError carries the failing op, an HTTP status (0 for transport errors),
// and up to ~800 bytes of server detail. Branch on http_status() (401/403 =
// auth, 400/422 = validation, 404 = not found, 429 = rate limit, 5xx =
// server, 0 = transport).
class VxError : public std::runtime_error {
public:
    VxError(std::string op, const std::string& message, int http_status = 0,
            std::string detail = "");
    int http_status() const noexcept { return http_status_; }
    const std::string& op() const noexcept { return op_; }
    const std::string& detail() const noexcept { return detail_; }
    bool is_auth() const noexcept { return http_status_ == 401 || http_status_ == 403; }
    bool is_retryable() const noexcept {
        return http_status_ == 0 || http_status_ == 429 || http_status_ >= 500;
    }
    // Two statuses that must be reported by name rather than as "an error".
    // Both are raised today only by the leads endpoints (see the leads* methods
    // on Client), and both are already excluded from is_retryable().
    //
    // 402: the reveal allowance for this period is spent — and NOTHING was
    // charged for the attempt that failed, which is the half users assume the
    // other way round.
    bool is_quota_exhausted() const noexcept { return http_status_ == 402; }
    // 410: the person asked to be forgotten. Terminal — not an outage, not a
    // transient failure, and retrying can never succeed.
    bool is_erased() const noexcept { return http_status_ == 410; }
private:
    int         http_status_;
    std::string op_;
    std::string detail_;
};

// ── HTTP response ────────────────────────────────────────────────────────

struct Response {
    long                               status = 0;
    std::string                        body;
    std::map<std::string, std::string> headers;
    bool ok() const noexcept { return status >= 200 && status < 300; }
};

// ── SalesShift leads — request shapes ────────────────────────────────────
//
// The pool search is filter-heavy, so the calls that need more than an id take
// a struct rather than fifteen positional arguments. Every field is optional:
// an empty vector or an unset optional means "no constraint" — never "match
// nothing".

struct LeadFilters {
    std::string              q;                // free text over name / title / company
    std::vector<std::string> titles;           // substring, OR'd
    std::vector<std::string> exclude_titles;   // substring, AND'd as NOT
    std::vector<std::string> seniorities;      // founder|c_suite|vp|director|manager|senior|entry
    std::vector<std::string> departments;      // engineering|sales|marketing|finance|hr|ops|legal|executive
    std::vector<std::string> countries;        // ISO-2, UPPER
    std::vector<std::string> industries;
    std::vector<std::string> email_statuses;   // verified|unverified|guessed|catch_all|invalid
    std::vector<std::string> employee_ranges;  // "1-10" … "5000+"
    std::vector<std::string> company_domains;
    std::vector<std::string> keywords;         // SUBSTRING against company keywords
    std::optional<int>       min_score;
    // "an address EXISTS", not "you may see it" — a has_email row is still
    // masked until it is revealed.
    std::optional<bool>      has_email;
    std::optional<bool>      has_phone;
};

struct LeadSearchRequest {
    LeadFilters filters;
    std::string result_type = "person";   // "person" | "company"
    // OPAQUE. Feed back the `next_cursor` you were handed, byte for byte, and
    // never across a sort or filter change: a keyset position is only
    // meaningful within the ordering that produced it, so reusing one after
    // re-sorting compares the wrong column and silently drops or repeats rows.
    // Empty starts at page one; walk until next_cursor is null/empty. Do not
    // parse one, do not build one.
    std::string cursor;
    int         limit = 25;               // server caps at 100
    // "" leaves the sort to the server (score desc). People: score|name|title|
    // company|location|employees|email. Companies: score|name|employees|
    // industry|location. An unknown field degrades to score desc SERVER-SIDE,
    // so render `data.sort` from the response — the sort APPLIED — not this.
    std::string sort_field;
    bool        sort_desc = true;
};

// Fields a saved lead accepts on update. Unset optionals are left untouched;
// an engaged one is sent, so `tags = std::vector<std::string>{}` clears tags.
struct LeadUpdate {
    std::optional<std::string>              status;
    std::optional<int>                      score;
    std::optional<std::string>              notes;
    std::optional<std::string>              disqualify_reason;
    std::optional<std::string>              owner_id;
    std::optional<std::vector<std::string>> tags;
};

// ── Client ───────────────────────────────────────────────────────────────

struct ClientOptions {
    std::string api_key;       // xc_dev|test|live_...  (enables auth exchange + refresh)
    std::string username;
    std::string access_token;  // pre-obtained Bearer JWT (alternative to api_key)
    std::string refresh_token;
    std::string vxcloud_url = kDefaultVxCloudUrl;
    std::string node_url;      // e.g. https://node1.vxcloud.io (auto-resolved if empty)
    std::string tenant_id;     // required for agentcontrol.* (X-Tenant-ID)
    std::string user_agent;    // defaults to vxsdk-cpp/<version>
};

class Client {
public:
    explicit Client(ClientOptions opts);
    ~Client();

    Client(const Client&) = delete;
    Client& operator=(const Client&) = delete;

    // Eagerly exchange the API key for a JWT pair (optional; lazy on 401).
    void authenticate();

    // Resolve + cache the tenant node base URL from /api/v1/auth/nodes/.
    // Idempotent; returns the cached value once known.
    const std::string& ensure_node_url();

    const std::string& node_url() const noexcept { return node_url_; }
    const std::string& username() const noexcept { return username_; }
    const std::string& access_token() const noexcept { return access_token_; }

    // ── generic verbs ──
    // `path` may be absolute (starts with http) or a node-relative path
    // (leading "/"), in which case it is joined onto the resolved node URL.
    // `extra_headers` is merged last (used for X-Tenant-ID).
    std::string get(const std::string& path,
                    const std::map<std::string, std::string>& extra_headers = {},
                    long timeout = kDefaultTimeout);
    std::string post(const std::string& path, const std::string& json_body,
                     const std::map<std::string, std::string>& extra_headers = {},
                     long timeout = kDefaultTimeout);
    std::string del(const std::string& path,
                    const std::map<std::string, std::string>& extra_headers = {},
                    long timeout = kDefaultTimeout);

    // ── convenience: platform surfaces ──
    std::string health();                 // GET {node}/api/v2/health   (no auth)
    std::string cicd_pipelines();         // GET {node}/api/v2/cicd/pipelines
    std::string sessions();               // GET {node}/api/v2/tenant/sessions?username=

    // AgentControl (adds X-Tenant-ID from ClientOptions.tenant_id).
    std::string agentcontrol_summary();
    std::string agentcontrol_agents();
    std::string agentcontrol_models();
    std::string agentcontrol_deployments();
    std::string agentcontrol_llm_providers();
    std::string agentcontrol_datasets();  // GET {node}/api/v2/agentcontrol/datasets/
    // Chat with a deployed agentcontrol agent.
    std::string agentcontrol_chat(const std::string& agent_id, const std::string& message);

    // AgentControl — training jobs (list / get / create). Long-running: poll
    // agentcontrol_training_get(id) until the "status" field is terminal
    // (completed/succeeded/failed). base_model may be a gated/HF slug — the
    // backend falls back to a tiny CPU model so the job still completes.
    std::string agentcontrol_training();
    std::string agentcontrol_training_get(const std::string& job_id);
    std::string agentcontrol_training_create(const std::string& name,
                                             const std::string& base_model,
                                             const std::string& dataset_id,
                                             const std::string& type = "pre-training",
                                             int total_epochs = 1);

    // AgentControl — fine-tuning jobs (list / create).
    std::string agentcontrol_fine_tuning();
    std::string agentcontrol_fine_tuning_create(const std::string& name,
                                                const std::string& base_model,
                                                const std::string& training_file,
                                                int epochs = 1, int batch_size = 4,
                                                double learning_rate = 5e-5);

    // AgentControl — vLLM serving artifact: returns a deployable
    // docker-compose + command args + test curl for an OpenAI-compatible
    // vLLM server. POST {node}/api/v2/agentcontrol/serving/vllm-artifact.
    std::string agentcontrol_vllm_artifact(const std::string& model,
                                           int port = 0,
                                           const std::string& quantization = "",
                                           int max_model_len = 0);

    // SalesShift — sales email service. Tracked sends through the org's BYOK
    // providers (tenant-node email worker preferred) with suppression gating,
    // daily caps and open/click tracking. Merge tags like {{first_name}} are
    // resolved against the contact record.
    //   POST {vxcloud}/api/v1/salesshift/email/send
    //   GET  {vxcloud}/api/v1/salesshift/emails[?status=]
    //   GET  {vxcloud}/api/v1/salesshift/stats
    //   GET  {node}/api/v2/salesshift/email/health
    std::string salesshift_send_email(const std::string& to_email,
                                      const std::string& subject,
                                      const std::string& body_html);
    std::string salesshift_emails(const std::string& status = "");
    std::string salesshift_stats();
    std::string salesshift_worker_health();

    // ── SalesShift — the leads pool ────────────────────────────────────────
    //
    // A global, tenant-blind database of people and companies, plus this
    // tenant's saved copies of rows out of it. Every endpoint lives on the
    // VXCLOUD control plane (`{vxcloud}/api/v1/salesshift/leads…`), not on
    // the tenant node — the implementations build absolute URLs for that
    // reason, exactly like the salesshift email methods above.
    //
    // FIVE RULES. Each one is here because breaking it costs something
    // specific:
    //
    //  1. LEADS ARE NOT MAILABLE. Never hand a lead or a pool row to a send,
    //     sequence or campaign call — salesshift_send_email takes CONTACTS.
    //     leadsConvert / leadsConvertFromPool is the only route to a mailable
    //     record, and it is where consent metadata gets written. A scraped
    //     record entering a send path is how a tenant's sending domain dies.
    //  2. AN UNREVEALED ADDRESS IS A MASK ("j•••@acme.com"), not an address.
    //     Never present it as one, never let it be copied as one, never feed
    //     it to anything that sends. `has_email` says an address EXISTS;
    //     `email_revealed` says whether you may see it.
    //  3. REVEAL SPENDS METERED QUOTA. leadsReveal charges one; so does
    //     leadsConvertFromPool per row it has to un-mask. Show the cost BEFORE
    //     acting (leadsQuota) and report every skipped record afterwards —
    //     rendering only the success count of a partial spend destroys trust
    //     in the meter.
    //  4. leadsConvertFromPool ACCOUNTS FOR EVERY ID: converted /
    //     already_converted / skipped_no_quota / skipped_no_email /
    //     skipped_erased. Render all of them; printing only `converted` hides
    //     a partial result.
    //  5. leadsErasure IS GLOBAL AND IRREVERSIBLE. It removes the person for
    //     EVERY tenant, not only the caller's, and cannot be undone. Anything
    //     that exposes it must say so and require explicit confirmation.
    //
    // Every method returns the raw JSON body (house style — no JSON
    // dependency) and throws VxError on a non-2xx. Statuses worth handling by
    // name via VxError::http_status():
    //   402  reveal allowance spent — NOTHING was charged for that attempt
    //   404  not in the pool / not your lead — distinguish the two
    //   410  erased at the person's request — terminal, NOT an outage, NOT
    //        retryable (VxError::is_retryable() is already false for it)
    //   400  malformed cursor, or a convert with no address on the record

    // Search the pool. Items come back MASKED (rule 2). Render
    // `data.total_display` — it reads "10,000+" once `data.total_is_estimate`
    // is true, because the server stops counting at 10,000 — never the raw
    // `data.total`. `data.sort` is the sort that was APPLIED and `data
    // .search_backend` names the engine that answered ("go-node", or
    // "fastapi-orm" while the node is down).
    //   POST {vxcloud}/api/v1/salesshift/leads/search
    std::string leadsSearch(const LeadSearchRequest& request);

    // Counts per seniority / department / country / email_status for a filter
    // set. Pool-wide: no tenant overlay, because the number of Directors in
    // the pool is the same for everyone.
    //   POST {vxcloud}/api/v1/salesshift/leads/facets
    std::string leadsFacets(const LeadFilters& filters = LeadFilters{});

    // Reveals used / allowance / remaining this period, plus a "3 / 200"
    // display string. Read it before any bulk reveal so the cost is visible
    // first (rule 3).
    //   GET {vxcloud}/api/v1/salesshift/leads/quota
    std::string leadsQuota();

    // Un-mask one person: real email, phone, linkedin_url, and the quota AFTER
    // the spend. SPENDS ONE REVEAL — free if this org already revealed the
    // row. Throws VxError 402 when the allowance is gone (nothing was charged
    // for the attempt) and 410 if the person has been erased.
    //   POST {vxcloud}/api/v1/salesshift/leads/reveal
    std::string leadsReveal(const std::string& pool_person_id);

    // Copy pool rows into the tenant's saved leads. A SNAPSHOT taken now: the
    // pool is re-crawled continuously and a qualified list must not move
    // underneath whoever qualified it. Saving reveals NOTHING and does not
    // make a row mailable. Max 200 ids.
    //   POST {vxcloud}/api/v1/salesshift/leads/save
    std::string leadsSave(const std::vector<std::string>& pool_person_ids);

    // Full detail for one pool person, and for one company plus the people
    // behind it split into `new_prospects` / `existing_contacts`. Masking
    // applies here exactly as in search — a detail view is not a back door
    // around the meter.
    //   GET {vxcloud}/api/v1/salesshift/leads/pool/{pool_id}
    //   GET {vxcloud}/api/v1/salesshift/leads/company/{company_id}
    std::string leadsPoolPerson(const std::string& pool_id);
    std::string leadsCompany(const std::string& company_id);

    // The tenant's saved leads. `status` filters (new|contacted|converted|…),
    // `limit` <= 500 (0 = server default of 100).
    //   GET {vxcloud}/api/v1/salesshift/leads
    std::string leadsList(const std::string& status = "", int limit = 0);

    // One saved lead, the live pool row behind it, and `drift` — non-empty
    // when the pool has moved on since the snapshot was taken.
    //   GET {vxcloud}/api/v1/salesshift/leads/{lead_id}
    std::string leadsGet(const std::string& lead_id);

    // Update status / score / notes / tags / owner on a saved lead. Only the
    // engaged fields of `patch` are sent.
    //   PATCH {vxcloud}/api/v1/salesshift/leads/{lead_id}
    std::string leadsUpdate(const std::string& lead_id, const LeadUpdate& patch);

    // Saved lead → Contact: the one-way gate that makes a record mailable
    // (rule 1). The lead row is kept as an audit trail, never moved. Throws
    // VxError 400 if the lead has no revealed address, or if it has been
    // erased. Answers `already_converted` rather than duplicating a contact.
    //   POST {vxcloud}/api/v1/salesshift/leads/{lead_id}/convert
    std::string leadsConvert(const std::string& lead_id,
                             const std::string& lifecycle_stage = "");

    // Many saved leads → Contacts. Reports converted / skipped_no_email /
    // already_converted — render all three, not just the first.
    //   POST {vxcloud}/api/v1/salesshift/leads/bulk-convert
    std::string leadsBulkConvert(const std::vector<std::string>& lead_ids);

    // Pool → Contact in one step: save, reveal if needed, convert. SPENDS
    // QUOTA — one reveal per row not already un-masked. Pass
    // reveal_if_needed=false to convert only rows this org has already
    // revealed: that path spends nothing and reports the remainder as
    // skipped_no_quota. Max 200 ids, because reveals are metered and a bigger
    // batch would spend more than anyone intends in one click.
    //
    // The response accounts for EVERY id passed in — converted, revealed_now,
    // already_converted, skipped_no_quota, skipped_no_email, skipped_erased,
    // plus the quota after the spend. Surface all of them (rule 4): when the
    // allowance runs out mid-batch the server converts what it can, and a
    // partial spend reported as a success is exactly the failure this split
    // exists to prevent.
    //   POST {vxcloud}/api/v1/salesshift/leads/convert-from-pool
    std::string leadsConvertFromPool(const std::vector<std::string>& pool_person_ids,
                                     bool reveal_if_needed = true,
                                     const std::string& lifecycle_stage = "");

    // Right to be forgotten. GLOBAL AND IRREVERSIBLE (rule 5): the person is
    // deactivated in the pool and stripped from EVERY tenant's saved leads —
    // not just the caller's — and the address is retained only as a hash so
    // future crawls keep honouring the block. There is no undo. A UI wrapping
    // this must say all of that and demand explicit confirmation.
    //   POST {vxcloud}/api/v1/salesshift/leads/erasure
    std::string leadsErasure(const std::string& email,
                             const std::string& reason = "gdpr_erasure",
                             const std::string& note = "");

    // Crawl a company's own website and fold what it finds into the pool.
    //
    // The ONLY call here that writes to the shared pool, so note what it
    // refuses to do: it fills gaps and never overwrites; it checks the erasure
    // list before every insert; it drops shared mailboxes (sales@, info@,
    // announce@) because those are not people; and everything it stores is
    // UNVERIFIED. It spends no reveal quota.
    //
    // Pass a company_id for a company already in the pool, or a bare domain
    // for one that is not — in which case the crawl CREATES it, and any people
    // it finds, too.
    //
    // Slow by nature (up to a dozen page fetches; the server's ceiling is 90s),
    // so callers should raise their own timeout accordingly.
    //   POST {vxcloud}/api/v1/salesshift/leads/enrich
    std::string leadsEnrich(const std::string& domain,
                            const std::string& company_id = "");

    // Saved searches: list them, or store one filter set under a name.
    //   GET  {vxcloud}/api/v1/salesshift/lead-searches
    //   POST {vxcloud}/api/v1/salesshift/lead-searches
    std::string leadsSavedSearches();
    std::string leadsSaveSearch(const std::string& name, const LeadFilters& filters,
                                bool is_shared = false);

private:
    // Low-level: performs the request with auth headers + one 401 refresh.
    Response do_request(const std::string& method, const std::string& url,
                        const std::string& body,
                        std::map<std::string, std::string> headers, long timeout);
    Response raw_request(const std::string& method, const std::string& url,
                         const std::string& body,
                         const std::map<std::string, std::string>& headers, long timeout);
    void refresh();
    std::map<std::string, std::string> auth_headers(const std::string& url) const;
    std::string resolve(const std::string& path);          // path -> absolute URL
    std::map<std::string, std::string> tenant_header() const;  // X-Tenant-ID or throw

    std::string api_key_, username_, access_token_, refresh_token_;
    std::string vxcloud_url_, node_url_, tenant_id_, user_agent_;
};

// ── Tiny JSON helpers (extraction only, not a full parser) ────────────────
// Enough to read a scalar field and to walk the node-list during discovery.
// Callers doing real work should parse `Response.body` with a JSON library.
namespace json {
// Return the string value of top-level (or first-seen) key "name", or "".
std::string field(const std::string& doc, const std::string& name);
}  // namespace json

}  // namespace vx

#endif  // VXSDK_HPP
