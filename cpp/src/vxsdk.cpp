// vxsdk.cpp — libcurl-backed implementation of the C++ vxcloud SDK.

#include "vxsdk/vxsdk.hpp"

#include <curl/curl.h>

#include <cctype>
#include <cstdlib>
#include <cstring>
#include <sstream>
#include <thread>

namespace vx {

static const char* kVersion = "2026.8.13";

// ── VxError ───────────────────────────────────────────────────────────────

static std::string compose(const std::string& op, const std::string& message,
                           int status, const std::string& detail) {
    std::ostringstream os;
    os << op << ": ";
    if (status) os << status << " ";
    os << message;
    if (!detail.empty()) os << " — " << detail;
    return os.str();
}

VxError::VxError(std::string op, const std::string& message, int http_status,
                 std::string detail)
    : std::runtime_error(compose(op, message, http_status, detail)),
      http_status_(http_status), op_(std::move(op)), detail_(std::move(detail)) {}

// ── JSON extraction helpers ───────────────────────────────────────────────

namespace json {

// Find `"name"` then return the following JSON string value. Very small: it
// scans for the quoted key, skips ':' and whitespace, and if the value opens
// with '"' returns the (backslash-aware) string contents. Non-string values
// (numbers/bools) are returned verbatim up to the next , } ] or whitespace.
std::string field(const std::string& doc, const std::string& name) {
    const std::string needle = "\"" + name + "\"";
    size_t p = 0;
    while ((p = doc.find(needle, p)) != std::string::npos) {
        size_t i = p + needle.size();
        while (i < doc.size() && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\n' ||
                                  doc[i] == '\r'))
            ++i;
        if (i >= doc.size() || doc[i] != ':') { p += needle.size(); continue; }
        ++i;
        while (i < doc.size() && (doc[i] == ' ' || doc[i] == '\t' || doc[i] == '\n' ||
                                  doc[i] == '\r'))
            ++i;
        if (i >= doc.size()) return "";
        if (doc[i] == '"') {
            ++i;
            std::string out;
            while (i < doc.size() && doc[i] != '"') {
                if (doc[i] == '\\' && i + 1 < doc.size()) {
                    char n = doc[i + 1];
                    switch (n) {
                        case 'n': out += '\n'; break;
                        case 't': out += '\t'; break;
                        case 'r': out += '\r'; break;
                        default:  out += n;   break;
                    }
                    i += 2;
                } else {
                    out += doc[i++];
                }
            }
            return out;
        }
        // scalar
        std::string out;
        while (i < doc.size() && doc[i] != ',' && doc[i] != '}' && doc[i] != ']' &&
               doc[i] != ' ' && doc[i] != '\n' && doc[i] != '\r' && doc[i] != '\t')
            out += doc[i++];
        return out;
    }
    return "";
}

}  // namespace json

// ── libcurl plumbing ──────────────────────────────────────────────────────

namespace {

struct GlobalInit {
    GlobalInit()  { curl_global_init(CURL_GLOBAL_DEFAULT); }
    ~GlobalInit() { curl_global_cleanup(); }
};
GlobalInit g_curl_init;  // one-time process init

size_t write_cb(char* ptr, size_t size, size_t nmemb, void* userdata) {
    auto* s = static_cast<std::string*>(userdata);
    s->append(ptr, size * nmemb);
    return size * nmemb;
}

size_t header_cb(char* buffer, size_t size, size_t nitems, void* userdata) {
    auto* h = static_cast<std::map<std::string, std::string>*>(userdata);
    std::string line(buffer, size * nitems);
    size_t colon = line.find(':');
    if (colon != std::string::npos) {
        std::string k = line.substr(0, colon);
        std::string v = line.substr(colon + 1);
        auto trim = [](std::string& x) {
            while (!x.empty() && (x.back() == '\r' || x.back() == '\n' || x.back() == ' '))
                x.pop_back();
            size_t i = 0; while (i < x.size() && x[i] == ' ') ++i; x = x.substr(i);
        };
        trim(k); trim(v);
        for (auto& c : k) c = static_cast<char>(std::tolower(c));
        (*h)[k] = v;
    }
    return size * nitems;
}

}  // namespace

// ── Client ────────────────────────────────────────────────────────────────

static void validate_api_key(const std::string& key) {
    if (key.rfind("xc_", 0) != 0)
        throw VxError("vxsdk.Client", "api key must start with xc_", 401);
    // xc_<env>_<token>
    size_t a = key.find('_');
    size_t b = key.find('_', a + 1);
    if (a == std::string::npos || b == std::string::npos)
        throw VxError("vxsdk.Client", "api key format: xc_<env>_<token>", 401);
    std::string env = key.substr(a + 1, b - a - 1);
    if (env != "dev" && env != "test" && env != "live")
        throw VxError("vxsdk.Client", "api key environment must be dev|test|live", 401);
    if (key.size() - b - 1 < 16)
        throw VxError("vxsdk.Client", "api key token segment too short", 401);
}

static std::string rstrip_slash(std::string s) {
    while (!s.empty() && s.back() == '/') s.pop_back();
    return s;
}

Client::Client(ClientOptions o) {
    if (o.api_key.empty() && o.access_token.empty())
        throw VxError("vxsdk.Client", "no credentials: set api_key or access_token", 401);
    if (!o.api_key.empty()) validate_api_key(o.api_key);
    api_key_       = o.api_key;
    username_      = o.username;
    access_token_  = o.access_token;
    refresh_token_ = o.refresh_token;
    infinity_url_  = rstrip_slash(o.infinity_url.empty() ? kDefaultInfinityUrl : o.infinity_url);
    node_url_      = rstrip_slash(o.node_url);
    tenant_id_     = o.tenant_id;
    user_agent_    = o.user_agent.empty() ? (std::string("vxsdk-cpp/") + kVersion)
                                          : o.user_agent;
}

Client::~Client() = default;

std::map<std::string, std::string> Client::auth_headers(const std::string& url) const {
    std::map<std::string, std::string> h;
    if (!access_token_.empty())
        h["Authorization"] = "Bearer " + access_token_;
    bool targets_node = !node_url_.empty() && url.rfind(node_url_, 0) == 0;
    if (!api_key_.empty() && !(!access_token_.empty() && targets_node))
        h["X-API-Key"] = api_key_;
    return h;
}

std::map<std::string, std::string> Client::tenant_header() const {
    if (tenant_id_.empty())
        throw VxError("agentcontrol", "tenant_id required (set ClientOptions.tenant_id)", 0);
    return {{"X-Tenant-ID", tenant_id_}};
}

std::string Client::resolve(const std::string& path) {
    if (path.rfind("http", 0) == 0) return path;
    ensure_node_url();
    std::string p = path;
    if (!p.empty() && p[0] != '/') p = "/" + p;
    return node_url_ + p;
}

Response Client::raw_request(const std::string& method, const std::string& url,
                             const std::string& body,
                             const std::map<std::string, std::string>& headers,
                             long timeout) {
    CURL* curl = curl_easy_init();
    if (!curl) throw VxError("transport", "curl_easy_init failed", 0);

    Response resp;
    struct curl_slist* hdr = nullptr;
    for (const auto& kv : headers)
        hdr = curl_slist_append(hdr, (kv.first + ": " + kv.second).c_str());

    curl_easy_setopt(curl, CURLOPT_URL, url.c_str());
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hdr);
    curl_easy_setopt(curl, CURLOPT_USERAGENT, user_agent_.c_str());
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, timeout);
    curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, &resp.body);
    curl_easy_setopt(curl, CURLOPT_HEADERFUNCTION, header_cb);
    curl_easy_setopt(curl, CURLOPT_HEADERDATA, &resp.headers);

    if (method == "POST") {
        curl_easy_setopt(curl, CURLOPT_POST, 1L);
        curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body.c_str());
        curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, static_cast<long>(body.size()));
    } else if (method == "DELETE") {
        curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, "DELETE");
    } else if (method == "PUT" || method == "PATCH") {
        curl_easy_setopt(curl, CURLOPT_CUSTOMREQUEST, method.c_str());
        curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body.c_str());
        curl_easy_setopt(curl, CURLOPT_POSTFIELDSIZE, static_cast<long>(body.size()));
    }

    CURLcode rc = curl_easy_perform(curl);
    if (rc == CURLE_OK)
        curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &resp.status);
    std::string curl_err = (rc == CURLE_OK) ? "" : curl_easy_strerror(rc);

    curl_slist_free_all(hdr);
    curl_easy_cleanup(curl);

    if (rc != CURLE_OK)
        throw VxError("transport", curl_err, 0);
    return resp;
}

Response Client::do_request(const std::string& method, const std::string& url,
                            const std::string& body,
                            std::map<std::string, std::string> headers, long timeout) {
    headers["Accept"] = "application/json";
    if ((method == "POST" || method == "PUT" || method == "PATCH") &&
        headers.find("Content-Type") == headers.end())
        headers["Content-Type"] = "application/json";

    const int max_retries = 3;
    bool refreshed = false;
    Response resp;
    for (int attempt = 0; attempt <= max_retries; ++attempt) {
        auto h = headers;
        for (const auto& kv : auth_headers(url)) h[kv.first] = kv.second;
        try {
            resp = raw_request(method, url, body, h, timeout);
        } catch (const VxError& e) {
            if (attempt >= max_retries) throw;
            std::this_thread::sleep_for(std::chrono::milliseconds(200 * (1 << attempt)));
            continue;
        }
        if (resp.ok()) return resp;

        if (resp.status == 401 && !refreshed && !api_key_.empty()) {
            refreshed = true;
            try { refresh(); continue; } catch (const VxError&) { /* surface 401 */ }
        }
        bool retryable = resp.status == 429 || resp.status >= 500;
        if (attempt < max_retries && retryable) {
            std::this_thread::sleep_for(std::chrono::milliseconds(200 * (1 << attempt)));
            continue;
        }
        std::string detail = resp.body.substr(0, 800);
        throw VxError(method + " " + url, "http " + std::to_string(resp.status),
                      static_cast<int>(resp.status), detail);
    }
    return resp;
}

void Client::refresh() {
    if (api_key_.empty())
        throw VxError("vxsdk.refresh", "no api key configured — cannot refresh JWT", 401);
    std::string url = infinity_url_ + "/api/v1/auth/developer/keys/login";
    std::string body = "{\"api_key\":\"" + api_key_ + "\",\"username\":\"" + username_ + "\"}";
    std::map<std::string, std::string> h{{"Content-Type", "application/json"},
                                         {"Accept", "application/json"}};
    Response r = raw_request("POST", url, body, h, 15);
    if (r.status != 200)
        throw VxError("vxsdk.refresh", "exchange api key for jwt",
                      static_cast<int>(r.status), r.body.substr(0, 200));
    std::string acc = json::field(r.body, "access");
    std::string ref = json::field(r.body, "refresh");
    if (acc.empty())
        throw VxError("vxsdk.refresh", "no access token in exchange response",
                      static_cast<int>(r.status), r.body.substr(0, 200));
    access_token_ = acc;
    if (!ref.empty()) refresh_token_ = ref;
}

void Client::authenticate() { refresh(); }

const std::string& Client::ensure_node_url() {
    if (!node_url_.empty()) return node_url_;
    std::string url = infinity_url_ + "/api/v1/auth/nodes/";
    Response r = do_request("GET", url, "", {}, kDefaultTimeout);
    // Pick the default node (or the first) and read its address field.
    // We look for "is_default_node":true; failing that, the first node's
    // custom_domain_name / load_balancer / public_ip.
    const std::string& doc = r.body;
    std::string addr;
    size_t def = doc.find("\"is_default_node\":true");
    if (def == std::string::npos) def = doc.find("\"is_default_node\": true");
    size_t scan_from = (def != std::string::npos) ? doc.rfind('{', def) : 0;
    std::string chunk = (scan_from == std::string::npos) ? doc : doc.substr(scan_from);
    for (const char* key : {"custom_domain_name", "load_balancer", "public_ip"}) {
        addr = json::field(chunk, key);
        if (!addr.empty()) break;
    }
    if (addr.empty())
        throw VxError("client.ensure_node_url", "no resolvable node address", 0,
                      doc.substr(0, 200));
    node_url_ = (addr.rfind("http", 0) == 0) ? rstrip_slash(addr)
                                             : "https://" + rstrip_slash(addr);
    return node_url_;
}

std::string Client::get(const std::string& path,
                        const std::map<std::string, std::string>& extra, long timeout) {
    return do_request("GET", resolve(path), "", extra, timeout).body;
}
std::string Client::post(const std::string& path, const std::string& json_body,
                         const std::map<std::string, std::string>& extra, long timeout) {
    return do_request("POST", resolve(path), json_body, extra, timeout).body;
}
std::string Client::del(const std::string& path,
                        const std::map<std::string, std::string>& extra, long timeout) {
    return do_request("DELETE", resolve(path), "", extra, timeout).body;
}

// ── convenience surfaces ──

std::string Client::health()        { return get("/api/v2/health"); }
std::string Client::cicd_pipelines(){ return get("/api/v2/cicd/pipelines"); }
std::string Client::sessions() {
    std::string q = "/api/v2/tenant/sessions?username=";
    // very small URL-encode: username is a plain slug in practice.
    return get(q + username_);
}

std::string Client::agentcontrol_summary() {
    return get("/api/v2/agentcontrol/summary", tenant_header());
}
std::string Client::agentcontrol_agents() {
    return get("/api/v2/agentcontrol/agents/", tenant_header());
}
std::string Client::agentcontrol_models() {
    return get("/api/v2/agentcontrol/models/", tenant_header());
}
std::string Client::agentcontrol_deployments() {
    return get("/api/v2/agentcontrol/deployments/", tenant_header());
}
std::string Client::agentcontrol_llm_providers() {
    return get("/api/v2/agentcontrol/llm/providers", tenant_header());
}
// JSON string escaping for building request bodies without a JSON lib.
//
// This SDK ships no JSON dependency, so every request body is assembled by
// string concatenation — which makes this function the boundary that keeps a
// VALUE from becoming STRUCTURE. Any user-supplied string (a title filter, a
// note, a saved-search name, a chat message) must pass through here before it
// is pasted into a body. One unescaped `"` closes the string early and the
// remainder of the value is read as JSON: a title filter of
//   x","reveal_if_needed":true
// would otherwise silently rewrite the request the caller thought it was
// merely parameterising — and on the leads endpoints that flag spends metered
// quota. It is not a formatting nicety; it is the only thing standing between
// a stray quote and a different request.
//
// Escaped: the two characters JSON reserves (`"` and `\`), the five with short
// escapes (\b \f \n \r \t), and every other C0 control character as \u00XX —
// RFC 8259 forbids raw control bytes inside a string, so passing one through
// yields a body the server rejects as malformed rather than the one intended.
// Bytes >= 0x20 are copied verbatim, which leaves valid UTF-8 intact (every
// continuation byte of a multi-byte sequence is >= 0x80).
static std::string json_escape(const std::string& s) {
    static const char* kHex = "0123456789abcdef";
    std::string esc;
    esc.reserve(s.size() + 8);
    for (unsigned char c : s) {
        switch (c) {
            case '"':  esc += "\\\""; break;
            case '\\': esc += "\\\\"; break;
            case '\b': esc += "\\b";  break;
            case '\f': esc += "\\f";  break;
            case '\n': esc += "\\n";  break;
            case '\r': esc += "\\r";  break;
            case '\t': esc += "\\t";  break;
            default:
                if (c < 0x20) {
                    esc += "\\u00";
                    esc += kHex[(c >> 4) & 0x0F];
                    esc += kHex[c & 0x0F];
                } else {
                    esc += static_cast<char>(c);
                }
        }
    }
    return esc;
}

// The escaped value WITH its quotes. Bodies are built from jstr(x) rather than
// "\"" + json_escape(x) + "\"" so there is no way to remember the quotes and
// forget the escape.
static std::string jstr(const std::string& s) { return "\"" + json_escape(s) + "\""; }

// `["a","b"]` — every element escaped, empty vector renders as `[]`.
static std::string jarr(const std::vector<std::string>& values) {
    std::string out = "[";
    for (size_t i = 0; i < values.size(); ++i) {
        if (i) out += ',';
        out += jstr(values[i]);
    }
    return out + "]";
}

std::string Client::agentcontrol_chat(const std::string& agent_id, const std::string& message) {
    std::string body = "{\"message\":\"" + json_escape(message) + "\"}";
    return post("/api/v2/agentcontrol/agents/" + agent_id + "/chat", body, tenant_header(),
                kLongTimeout);
}

std::string Client::agentcontrol_datasets() {
    return get("/api/v2/agentcontrol/datasets/", tenant_header());
}

// ── AgentControl: training jobs ──
std::string Client::agentcontrol_training() {
    return get("/api/v2/agentcontrol/training/", tenant_header());
}
std::string Client::agentcontrol_training_get(const std::string& job_id) {
    return get("/api/v2/agentcontrol/training/" + job_id, tenant_header());
}
std::string Client::agentcontrol_training_create(const std::string& name,
                                                 const std::string& base_model,
                                                 const std::string& dataset_id,
                                                 const std::string& type,
                                                 int total_epochs) {
    std::string body = "{\"name\":\"" + json_escape(name) +
        "\",\"base_model\":\"" + json_escape(base_model) +
        "\",\"dataset_id\":\"" + json_escape(dataset_id) +
        "\",\"type\":\"" + json_escape(type) +
        "\",\"total_epochs\":" + std::to_string(total_epochs) + "}";
    return post("/api/v2/agentcontrol/training/", body, tenant_header(), kLongTimeout);
}

// ── AgentControl: fine-tuning jobs ──
std::string Client::agentcontrol_fine_tuning() {
    return get("/api/v2/agentcontrol/fine-tuning/", tenant_header());
}
std::string Client::agentcontrol_fine_tuning_create(const std::string& name,
                                                    const std::string& base_model,
                                                    const std::string& training_file,
                                                    int epochs, int batch_size,
                                                    double learning_rate) {
    std::string body = "{\"name\":\"" + json_escape(name) +
        "\",\"base_model\":\"" + json_escape(base_model) +
        "\",\"training_file\":\"" + json_escape(training_file) +
        "\",\"epochs\":" + std::to_string(epochs) +
        ",\"batch_size\":" + std::to_string(batch_size) +
        ",\"learning_rate\":" + std::to_string(learning_rate) + "}";
    return post("/api/v2/agentcontrol/fine-tuning/", body, tenant_header(), kLongTimeout);
}

// ── AgentControl: vLLM serving artifact ──
std::string Client::agentcontrol_vllm_artifact(const std::string& model, int port,
                                               const std::string& quantization,
                                               int max_model_len) {
    std::string body = "{\"model\":\"" + json_escape(model) + "\"";
    if (port) body += ",\"port\":" + std::to_string(port);
    if (!quantization.empty()) body += ",\"quantization\":\"" + json_escape(quantization) + "\"";
    if (max_model_len) body += ",\"max_model_len\":" + std::to_string(max_model_len);
    body += "}";
    return post("/api/v2/agentcontrol/serving/vllm-artifact", body, tenant_header());
}

// ── SalesShift: sales email service ──
std::string Client::salesshift_send_email(const std::string& to_email,
                                          const std::string& subject,
                                          const std::string& body_html) {
    std::string body = "{\"to_email\":\"" + json_escape(to_email) +
        "\",\"subject\":\"" + json_escape(subject) +
        "\",\"body_html\":\"" + json_escape(body_html) + "\"}";
    return post(infinity_url_ + "/api/v1/salesshift/email/send", body, {}, kLongTimeout);
}

std::string Client::salesshift_emails(const std::string& status) {
    std::string url = infinity_url_ + "/api/v1/salesshift/emails";
    if (!status.empty()) url += "?status=" + status;
    return get(url);
}

std::string Client::salesshift_stats() {
    return get(infinity_url_ + "/api/v1/salesshift/stats");
}

std::string Client::salesshift_worker_health() {
    return get("/api/v2/salesshift/email/health");
}

// ── SalesShift: the leads pool ────────────────────────────────────────────
//
// Every leads endpoint lives on the INFINITY control plane, so every URL below
// is built absolute from infinity_url_. A relative path would go through
// Client::resolve(), which joins it onto the tenant NODE and would 404 — the
// same reason the salesshift email methods above spell their URLs out.
//
// All bodies are assembled with JsonBuilder, which routes every user-supplied
// value through json_escape() (see the comment on that function: it is the one
// piece of real logic in this section).

namespace {

// Shared by every path in this section.
const char* kSalesShift = "/api/v1/salesshift";

// A JSON object under construction. Fields are pushed as pre-encoded
// `"key":value` fragments and joined once at the end, so no caller has to
// track where the commas go — a trailing comma is invalid JSON and surfaces as
// a 422 a long way from the line that caused it.
class JsonBuilder {
public:
    JsonBuilder& str(const std::string& key, const std::string& value) {
        fields_.push_back(jstr(key) + ":" + jstr(value));
        return *this;
    }
    // Omitted when empty: the server reads an absent field as "no constraint",
    // whereas "" is a constraint that matches nothing.
    JsonBuilder& str_if(const std::string& key, const std::string& value) {
        if (!value.empty()) str(key, value);
        return *this;
    }
    JsonBuilder& num(const std::string& key, long long value) {
        fields_.push_back(jstr(key) + ":" + std::to_string(value));
        return *this;
    }
    JsonBuilder& boolean(const std::string& key, bool value) {
        fields_.push_back(jstr(key) + ":" + (value ? "true" : "false"));
        return *this;
    }
    JsonBuilder& strings(const std::string& key, const std::vector<std::string>& values) {
        fields_.push_back(jstr(key) + ":" + jarr(values));
        return *this;
    }
    JsonBuilder& strings_if(const std::string& key, const std::vector<std::string>& values) {
        if (!values.empty()) strings(key, values);
        return *this;
    }
    // A value that is ALREADY encoded JSON — a nested object built by another
    // JsonBuilder. The single entry point that does not escape, and therefore
    // the one no user-supplied string may reach.
    JsonBuilder& raw(const std::string& key, const std::string& encoded_json) {
        fields_.push_back(jstr(key) + ":" + encoded_json);
        return *this;
    }
    std::string build() const {
        std::string out = "{";
        for (size_t i = 0; i < fields_.size(); ++i) {
            if (i) out += ',';
            out += fields_[i];
        }
        return out + "}";
    }

private:
    std::vector<std::string> fields_;
};

// LeadFilters → the `filters` object. Unset optionals and empty vectors are
// left out entirely rather than sent as null, because absent means "no
// constraint" on both backends.
std::string encode_filters(const LeadFilters& f) {
    JsonBuilder b;
    b.str_if("q", f.q)
     .strings_if("titles", f.titles)
     .strings_if("exclude_titles", f.exclude_titles)
     .strings_if("seniorities", f.seniorities)
     .strings_if("departments", f.departments)
     .strings_if("countries", f.countries)
     .strings_if("industries", f.industries)
     .strings_if("email_statuses", f.email_statuses)
     .strings_if("employee_ranges", f.employee_ranges)
     .strings_if("company_domains", f.company_domains)
     .strings_if("keywords", f.keywords);
    if (f.min_score) b.num("min_score", *f.min_score);
    if (f.has_email) b.boolean("has_email", *f.has_email);
    if (f.has_phone) b.boolean("has_phone", *f.has_phone);
    return b.build();
}

// Refuse an id batch locally instead of round-tripping to be told the same
// thing. `max_ids` of 0 means the endpoint has no ceiling; where there is one
// it is 200, and it exists because reveals are metered — a larger batch would
// spend more quota than anyone intends from one click.
void require_ids(const char* op, const std::vector<std::string>& ids, size_t max_ids) {
    if (ids.empty())
        throw VxError(op, "at least one id is required", 400);
    if (max_ids && ids.size() > max_ids)
        throw VxError(op, "max " + std::to_string(max_ids) +
                              " ids per call — reveals are metered, split the batch", 400);
}

}  // namespace

std::string Client::leadsSearch(const LeadSearchRequest& request) {
    JsonBuilder b;
    b.raw("filters", encode_filters(request.filters))
     .str("result_type", request.result_type.empty() ? "person" : request.result_type)
     // Sent back exactly as received. The cursor is opaque: the server is the
     // only thing entitled to interpret one, and this SDK never builds one.
     .str("cursor", request.cursor)
     .num("limit", request.limit);
    // Only sent when asked for — the server's own default (score desc) is what
    // an omitted sort means, and it echoes the sort it actually applied.
    if (!request.sort_field.empty())
        b.raw("sort", JsonBuilder().str("field", request.sort_field)
                                   .boolean("desc", request.sort_desc)
                                   .build());
    return post(infinity_url_ + kSalesShift + "/leads/search", b.build());
}

std::string Client::leadsFacets(const LeadFilters& filters) {
    std::string body = JsonBuilder().raw("filters", encode_filters(filters)).build();
    return post(infinity_url_ + kSalesShift + "/leads/facets", body);
}

std::string Client::leadsQuota() {
    return get(infinity_url_ + kSalesShift + "/leads/quota");
}

std::string Client::leadsReveal(const std::string& pool_person_id) {
    // Spends one reveal unless this org already holds the row. A 402 here means
    // the allowance is gone AND that nothing was charged for the attempt; a 410
    // means the person was erased, which is terminal rather than transient.
    std::string body = JsonBuilder().str("pool_person_id", pool_person_id).build();
    return post(infinity_url_ + kSalesShift + "/leads/reveal", body);
}

std::string Client::leadsSave(const std::vector<std::string>& pool_person_ids) {
    require_ids("leads.save", pool_person_ids, 200);
    std::string body = JsonBuilder().strings("pool_person_ids", pool_person_ids).build();
    // Up to 200 snapshot rows in one transaction — more headroom than the
    // 30s default gives a batch write.
    return post(infinity_url_ + kSalesShift + "/leads/save", body, {}, kLongTimeout);
}

std::string Client::leadsPoolPerson(const std::string& pool_id) {
    return get(infinity_url_ + kSalesShift + "/leads/pool/" + pool_id);
}

std::string Client::leadsCompany(const std::string& company_id) {
    return get(infinity_url_ + kSalesShift + "/leads/company/" + company_id);
}

std::string Client::leadsList(const std::string& status, int limit) {
    // status is a slug (new|contacted|converted|…) and limit an int, so they go
    // into the query string as-is — the same shortcut salesshift_emails takes.
    // This SDK carries no percent-encoder.
    std::string url = infinity_url_ + kSalesShift + "/leads";
    std::string sep = "?";
    if (!status.empty()) { url += sep + "status=" + status; sep = "&"; }
    if (limit > 0)       { url += sep + "limit=" + std::to_string(limit); }
    return get(url);
}

std::string Client::leadsGet(const std::string& lead_id) {
    return get(infinity_url_ + kSalesShift + "/leads/" + lead_id);
}

std::string Client::leadsUpdate(const std::string& lead_id, const LeadUpdate& patch) {
    JsonBuilder b;
    if (patch.status)            b.str("status", *patch.status);
    if (patch.score)             b.num("score", *patch.score);
    if (patch.notes)             b.str("notes", *patch.notes);
    if (patch.disqualify_reason) b.str("disqualify_reason", *patch.disqualify_reason);
    if (patch.owner_id)          b.str("owner_id", *patch.owner_id);
    // strings(), not strings_if(): an engaged-but-empty `tags` means "clear the
    // tags", and dropping it would turn an intentional clear into a no-op.
    if (patch.tags)              b.strings("tags", *patch.tags);
    // PATCH has no public verb on this client; the transport already speaks it.
    return do_request("PATCH", infinity_url_ + kSalesShift + "/leads/" + lead_id,
                      b.build(), {}, kDefaultTimeout).body;
}

std::string Client::leadsConvert(const std::string& lead_id,
                                 const std::string& lifecycle_stage) {
    std::string body = JsonBuilder().str_if("lifecycle_stage", lifecycle_stage).build();
    return post(infinity_url_ + kSalesShift + "/leads/" + lead_id + "/convert", body,
                {}, kLongTimeout);
}

std::string Client::leadsBulkConvert(const std::vector<std::string>& lead_ids) {
    // No server-side ceiling on this one (it converts records this org already
    // holds and reveals nothing), so only the empty case is refused here.
    require_ids("leads.bulkConvert", lead_ids, 0);
    std::string body = JsonBuilder().strings("lead_ids", lead_ids).build();
    return post(infinity_url_ + kSalesShift + "/leads/bulk-convert", body,
                {}, kLongTimeout);
}

std::string Client::leadsConvertFromPool(const std::vector<std::string>& pool_person_ids,
                                         bool reveal_if_needed,
                                         const std::string& lifecycle_stage) {
    require_ids("leads.convertFromPool", pool_person_ids, 200);
    std::string body = JsonBuilder()
        .strings("pool_person_ids", pool_person_ids)
        // true SPENDS QUOTA — one reveal per row not already un-masked. false
        // converts only what is already revealed and reports the remainder as
        // skipped_no_quota, spending nothing.
        .boolean("reveal_if_needed", reveal_if_needed)
        .str_if("lifecycle_stage", lifecycle_stage)
        .build();
    // The caller must render converted / already_converted / skipped_no_quota /
    // skipped_no_email / skipped_erased from the reply. They add up to the ids
    // sent; showing only `converted` hides a partial spend.
    return post(infinity_url_ + kSalesShift + "/leads/convert-from-pool", body,
                {}, kLongTimeout);
}

std::string Client::leadsErasure(const std::string& email, const std::string& reason,
                                 const std::string& note) {
    // GLOBAL and IRREVERSIBLE — this deactivates the person in the shared pool
    // and strips them from every tenant's saved leads, not just this one's.
    // Checked here so an empty address cannot reach the endpoint by accident.
    if (email.empty())
        throw VxError("leads.erasure",
                      "email required — erasure is global and irreversible, so it is "
                      "never issued against an empty address", 400);
    std::string body = JsonBuilder()
        .str("email", email)
        .str_if("reason", reason)
        .str_if("note", note)
        .build();
    return post(infinity_url_ + kSalesShift + "/leads/erasure", body, {}, kLongTimeout);
}

std::string Client::leadsSavedSearches() {
    return get(infinity_url_ + kSalesShift + "/lead-searches");
}

std::string Client::leadsEnrich(const std::string& domain,
                                const std::string& company_id) {
    // str_if, not str: an empty "domain" is a constraint that matches nothing,
    // and the server wants the field absent instead.
    std::string body = JsonBuilder()
        .str_if("company_id", company_id)
        .str_if("domain", domain)
        .build();
    return post(infinity_url_ + kSalesShift + "/leads/enrich", body);
}

std::string Client::leadsSaveSearch(const std::string& name, const LeadFilters& filters,
                                    bool is_shared) {
    std::string body = JsonBuilder()
        .str("name", name)
        // Stored verbatim and handed back by leadsSavedSearches, so it is
        // encoded exactly as leadsSearch encodes it — a saved search must
        // replay as the search that was saved.
        .raw("filters", encode_filters(filters))
        .boolean("is_shared", is_shared)
        .build();
    return post(infinity_url_ + kSalesShift + "/lead-searches", body);
}

}  // namespace vx
