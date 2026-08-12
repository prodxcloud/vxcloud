package io.vxcloud.sdk;

import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * VxClient — Java SDK for the vxcloud platform (preview).
 *
 * <p>Speaks the same wire contract as the Go / Python / TypeScript / C++ SDKs:
 * <ul>
 *   <li>auth exchange &mdash; {@code POST {infinity}/api/v1/auth/developer/keys/login}</li>
 *   <li>node discovery &mdash; {@code GET  {infinity}/api/v1/auth/nodes/}</li>
 *   <li>node ops &mdash; {@code {node}/api/v2/...} (cicd, sessions, agentcontrol, health)</li>
 * </ul>
 *
 * <p>Auth header rules mirror {@code python/vxsdk.py Client._auth_headers}:
 * a Bearer access token is always sent when held; {@code X-API-Key} is sent
 * except when a Bearer token is present AND the request targets the tenant
 * node (dropping it there avoids the node middleware's 403). AgentControl
 * requests additionally carry {@code X-Tenant-ID}. A single lazy refresh runs
 * on the first 401 for api-key clients.
 *
 * <p>Only tokens (on login) and the node address (on discovery) are parsed
 * internally by a tiny scalar-field extractor; every data method returns the
 * raw JSON body as a {@link String} so callers can use any JSON library.
 * Zero third-party dependencies &mdash; JDK 11+ only.
 *
 * <pre>{@code
 *   VxClient c = VxClient.builder()
 *       .accessToken(System.getenv("VX_ACCESS_TOKEN"))
 *       .username("alice")
 *       .nodeUrl("https://node1.vxcloud.io")
 *       .tenantId("<uuid>")
 *       .build();
 *   System.out.println(c.health());
 *   System.out.println(c.agentcontrolSummary());
 * }</pre>
 */
public final class VxClient {

    public static final String DEFAULT_INFINITY_URL = "https://api.vxcloud.io";
    public static final Duration DEFAULT_TIMEOUT = Duration.ofSeconds(30);
    public static final Duration LONG_TIMEOUT = Duration.ofSeconds(600);
    private static final String VERSION = "2026.8.13";
    /** Page cap for {@code /leads/search}; the server clamps to this too. */
    private static final int LEADS_PAGE_MAX = 100;
    /** Batch cap for {@code /leads/save} and {@code /leads/convert-from-pool}. */
    private static final int LEADS_BATCH_MAX = 200;
    /**
     * U+2022, the character the server masks an unrevealed address with. Written
     * as an escape so the check survives a build under a non-UTF-8 encoding.
     */
    private static final char MASK_BULLET = '\u2022';

    private final HttpClient http;
    private String apiKey;
    private String username;
    private String accessToken;
    private String refreshToken;
    private final String infinityUrl;
    private String nodeUrl;
    private final String tenantId;
    private final String userAgent;

    private VxClient(Builder b) {
        if ((b.apiKey == null || b.apiKey.isEmpty())
                && (b.accessToken == null || b.accessToken.isEmpty())) {
            throw new VxException("vxsdk.Client", "no credentials: set apiKey or accessToken", 401, "");
        }
        if (b.apiKey != null && !b.apiKey.isEmpty()) validateApiKey(b.apiKey);
        this.apiKey = orEmpty(b.apiKey);
        this.username = orEmpty(b.username);
        this.accessToken = orEmpty(b.accessToken);
        this.refreshToken = orEmpty(b.refreshToken);
        this.infinityUrl = rstripSlash(isEmpty(b.infinityUrl) ? DEFAULT_INFINITY_URL : b.infinityUrl);
        this.nodeUrl = rstripSlash(orEmpty(b.nodeUrl));
        this.tenantId = orEmpty(b.tenantId);
        this.userAgent = isEmpty(b.userAgent) ? "vxsdk-java/" + VERSION : b.userAgent;
        this.http = HttpClient.newBuilder()
                .connectTimeout(Duration.ofSeconds(20))
                // Pin HTTP/1.1: the default HTTP/2 client does a cleartext h2c
                // upgrade that some ASGI servers (uvicorn) mishandle, dropping
                // the POST body (server sees an empty body -> 422). All node
                // endpoints speak HTTP/1.1, so force it for correctness.
                .version(HttpClient.Version.HTTP_1_1)
                .followRedirects(HttpClient.Redirect.NORMAL)
                .build();
    }

    // ── builder ──────────────────────────────────────────────────────────

    public static Builder builder() { return new Builder(); }

    public static final class Builder {
        private String apiKey, username, accessToken, refreshToken;
        private String infinityUrl = DEFAULT_INFINITY_URL;
        private String nodeUrl, tenantId, userAgent;

        public Builder apiKey(String v)       { this.apiKey = v; return this; }
        public Builder username(String v)     { this.username = v; return this; }
        public Builder accessToken(String v)  { this.accessToken = v; return this; }
        public Builder refreshToken(String v) { this.refreshToken = v; return this; }
        public Builder infinityUrl(String v)  { this.infinityUrl = v; return this; }
        public Builder nodeUrl(String v)      { this.nodeUrl = v; return this; }
        public Builder tenantId(String v)     { this.tenantId = v; return this; }
        public Builder userAgent(String v)    { this.userAgent = v; return this; }
        public VxClient build()               { return new VxClient(this); }
    }

    // ── public accessors ─────────────────────────────────────────────────

    public String nodeUrl()      { return nodeUrl; }
    public String username()     { return username; }
    public String accessToken()  { return accessToken; }

    /** Eagerly exchange the API key for a JWT pair (optional; lazy on 401). */
    public void authenticate() { refresh(); }

    /** Resolve + cache the tenant node base URL from /api/v1/auth/nodes/. */
    public synchronized String ensureNodeUrl() {
        if (!nodeUrl.isEmpty()) return nodeUrl;
        HttpResponse<String> r = doRequest("GET", infinityUrl + "/api/v1/auth/nodes/",
                null, new LinkedHashMap<>(), DEFAULT_TIMEOUT);
        String doc = r.body();
        int def = doc.indexOf("\"is_default_node\":true");
        if (def < 0) def = doc.indexOf("\"is_default_node\": true");
        String chunk = def >= 0 ? doc.substring(Math.max(0, doc.lastIndexOf('{', def))) : doc;
        String addr = "";
        for (String key : new String[]{"custom_domain_name", "load_balancer", "public_ip"}) {
            addr = Json.field(chunk, key);
            if (!addr.isEmpty()) break;
        }
        if (addr.isEmpty()) {
            throw new VxException("client.ensureNodeUrl", "no resolvable node address", 0,
                    doc.substring(0, Math.min(200, doc.length())));
        }
        nodeUrl = rstripSlash(addr.startsWith("http") ? addr : "https://" + addr);
        return nodeUrl;
    }

    // ── generic verbs ────────────────────────────────────────────────────

    public String get(String path) { return get(path, Map.of(), DEFAULT_TIMEOUT); }

    public String get(String path, Map<String, String> extra, Duration timeout) {
        return doRequest("GET", resolve(path), null, extra, timeout).body();
    }

    public String post(String path, String jsonBody) {
        return post(path, jsonBody, Map.of(), DEFAULT_TIMEOUT);
    }

    public String post(String path, String jsonBody, Map<String, String> extra, Duration timeout) {
        return doRequest("POST", resolve(path), jsonBody, extra, timeout).body();
    }

    public String patch(String path, String jsonBody) {
        return patch(path, jsonBody, Map.of(), DEFAULT_TIMEOUT);
    }

    public String patch(String path, String jsonBody, Map<String, String> extra, Duration timeout) {
        return doRequest("PATCH", resolve(path), jsonBody, extra, timeout).body();
    }

    public String delete(String path, Map<String, String> extra, Duration timeout) {
        return doRequest("DELETE", resolve(path), null, extra, timeout).body();
    }

    // ── convenience surfaces ─────────────────────────────────────────────

    public String health()        { return get("/api/v2/health"); }
    public String cicdPipelines() { return get("/api/v2/cicd/pipelines"); }
    public String sessions()      { return get("/api/v2/tenant/sessions?username=" + username); }

    public String agentcontrolSummary()     { return get("/api/v2/agentcontrol/summary", tenantHeader(), DEFAULT_TIMEOUT); }
    public String agentcontrolAgents()      { return get("/api/v2/agentcontrol/agents/", tenantHeader(), DEFAULT_TIMEOUT); }
    public String agentcontrolModels()      { return get("/api/v2/agentcontrol/models/", tenantHeader(), DEFAULT_TIMEOUT); }
    public String agentcontrolDeployments() { return get("/api/v2/agentcontrol/deployments/", tenantHeader(), DEFAULT_TIMEOUT); }
    public String agentcontrolLlmProviders(){ return get("/api/v2/agentcontrol/llm/providers", tenantHeader(), DEFAULT_TIMEOUT); }

    public String agentcontrolChat(String agentId, String message) {
        String body = "{\"message\":\"" + jsonEscape(message) + "\"}";
        return post("/api/v2/agentcontrol/agents/" + agentId + "/chat", body, tenantHeader(), LONG_TIMEOUT);
    }

    /** List training datasets — {@code GET /api/v2/agentcontrol/datasets/}. */
    public String agentcontrolDatasets() { return get("/api/v2/agentcontrol/datasets/", tenantHeader(), DEFAULT_TIMEOUT); }

    // ── SalesShift: sales email service (mirrors python vxsdk SalesShift) ──

    /**
     * Send one tracked email through the org's BYOK provider (tenant-node
     * email worker preferred) — {@code POST {infinity}/api/v1/salesshift/email/send}.
     * Merge tags like {{first_name}} resolve against the contact record;
     * suppressed/unsubscribed recipients are rejected.
     */
    public String salesshiftSendEmail(String toEmail, String subject, String bodyHtml) {
        if (isEmpty(toEmail) || isEmpty(subject) || isEmpty(bodyHtml)) {
            throw new VxException("salesshift.send_email",
                    "toEmail, subject, bodyHtml required", 0, "");
        }
        assertNotMasked("salesshift.send_email", toEmail);
        String body = "{\"to_email\":\"" + jsonEscape(toEmail)
                + "\",\"subject\":\"" + jsonEscape(subject)
                + "\",\"body_html\":\"" + jsonEscape(bodyHtml) + "\"}";
        return post(infinityUrl + "/api/v1/salesshift/email/send", body);
    }

    /** Tracked outbound emails — {@code GET {infinity}/api/v1/salesshift/emails[?status=]}. */
    public String salesshiftEmails(String status) {
        String url = infinityUrl + "/api/v1/salesshift/emails";
        if (!isEmpty(status)) url += "?status=" + status;
        return get(url);
    }

    /** Live dashboard stats — {@code GET {infinity}/api/v1/salesshift/stats}. */
    public String salesshiftStats() {
        return get(infinityUrl + "/api/v1/salesshift/stats");
    }

    /** Tenant-node email worker health — {@code GET {node}/api/v2/salesshift/email/health}. */
    public String salesshiftWorkerHealth() {
        return get("/api/v2/salesshift/email/health");
    }

    // ── SalesShift: the leads pool ───────────────────────────────────────
    //
    // Implements LEADS_CLIENT_CONTRACT.md for Java: flat `leads*` naming, raw
    // JSON string returns, no typed models (this SDK ships no JSON dependency).
    //
    // Every path below is an ABSOLUTE Infinity control-plane URL. Leads do NOT
    // live on the tenant node — a relative path would resolve() against nodeUrl
    // and 404, which is why the salesshift email methods above build absolute
    // URLs too.
    //
    // FIVE RULES A CALLER MUST NOT BREAK (contract §1). Each exists because
    // breaking it causes a specific, expensive failure:
    //
    //  1. LEADS ARE NOT MAILABLE. Never pass a pool_id or a saved lead id into
    //     a send / sequence / campaign call. leadsConvert and
    //     leadsConvertFromPool are the ONLY routes to a mailable record: they
    //     create a Contact, which is where consent metadata is written. A
    //     scraped record entering a send path is how a sending domain dies.
    //  2. AN UNREVEALED ADDRESS IS A MASK, NOT AN ADDRESS. The server replaces
    //     the local part with its first letter plus a bullet run and leaves the
    //     domain intact. `has_email` says an address EXISTS; `email_revealed`
    //     says you are allowed to see it. Never render a mask as real, never
    //     let it be copied as one, never feed it to anything that sends.
    //  3. REVEAL SPENDS METERED QUOTA. Read leadsQuota() and show `remaining`
    //     BEFORE any bulk action, and render every skipped bucket afterwards.
    //  4. leadsConvertFromPool ACCOUNTS FOR EVERY ID PASSED IN — converted,
    //     already_converted, skipped_no_quota, skipped_no_email, skipped_erased.
    //     Printing only `converted` hides a partial spend; see
    //     leadsConvertFromPoolSummary(String), which renders all of them.
    //  5. ERASURE IS GLOBAL AND IRREVERSIBLE — it removes the person for EVERY
    //     tenant, not just yours. leadsErasure therefore refuses to act without
    //     an explicit confirmation flag.
    //
    // Two response-rendering rules that are easy to get wrong:
    //   - `next_cursor` is OPAQUE. Never parse one, never construct one, never
    //     reuse one across a sort or filter change — a keyset position is only
    //     meaningful within the ordering it came from.
    //   - `total` is capped at 10000. When `total_is_estimate` is true, render
    //     `total_display` ("10,000+"), never the raw number.

    /**
     * Search the global lead pool &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/search}. Returns raw JSON.
     *
     * <p>Reading the response:
     * <ul>
     *   <li>{@code data.items[].email} is MASKED unless
     *       {@code email_revealed} is true. {@code has_email} only says an
     *       address exists. Call {@link #leadsReveal(String)} to unmask &mdash;
     *       that spends quota.</li>
     *   <li>Render {@code data.sort} &mdash; the sort the server APPLIED. An
     *       unknown sort field degrades to {@code score} desc server-side, so
     *       the requested sort may not be the effective one.</li>
     *   <li>Render {@code data.total_display} whenever
     *       {@code data.total_is_estimate} is true; {@code total} is capped at
     *       10000 and would read as an exact 10000.</li>
     *   <li>Page by feeding {@code data.next_cursor} back into
     *       {@link LeadQuery#cursor(String)} until it is null or empty. The
     *       cursor is opaque and is only valid for the sort and filters that
     *       produced it &mdash; changing either means starting from {@code ""}
     *       again.</li>
     *   <li>{@code data.search_backend} says which engine answered
     *       ({@code go-node}, or {@code fastapi-orm} during a node outage).</li>
     * </ul>
     *
     * @param query filters, paging and sort; {@code null} means "everything,
     *              first page, score desc"
     */
    public String leadsSearch(LeadQuery query) {
        LeadQuery q = query == null ? new LeadQuery() : query;
        List<String> parts = new ArrayList<>();
        parts.add("\"filters\":" + q.filtersJson());
        parts.add("\"result_type\":" + jstr(isEmpty(q.resultType) ? "person" : q.resultType));
        // Always sent, "" on the first page — the server treats a missing and an
        // empty cursor identically, and sending it explicitly keeps the "start
        // over after a sort change" reset visible in the payload.
        parts.add("\"cursor\":" + jstr(orEmpty(q.cursor)));
        parts.add("\"limit\":" + (q.limit <= 0 ? 25 : Math.min(q.limit, LEADS_PAGE_MAX)));
        if (!isEmpty(q.sortField)) {
            parts.add("\"sort\":{\"field\":" + jstr(q.sortField) + ",\"desc\":" + q.sortDesc + "}");
        }
        return post(salesshiftUrl("/leads/search"), "{" + String.join(",", parts) + "}",
                Map.of(), DEFAULT_TIMEOUT);
    }

    /**
     * Counts per seniority / department / country / email_status &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/facets}.
     *
     * <p>Only the FILTERS of {@code query} are sent; paging and sort are
     * meaningless here. Facets are pool-wide with no tenant overlay: the number
     * of Directors in the pool is the same for every org.
     */
    public String leadsFacets(LeadQuery query) {
        String body = "{\"filters\":" + (query == null ? new LeadQuery() : query).filtersJson() + "}";
        return post(salesshiftUrl("/leads/facets"), body, Map.of(), DEFAULT_TIMEOUT);
    }

    /**
     * Reveal allowance for this period &mdash;
     * {@code GET {infinity}/api/v1/salesshift/leads/quota}. Returns
     * {@code {used, allowance, remaining, display}}.
     *
     * <p>Call this and show {@code remaining} BEFORE any action that reveals
     * (contract §1.3) &mdash; {@link #leadsReveal(String)} and
     * {@link #leadsConvertFromPool(List, boolean, String)} both spend it.
     */
    public String leadsQuota() {
        return get(salesshiftUrl("/leads/quota"));
    }

    /**
     * Un-mask one pool person &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/reveal}. <b>Spends one
     * reveal from the metered allowance</b>, unless this org has already
     * revealed the record, in which case it is free.
     *
     * <p>The response carries the real {@code email} / {@code phone} /
     * {@code linkedin_url} plus the updated {@code quota} block &mdash; render
     * that, it is the one moment the meter is actually being read.
     *
     * <p>Errors worth handling by name (see {@link #leadsExplain(VxException)}):
     * <b>402</b> allowance spent &mdash; NOTHING was charged for this attempt;
     * <b>404</b> not in the pool; <b>410</b> erased at the person's request,
     * terminal and not retryable.
     */
    public String leadsReveal(String poolPersonId) {
        String id = requireId("salesshift.leads.reveal", "poolPersonId", poolPersonId);
        return post(salesshiftUrl("/leads/reveal"), "{\"pool_person_id\":" + jstr(id) + "}",
                Map.of(), DEFAULT_TIMEOUT);
    }

    /**
     * Copy pool rows into this org's saved leads &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/save}. Max 200 per call.
     *
     * <p>Free: saving does NOT reveal and does NOT spend quota. A row saved
     * while masked is stored without an address, and stays that way until it is
     * revealed &mdash; at which point the server back-fills the snapshot.
     *
     * <p>A saved lead is a SNAPSHOT and is still not mailable (contract §1.1).
     * Returns {@code {saved, already_saved}}: render both, or a re-save of a
     * list you already hold looks like it did nothing.
     */
    public String leadsSave(List<String> poolPersonIds) {
        final String op = "salesshift.leads.save";
        requireIds(op, "poolPersonIds", poolPersonIds, LEADS_BATCH_MAX);
        return post(salesshiftUrl("/leads/save"), "{\"pool_person_ids\":" + jarr(poolPersonIds) + "}",
                Map.of(), LONG_TIMEOUT);
    }

    /**
     * Full detail for one pool person &mdash;
     * {@code GET {infinity}/api/v1/salesshift/leads/pool/{pool_id}}.
     *
     * <p>Masking applies here exactly as in search &mdash; a detail view is not
     * a back door around the meter. {@code existing_contact_id} is non-null when
     * this org already owns the person, which is the check that stops someone
     * paying to reveal a contact they already have. <b>410</b> means erased.
     */
    public String leadsPoolPerson(String poolId) {
        String id = requireId("salesshift.leads.pool_person", "poolId", poolId);
        return get(salesshiftUrl("/leads/pool/" + id));
    }

    /**
     * A pool company and its people &mdash;
     * {@code GET {infinity}/api/v1/salesshift/leads/company/{company_id}}.
     *
     * <p>The people are returned three ways: {@code people} (all),
     * {@code new_prospects} and {@code existing_contacts} (already owned by this
     * org). Show the split rather than a raw headcount &mdash; it is what says
     * "you already have 4 of these" instead of inviting a re-buy.
     */
    public String leadsCompany(String companyId) {
        String id = requireId("salesshift.leads.company", "companyId", companyId);
        return get(salesshiftUrl("/leads/company/" + id));
    }

    /**
     * This org's saved leads &mdash;
     * {@code GET {infinity}/api/v1/salesshift/leads[?status=&limit=]}.
     *
     * <p>Each row carries {@code email_masked} and {@code has_email} alongside
     * {@code email}: a lead saved before it was revealed has {@code email: null}
     * but {@code has_email: true}, which means "not paid for yet", NOT "this
     * person has no address". Rendering the second is how a rep skips a good
     * prospect. {@code erasure_pending} marks a row the person asked to erase.
     *
     * @param status optional filter (e.g. {@code new}, {@code converted}); null/empty for all
     * @param limit  0 for the server default (100); capped at 500
     */
    public String leadsList(String status, int limit) {
        StringBuilder url = new StringBuilder(salesshiftUrl("/leads"));
        char sep = '?';
        if (!isEmpty(status)) {
            url.append(sep).append("status=").append(urlValue(status));
            sep = '&';
        }
        if (limit > 0) url.append(sep).append("limit=").append(Math.min(limit, 500));
        return get(url.toString());
    }

    /**
     * One saved lead plus the live pool row behind it &mdash;
     * {@code GET {infinity}/api/v1/salesshift/leads/{lead_id}}.
     *
     * <p>{@code drift} is non-empty when the pool has moved on since the
     * snapshot was taken (title changed, company changed, address went from
     * verified to invalid). Show it &mdash; that is the difference between
     * noticing a stale record and discovering it after a bounce. <b>404</b> here
     * means "not your lead", not "not in the pool".
     */
    public String leadsGet(String leadId) {
        String id = requireId("salesshift.leads.get", "leadId", leadId);
        return get(salesshiftUrl("/leads/" + id));
    }

    /**
     * Update a saved lead's status / notes / tags / score &mdash;
     * {@code PATCH {infinity}/api/v1/salesshift/leads/{lead_id}}. Only the
     * fields set on {@code changes} are sent, so an unset field is left alone
     * rather than nulled.
     */
    public String leadsUpdate(String leadId, LeadUpdate changes) {
        final String op = "salesshift.leads.update";
        String id = requireId(op, "leadId", leadId);
        if (changes == null) throw new VxException(op, "changes required", 0, "");
        return patch(salesshiftUrl("/leads/" + id), changes.toJson(op), Map.of(), DEFAULT_TIMEOUT);
    }

    /**
     * Convert one saved lead into a Contact &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/{lead_id}/convert}. This is
     * the ONE-WAY GATE that makes a record mailable (contract §1.1); the lead
     * row is kept as an audit trail, never moved.
     *
     * <p>Requires an address on the lead: a <b>400</b> here means either the
     * lead has not been revealed yet (reveal it first) or the person requested
     * erasure &mdash; the {@code detail} says which. An already-converted lead
     * answers 200 with {@code already_converted: true} and the existing
     * {@code contact_id}, so it is safe to retry.
     *
     * @param lifecycleStage optional; server default {@code "lead"}
     */
    public String leadsConvert(String leadId, String lifecycleStage) {
        String id = requireId("salesshift.leads.convert", "leadId", leadId);
        String body = isEmpty(lifecycleStage) ? "{}"
                : "{\"lifecycle_stage\":" + jstr(lifecycleStage) + "}";
        return post(salesshiftUrl("/leads/" + id + "/convert"), body, Map.of(), DEFAULT_TIMEOUT);
    }

    /**
     * Convert many saved leads &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/bulk-convert}.
     *
     * <p>Spends NO quota: it never reveals. Leads with no address are returned
     * as {@code skipped_no_email} rather than converted &mdash; render
     * {@code converted}, {@code skipped_no_email} AND {@code already_converted},
     * or a partial result reads as a complete one.
     */
    public String leadsBulkConvert(List<String> leadIds) {
        final String op = "salesshift.leads.bulk_convert";
        requireIds(op, "leadIds", leadIds, 0);
        return post(salesshiftUrl("/leads/bulk-convert"), "{\"lead_ids\":" + jarr(leadIds) + "}",
                Map.of(), LONG_TIMEOUT);
    }

    /**
     * Pool &rarr; Contact in one step: save, reveal if needed, convert &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/convert-from-pool}. Max 200
     * ids per call.
     *
     * <p><b>This is a metered bulk action.</b> With
     * {@code revealIfNeeded = true}, every id that is not already revealed and
     * does have an address costs ONE reveal. There is deliberately no overload
     * that defaults this to true: read {@link #leadsQuota()} and show
     * {@code remaining} against the batch size before calling, so the cost is
     * visible before it is spent (contract §1.3). With
     * {@code revealIfNeeded = false} nothing is ever charged &mdash; only
     * already-revealed records convert and the rest come back as
     * {@code skipped_no_quota}.
     *
     * <p>The response accounts for EVERY id: {@code converted},
     * {@code revealed_now}, {@code already_converted}, {@code skipped_no_quota},
     * {@code skipped_no_email}, {@code skipped_erased}, plus {@code contact_ids}
     * and the updated {@code quota}. Rendering only {@code converted} hides a
     * partial spend (contract §1.4) &mdash;
     * {@link #leadsConvertFromPoolSummary(String)} renders all of them.
     *
     * <p>When quota runs out mid-batch the server converts what it can and
     * reports the remainder; that is a 200, not a 402.
     *
     * @param revealIfNeeded whether unrevealed records may be paid for
     * @param lifecycleStage optional; server default {@code "lead"}
     */
    public String leadsConvertFromPool(List<String> poolPersonIds, boolean revealIfNeeded,
                                       String lifecycleStage) {
        final String op = "salesshift.leads.convert_from_pool";
        requireIds(op, "poolPersonIds", poolPersonIds, LEADS_BATCH_MAX);
        List<String> parts = new ArrayList<>();
        parts.add("\"pool_person_ids\":" + jarr(poolPersonIds));
        parts.add("\"reveal_if_needed\":" + revealIfNeeded);
        if (!isEmpty(lifecycleStage)) parts.add("\"lifecycle_stage\":" + jstr(lifecycleStage));
        return post(salesshiftUrl("/leads/convert-from-pool"), "{" + String.join(",", parts) + "}",
                Map.of(), LONG_TIMEOUT);
    }

    /**
     * Right to be forgotten &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/erasure}, with
     * {@code reason} defaulting to {@code "gdpr_erasure"}.
     *
     * <p><b>GLOBAL AND IRREVERSIBLE.</b> See
     * {@link #leadsErasure(String, String, String, String, boolean)}.
     */
    public String leadsErasure(String email, String note, boolean confirmGlobalIrreversible) {
        return leadsErasure(email, null, null, note, confirmGlobalIrreversible);
    }

    /**
     * Right to be forgotten &mdash;
     * {@code POST {infinity}/api/v1/salesshift/leads/erasure}.
     *
     * <p><b>GLOBAL AND IRREVERSIBLE</b> (contract §1.5). This does not clean up
     * your copy of a person: it deactivates them in the pool for EVERY tenant,
     * strips the contact fields from every org's saved leads, and records a hash
     * so no future crawl can resurrect them. There is no undo, and the address
     * itself is not retained afterwards.
     *
     * <p>Because of that, this method refuses to act unless
     * {@code confirmGlobalIrreversible} is true &mdash; any UI in front of it
     * must say what it does and take an explicit confirmation.
     *
     * <p>Returns {@code {pool_rows_erased, saved_leads_flagged, already_recorded}}.
     * All three zero simply means nobody in the pool matched; the block is still
     * recorded for future crawls.
     *
     * @param email        the address to erase; optional only if {@code linkedinUrl} is given
     * @param linkedinUrl  alternative identifier when no address is known
     * @param reason       audit reason; empty defaults to {@code "gdpr_erasure"}
     * @param note         free-text note for the erasure record
     * @param confirmGlobalIrreversible must be true, acknowledging the above
     */
    public String leadsErasure(String email, String linkedinUrl, String reason, String note,
                               boolean confirmGlobalIrreversible) {
        final String op = "salesshift.leads.erasure";
        if (isEmpty(email) && isEmpty(linkedinUrl)) {
            throw new VxException(op, "email or linkedinUrl required", 0, "");
        }
        if (!confirmGlobalIrreversible) {
            throw new VxException(op,
                    "refusing to erase without explicit confirmation: this removes the person "
                    + "from the pool for EVERY tenant, strips every org's saved copy, and cannot "
                    + "be undone - pass confirmGlobalIrreversible=true to proceed", 0, "");
        }
        List<String> parts = new ArrayList<>();
        if (!isEmpty(email))       parts.add("\"email\":" + jstr(email));
        if (!isEmpty(linkedinUrl)) parts.add("\"linkedin_url\":" + jstr(linkedinUrl));
        parts.add("\"reason\":" + jstr(isEmpty(reason) ? "gdpr_erasure" : reason));
        if (!isEmpty(note))        parts.add("\"note\":" + jstr(note));
        return post(salesshiftUrl("/leads/erasure"), "{" + String.join(",", parts) + "}",
                Map.of(), LONG_TIMEOUT);
    }

    /** This org's saved searches — {@code GET {infinity}/api/v1/salesshift/lead-searches}. */
    public String leadsSavedSearches() {
        return get(salesshiftUrl("/lead-searches"));
    }

    /**
     * Crawls a company's own website and folds what it finds into the pool.
     * {@code POST {infinity}/api/v1/salesshift/leads/enrich}. Returns raw JSON.
     *
     * <p>The only call on this client that WRITES to the shared pool. What it
     * refuses to do matters as much as what it does: it fills gaps and never
     * overwrites an existing value; it checks the erasure list before every
     * insert, so a crawl cannot resurrect someone who asked to be forgotten;
     * it drops shared mailboxes ({@code sales@}, {@code info@},
     * {@code announce@} and their regional variants) because those are not
     * people; and everything it stores is UNVERIFIED. No reveal quota is
     * spent.
     *
     * <p>Pass {@code companyId} for a company already in the pool, or
     * {@code domain} for one that is not — in which case the crawl CREATES the
     * company, and any people it finds, too. At least one is required.
     *
     * <p>Read {@code crawled} in the response first: when it is 0, {@code note}
     * says why (blocked by a CDN, nothing readable, a server error) and nothing
     * was written. Slow by nature — up to a dozen page fetches, with a 90s
     * ceiling server-side.
     *
     * @throws IllegalArgumentException if both arguments are blank.
     */
    public String leadsEnrich(String domain, String companyId) {
        if (isEmpty(domain) && isEmpty(companyId)) {
            throw new IllegalArgumentException(
                "leadsEnrich: domain or companyId is required");
        }
        List<String> parts = new ArrayList<>();
        if (!isEmpty(companyId)) parts.add("\"company_id\":" + jstr(companyId));
        if (!isEmpty(domain))    parts.add("\"domain\":" + jstr(domain.trim().toLowerCase()));
        // LONG_TIMEOUT, like erasure: a crawl fetches up to a dozen pages and
        // the server allows itself 90 seconds. The default timeout would give
        // up on a perfectly healthy call.
        return post(salesshiftUrl("/leads/enrich"), "{" + String.join(",", parts) + "}",
                Map.of(), LONG_TIMEOUT);
    }

    /**
     * Save a search &mdash;
     * {@code POST {infinity}/api/v1/salesshift/lead-searches}.
     *
     * <p>Only the FILTERS of {@code query} are stored &mdash; a saved search is
     * a filter set, not a page. Cursors are never persisted: one is only
     * meaningful within the result set and ordering it came from.
     */
    public String leadsSaveSearch(String name, LeadQuery query, boolean shared) {
        final String op = "salesshift.leads.save_search";
        if (isEmpty(name)) throw new VxException(op, "name is required", 0, "");
        String body = "{\"name\":" + jstr(name)
                + ",\"filters\":" + (query == null ? new LeadQuery() : query).filtersJson()
                + ",\"is_shared\":" + shared + "}";
        return post(salesshiftUrl("/lead-searches"), body, Map.of(), DEFAULT_TIMEOUT);
    }

    // ── leads: error and report helpers ──────────────────────────────────

    /**
     * True when a reveal failed because the allowance is spent (HTTP 402).
     * <b>Nothing was charged for the attempt</b> &mdash; say so, or the user
     * assumes the failed call still cost them one.
     */
    public static boolean leadsAllowanceSpent(VxException e) {
        return e != null && e.httpStatus() == 402;
    }

    /**
     * True when the record was erased at the person's request (HTTP 410).
     * Terminal: not an outage, not retryable, and no amount of quota brings it
     * back. Distinct from {@link VxException#isRetryable()}, which is false here
     * for exactly that reason.
     */
    public static boolean leadsErased(VxException e) {
        return e != null && e.httpStatus() == 410;
    }

    /**
     * A sentence a caller can show for a failed leads call, so the four statuses
     * that mean something specific (contract §6) do not surface as a generic
     * "request failed". Falls back to the exception message.
     */
    public static String leadsExplain(VxException e) {
        if (e == null) return "";
        switch (e.httpStatus()) {
            case 402:
                return "Reveal allowance spent for this period - and nothing was charged "
                        + "for this attempt.";
            case 404:
                return "Not found - either this person is not in the pool, or that lead "
                        + "does not belong to your organisation.";
            case 410:
                return "This record was erased at the person's request. That is terminal: "
                        + "not an outage, not retryable.";
            case 400:
                return "Rejected: " + (e.detail().isEmpty()
                        ? "a malformed cursor, or a convert on a lead with no address."
                        : e.detail());
            default:
                return e.getMessage();
        }
    }

    /**
     * One line accounting for EVERY id passed to
     * {@link #leadsConvertFromPool(List, boolean, String)}, so no caller has to
     * assemble it and quietly render only the success count (contract §1.4).
     *
     * <p>Convenience over a flat single-object response, not a JSON parser
     * &mdash; pass it the raw body of a convert-from-pool call and nothing else.
     */
    public static String leadsConvertFromPoolSummary(String responseJson) {
        if (responseJson == null || responseJson.isEmpty()) return "";
        String quota = Json.field(responseJson, "display");
        return "converted " + count(responseJson, "converted")
                + " (revealed now " + count(responseJson, "revealed_now") + ")"
                + ", already converted " + count(responseJson, "already_converted")
                + ", skipped: no quota " + count(responseJson, "skipped_no_quota")
                + ", no address " + count(responseJson, "skipped_no_email")
                + ", erased " + count(responseJson, "skipped_erased")
                + (quota.isEmpty() ? "" : "; reveals used " + quota);
    }

    private static String count(String doc, String name) {
        String v = Json.field(doc, name);
        return v.isEmpty() ? "0" : v;
    }

    // ── leads: request options ───────────────────────────────────────────

    /**
     * Filters, paging and sort for {@link VxClient#leadsSearch(LeadQuery)},
     * {@link VxClient#leadsFacets(LeadQuery)} and
     * {@link VxClient#leadsSaveSearch(String, LeadQuery, boolean)}.
     *
     * <p>A parameter object rather than a long positional signature: there are
     * thirteen optional filters and a mis-ordered pair of string lists would
     * compile cleanly and search for the wrong thing. Unset fields are omitted
     * from the body entirely.
     *
     * <pre>{@code
     *   String page = c.leadsSearch(new VxClient.LeadQuery()
     *           .titles(List.of("director"))
     *           .countries(List.of("AU"))
     *           .minScore(50)
     *           .sort("score", true)
     *           .limit(50));
     * }</pre>
     */
    public static final class LeadQuery {
        private String q;
        private List<String> titles, excludeTitles, seniorities, departments, countries,
                industries, emailStatuses, employeeRanges, companyDomains, keywords;
        private Integer minScore;
        private Boolean hasEmail, hasPhone;
        private String resultType = "person";
        private String cursor = "";
        private int limit;
        private String sortField;
        private boolean sortDesc = true;

        /** Free text over name / title / company. */
        public LeadQuery q(String v)                        { this.q = v; return this; }
        /** Substring match on title, OR'd together. */
        public LeadQuery titles(List<String> v)             { this.titles = v; return this; }
        /** Substring match on title, AND'd as NOT. */
        public LeadQuery excludeTitles(List<String> v)      { this.excludeTitles = v; return this; }
        /** founder | c_suite | vp | director | manager | senior | entry. */
        public LeadQuery seniorities(List<String> v)        { this.seniorities = v; return this; }
        /** engineering | sales | marketing | finance | hr | ops | legal | executive. */
        public LeadQuery departments(List<String> v)        { this.departments = v; return this; }
        /** ISO-2 country codes; upper-cased server-side. */
        public LeadQuery countries(List<String> v)          { this.countries = v; return this; }
        public LeadQuery industries(List<String> v)         { this.industries = v; return this; }
        /** verified | unverified | guessed | catch_all | invalid. */
        public LeadQuery emailStatuses(List<String> v)      { this.emailStatuses = v; return this; }
        /** e.g. {@code 1-10}, {@code 11-50}, {@code 5000+}. */
        public LeadQuery employeeRanges(List<String> v)     { this.employeeRanges = v; return this; }
        public LeadQuery companyDomains(List<String> v)     { this.companyDomains = v; return this; }
        /** Substring match against company keywords. */
        public LeadQuery keywords(List<String> v)           { this.keywords = v; return this; }
        public LeadQuery minScore(Integer v)                { this.minScore = v; return this; }
        /**
         * Only rows where an address EXISTS — which is not the same as one you
         * may see; revealing is still what unmasks it.
         *
         * <p>In the contract's filter shape, but the server's {@code LeadFilters}
         * model does not declare it yet, so today it is dropped on both the node
         * and ORM paths rather than narrowing the result set. Sent anyway so the
         * binding needs no change when the server catches up — do not rely on it
         * to filter until it does.
         */
        public LeadQuery hasEmail(Boolean v)                { this.hasEmail = v; return this; }
        /** See {@link #hasEmail(Boolean)} — same "declared in the contract, not yet honoured" caveat. */
        public LeadQuery hasPhone(Boolean v)                { this.hasPhone = v; return this; }
        /** {@code person} (default) or {@code company}. */
        public LeadQuery resultType(String v)               { this.resultType = v; return this; }

        /**
         * The opaque {@code next_cursor} from the previous page, or {@code ""}
         * to start over.
         *
         * <p>Never parse or construct one, and never carry one across a sort or
         * filter change: a keyset position is only meaningful within the
         * ordering that produced it, and reusing it compares the wrong column,
         * silently dropping or repeating rows. After changing filters or sort,
         * reset to {@code ""}.
         */
        public LeadQuery cursor(String v)                   { this.cursor = v; return this; }

        /** Page size; 0 uses the server default of 25. Max 100. */
        public LeadQuery limit(int v)                       { this.limit = v; return this; }

        /**
         * People: {@code score} (default), {@code name}, {@code title},
         * {@code company}, {@code location}, {@code employees}, {@code email}.
         * Companies: {@code score}, {@code name}, {@code employees},
         * {@code industry}, {@code location}.
         *
         * <p>An unknown field silently degrades to {@code score} desc
         * server-side, so render {@code data.sort} from the response rather than
         * what was asked for here.
         */
        public LeadQuery sort(String field, boolean desc) {
            this.sortField = field;
            this.sortDesc = desc;
            return this;
        }

        /** The {@code filters} object alone — shared by search, facets and save-search. */
        private String filtersJson() {
            List<String> f = new ArrayList<>();
            if (!isEmpty(q))                f.add("\"q\":" + jstr(q));
            if (notEmpty(titles))           f.add("\"titles\":" + jarr(titles));
            if (notEmpty(excludeTitles))    f.add("\"exclude_titles\":" + jarr(excludeTitles));
            if (notEmpty(seniorities))      f.add("\"seniorities\":" + jarr(seniorities));
            if (notEmpty(departments))      f.add("\"departments\":" + jarr(departments));
            if (notEmpty(countries))        f.add("\"countries\":" + jarr(countries));
            if (notEmpty(industries))       f.add("\"industries\":" + jarr(industries));
            if (notEmpty(emailStatuses))    f.add("\"email_statuses\":" + jarr(emailStatuses));
            if (notEmpty(employeeRanges))   f.add("\"employee_ranges\":" + jarr(employeeRanges));
            if (notEmpty(companyDomains))   f.add("\"company_domains\":" + jarr(companyDomains));
            if (notEmpty(keywords))         f.add("\"keywords\":" + jarr(keywords));
            if (minScore != null)           f.add("\"min_score\":" + minScore.intValue());
            if (hasEmail != null)           f.add("\"has_email\":" + hasEmail.booleanValue());
            if (hasPhone != null)           f.add("\"has_phone\":" + hasPhone.booleanValue());
            return "{" + String.join(",", f) + "}";
        }
    }

    /**
     * The mutable fields of a saved lead, for
     * {@link VxClient#leadsUpdate(String, LeadUpdate)}. Only the fields set here
     * are sent, so unset ones keep their current value instead of being nulled.
     */
    public static final class LeadUpdate {
        private String status, notes, disqualifyReason, ownerId;
        private Integer score;
        private List<String> tags;

        /** e.g. {@code new}, {@code working}, {@code qualified}, {@code disqualified}. */
        public LeadUpdate status(String v)            { this.status = v; return this; }
        public LeadUpdate notes(String v)             { this.notes = v; return this; }
        public LeadUpdate tags(List<String> v)        { this.tags = v; return this; }
        public LeadUpdate score(Integer v)            { this.score = v; return this; }
        public LeadUpdate disqualifyReason(String v)  { this.disqualifyReason = v; return this; }
        public LeadUpdate ownerId(String v)           { this.ownerId = v; return this; }

        private String toJson(String op) {
            List<String> f = new ArrayList<>();
            if (status != null)           f.add("\"status\":" + jstr(status));
            if (notes != null)            f.add("\"notes\":" + jstr(notes));
            if (tags != null)             f.add("\"tags\":" + jarr(tags));
            if (score != null)            f.add("\"score\":" + score.intValue());
            if (disqualifyReason != null) f.add("\"disqualify_reason\":" + jstr(disqualifyReason));
            if (ownerId != null)          f.add("\"owner_id\":" + jstr(ownerId));
            if (f.isEmpty()) throw new VxException(op, "no fields to update", 0, "");
            return "{" + String.join(",", f) + "}";
        }
    }

    // ── training (mirrors python vxsdk Training) ─────────────────────────

    /** List training jobs — {@code GET /api/v2/agentcontrol/training/}. */
    public String agentcontrolTraining() {
        return get("/api/v2/agentcontrol/training/", tenantHeader(), DEFAULT_TIMEOUT);
    }

    /** Fetch one training job — {@code GET /api/v2/agentcontrol/training/{id}}. */
    public String agentcontrolTrainingGet(String jobId) {
        if (jobId == null || jobId.isEmpty()) {
            throw new VxException("agentcontrol.training.get", "jobId is required", 0, "");
        }
        return get("/api/v2/agentcontrol/training/" + jobId, tenantHeader(), DEFAULT_TIMEOUT);
    }

    /**
     * Create a training job — {@code POST /api/v2/agentcontrol/training/} with body
     * {@code {name, base_model, dataset_id, type, total_epochs}}. Returns the raw JSON.
     */
    public String agentcontrolTrainingCreate(String name, String baseModel, String datasetId,
                                             String type, int totalEpochs) {
        if (isEmpty(name) || isEmpty(baseModel) || isEmpty(datasetId)) {
            throw new VxException("agentcontrol.training.create",
                    "name, baseModel, datasetId required", 0, "");
        }
        String body = "{"
                + "\"name\":\"" + jsonEscape(name) + "\","
                + "\"base_model\":\"" + jsonEscape(baseModel) + "\","
                + "\"dataset_id\":\"" + jsonEscape(datasetId) + "\","
                + "\"type\":\"" + jsonEscape(isEmpty(type) ? "pre-training" : type) + "\","
                + "\"total_epochs\":" + totalEpochs
                + "}";
        return post("/api/v2/agentcontrol/training/", body, tenantHeader(), LONG_TIMEOUT);
    }

    // ── fine-tuning (mirrors python vxsdk FineTuning) ────────────────────

    /** List fine-tuning jobs — {@code GET /api/v2/agentcontrol/fine-tuning/}. */
    public String agentcontrolFineTuning() {
        return get("/api/v2/agentcontrol/fine-tuning/", tenantHeader(), DEFAULT_TIMEOUT);
    }

    /** Fetch one fine-tuning job — {@code GET /api/v2/agentcontrol/fine-tuning/{id}}. */
    public String agentcontrolFineTuningGet(String jobId) {
        if (jobId == null || jobId.isEmpty()) {
            throw new VxException("agentcontrol.fine-tuning.get", "jobId is required", 0, "");
        }
        return get("/api/v2/agentcontrol/fine-tuning/" + jobId, tenantHeader(), DEFAULT_TIMEOUT);
    }

    /**
     * Create a fine-tuning job — {@code POST /api/v2/agentcontrol/fine-tuning/} with body
     * {@code {name, base_model, training_file, epochs, batch_size, learning_rate}}.
     */
    public String agentcontrolFineTuningCreate(String name, String baseModel, String trainingFile,
                                               int epochs, int batchSize, double learningRate) {
        if (isEmpty(name) || isEmpty(baseModel) || isEmpty(trainingFile)) {
            throw new VxException("agentcontrol.fine-tuning.create",
                    "name, baseModel, trainingFile required", 0, "");
        }
        String body = "{"
                + "\"name\":\"" + jsonEscape(name) + "\","
                + "\"base_model\":\"" + jsonEscape(baseModel) + "\","
                + "\"training_file\":\"" + jsonEscape(trainingFile) + "\","
                + "\"epochs\":" + epochs + ","
                + "\"batch_size\":" + batchSize + ","
                + "\"learning_rate\":" + learningRate
                + "}";
        return post("/api/v2/agentcontrol/fine-tuning/", body, tenantHeader(), LONG_TIMEOUT);
    }

    // ── serving (mirrors python vxsdk vllm_artifact) ─────────────────────

    /**
     * Generate a deployable vLLM OpenAI-compatible serving artifact
     * (docker-compose + command args + test curl) —
     * {@code POST /api/v2/agentcontrol/serving/vllm-artifact} with body
     * {@code {model, port?, quantization?, max_model_len?}}. Optional fields are
     * omitted when 0/empty, matching the Python SDK. Returns the raw JSON.
     */
    public String agentcontrolVllmArtifact(String model, int port, String quantization, int maxModelLen) {
        if (isEmpty(model)) {
            throw new VxException("agentcontrol.vllm_artifact", "model is required", 0, "");
        }
        StringBuilder body = new StringBuilder("{\"model\":\"").append(jsonEscape(model)).append("\"");
        if (port != 0)         body.append(",\"port\":").append(port);
        if (!isEmpty(quantization)) body.append(",\"quantization\":\"").append(jsonEscape(quantization)).append("\"");
        if (maxModelLen != 0)  body.append(",\"max_model_len\":").append(maxModelLen);
        body.append("}");
        return post("/api/v2/agentcontrol/serving/vllm-artifact", body.toString(), tenantHeader(), LONG_TIMEOUT);
    }

    // ── internals ────────────────────────────────────────────────────────

    private Map<String, String> tenantHeader() {
        if (tenantId.isEmpty()) {
            throw new VxException("agentcontrol", "tenantId required (set on builder)", 0, "");
        }
        Map<String, String> h = new LinkedHashMap<>();
        h.put("X-Tenant-ID", tenantId);
        return h;
    }

    private String resolve(String path) {
        if (path.startsWith("http")) return path;
        ensureNodeUrl();
        return nodeUrl + (path.startsWith("/") ? path : "/" + path);
    }

    private Map<String, String> authHeaders(String url) {
        Map<String, String> h = new LinkedHashMap<>();
        if (!accessToken.isEmpty()) h.put("Authorization", "Bearer " + accessToken);
        boolean targetsNode = !nodeUrl.isEmpty() && url.startsWith(nodeUrl);
        if (!apiKey.isEmpty() && !(!accessToken.isEmpty() && targetsNode)) {
            h.put("X-API-Key", apiKey);
        }
        return h;
    }

    private HttpResponse<String> doRequest(String method, String url, String body,
                                           Map<String, String> extra, Duration timeout) {
        Map<String, String> base = new LinkedHashMap<>();
        base.put("Accept", "application/json");
        if (body != null) base.put("Content-Type", "application/json");
        base.putAll(extra);

        final int maxRetries = 3;
        boolean refreshed = false;
        HttpResponse<String> resp = null;
        for (int attempt = 0; attempt <= maxRetries; attempt++) {
            Map<String, String> h = new LinkedHashMap<>(base);
            h.putAll(authHeaders(url));
            try {
                resp = rawRequest(method, url, body, h, timeout);
            } catch (VxException e) {
                if (attempt >= maxRetries) throw e;
                sleep(200L * (1L << attempt));
                continue;
            }
            int st = resp.statusCode();
            if (st >= 200 && st < 300) return resp;

            if (st == 401 && !refreshed && !apiKey.isEmpty()) {
                refreshed = true;
                try { refresh(); continue; } catch (VxException ignored) { /* surface 401 */ }
            }
            boolean retryable = st == 429 || st >= 500;
            if (attempt < maxRetries && retryable) { sleep(200L * (1L << attempt)); continue; }
            String detail = resp.body() == null ? "" : trunc(resp.body(), 800);
            throw new VxException(method + " " + url, "http " + st, st, detail);
        }
        return resp;
    }

    private HttpResponse<String> rawRequest(String method, String url, String body,
                                            Map<String, String> headers, Duration timeout) {
        HttpRequest.Builder rb = HttpRequest.newBuilder(URI.create(url))
                .timeout(timeout)
                .header("User-Agent", userAgent);
        for (Map.Entry<String, String> e : headers.entrySet()) rb.header(e.getKey(), e.getValue());
        HttpRequest.BodyPublisher pub = (body == null)
                ? HttpRequest.BodyPublishers.noBody()
                : HttpRequest.BodyPublishers.ofString(body);
        rb.method(method, pub);
        try {
            return http.send(rb.build(), HttpResponse.BodyHandlers.ofString());
        } catch (Exception e) {
            throw new VxException("transport", e.getMessage() == null ? "network" : e.getMessage(), 0, "");
        }
    }

    private synchronized void refresh() {
        if (apiKey.isEmpty()) {
            throw new VxException("vxsdk.refresh", "no api key configured — cannot refresh JWT", 401, "");
        }
        String url = infinityUrl + "/api/v1/auth/developer/keys/login";
        String reqBody = "{\"api_key\":\"" + jsonEscape(apiKey) + "\",\"username\":\"" + jsonEscape(username) + "\"}";
        Map<String, String> h = new LinkedHashMap<>();
        h.put("Content-Type", "application/json");
        h.put("Accept", "application/json");
        HttpResponse<String> r = rawRequest("POST", url, reqBody, h, Duration.ofSeconds(15));
        if (r.statusCode() != 200) {
            throw new VxException("vxsdk.refresh", "exchange api key for jwt",
                    r.statusCode(), trunc(r.body(), 200));
        }
        String acc = Json.field(r.body(), "access");
        String ref = Json.field(r.body(), "refresh");
        if (acc.isEmpty()) {
            throw new VxException("vxsdk.refresh", "no access token in exchange response",
                    r.statusCode(), trunc(r.body(), 200));
        }
        this.accessToken = acc;
        if (!ref.isEmpty()) this.refreshToken = ref;
    }

    // ── small helpers ────────────────────────────────────────────────────

    private static void validateApiKey(String key) {
        if (!key.startsWith("xc_")) throw new VxException("vxsdk.Client", "api key must start with xc_", 401, "");
        String[] parts = key.split("_", 3);
        if (parts.length != 3) throw new VxException("vxsdk.Client", "api key format: xc_<env>_<token>", 401, "");
        if (!parts[1].equals("dev") && !parts[1].equals("test") && !parts[1].equals("live")) {
            throw new VxException("vxsdk.Client", "api key environment must be dev|test|live", 401, "");
        }
        if (parts[2].length() < 16) throw new VxException("vxsdk.Client", "api key token segment too short", 401, "");
    }

    private static String jsonEscape(String s) {
        StringBuilder b = new StringBuilder();
        for (int i = 0; i < s.length(); i++) {
            char c = s.charAt(i);
            switch (c) {
                case '"':  b.append("\\\""); break;
                case '\\': b.append("\\\\"); break;
                case '\n': b.append("\\n");  break;
                case '\r': b.append("\\r");  break;
                case '\t': b.append("\\t");  break;
                default:
                    // Any other control character is illegal raw inside a JSON
                    // string and would produce a body the server rejects as
                    // malformed — a filter pasted out of a spreadsheet can carry
                    // one invisibly.
                    if (c < 0x20) b.append(String.format("\\u%04x", (int) c));
                    else b.append(c);
            }
        }
        return b.toString();
    }

    /** A quoted, fully escaped JSON string. Every user-supplied value goes through this. */
    private static String jstr(String s) { return "\"" + jsonEscape(s == null ? "" : s) + "\""; }

    /** A JSON array of escaped strings; nulls become {@code ""}. */
    private static String jarr(List<String> values) {
        StringBuilder b = new StringBuilder("[");
        for (int i = 0; i < values.size(); i++) {
            if (i > 0) b.append(',');
            b.append(jstr(values.get(i)));
        }
        return b.append(']').toString();
    }

    private static boolean notEmpty(List<String> v) { return v != null && !v.isEmpty(); }

    /** Percent-encode a query-string value so a filter cannot alter the URL. */
    private static String urlValue(String v) { return URLEncoder.encode(v, StandardCharsets.UTF_8); }

    /**
     * Absolute Infinity control-plane URL for a SalesShift path. Leads and email
     * live on the control plane, never on the tenant node, so these must not be
     * passed as relative paths — {@link #resolve(String)} would send them to the
     * node.
     */
    private String salesshiftUrl(String path) { return infinityUrl + "/api/v1/salesshift" + path; }

    /**
     * Validate an id that will be interpolated into a URL path. Rejects the
     * characters that would end the segment and turn an id into a different
     * request.
     */
    private static String requireId(String op, String name, String value) {
        if (isEmpty(value)) throw new VxException(op, name + " is required", 0, "");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c <= ' ' || c == '/' || c == '?' || c == '#' || c == '%' || c == '&') {
                throw new VxException(op, name + " must be a bare id", 0, value);
            }
        }
        return value;
    }

    /** Validate a batch of ids: non-empty, no blanks, and within the server's cap. */
    private static void requireIds(String op, String name, List<String> ids, int max) {
        if (!notEmpty(ids)) throw new VxException(op, name + " required (at least one id)", 0, "");
        if (max > 0 && ids.size() > max) {
            throw new VxException(op,
                    "max " + max + " ids per call (got " + ids.size() + ")", 0, "");
        }
        for (String id : ids) {
            if (isEmpty(id)) throw new VxException(op, name + " contains an empty id", 0, "");
        }
    }

    /**
     * Refuse a masked pool address on a send path (leads contract §1.2). The
     * server masks an unrevealed address with U+2022 bullets, which cannot occur
     * in a deliverable address — so this only ever rejects a string that was
     * never going to be delivered, and it catches the one mistake that matters:
     * copying a pool row's display address straight into a send.
     */
    private static void assertNotMasked(String op, String email) {
        if (email.indexOf(MASK_BULLET) >= 0) {
            throw new VxException(op,
                    "that address is a MASK, not a real address - reveal the lead first, "
                    + "then convert it to a contact before sending", 0, "");
        }
    }

    private static void sleep(long ms) {
        try { Thread.sleep(Math.min(ms, 5000L)); } catch (InterruptedException e) { Thread.currentThread().interrupt(); }
    }
    private static String orEmpty(String s) { return s == null ? "" : s; }
    private static boolean isEmpty(String s) { return s == null || s.isEmpty(); }
    private static String rstripSlash(String s) {
        int end = s.length();
        while (end > 0 && s.charAt(end - 1) == '/') end--;
        return s.substring(0, end);
    }
    private static String trunc(String s, int n) { return s == null ? "" : s.substring(0, Math.min(n, s.length())); }
}
