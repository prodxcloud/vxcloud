// example.cpp — smoke test / usage example for the C++ vxcloud SDK.
//
// Credentials come from the environment so the binary never embeds secrets:
//   VX_API_KEY       xc_live_...      (optional — enables auth exchange)
//   VX_ACCESS_TOKEN  eyJ...           (optional — pre-obtained Bearer JWT)
//   VX_USERNAME      your username
//   VX_NODE_URL      https://node1.vxcloud.io   (optional; auto-resolved)
//   VX_TENANT_ID     <uuid>           (needed for agentcontrol.*)
//
// At minimum VX_NODE_URL lets the no-auth health() call succeed. With a token
// + tenant id it also exercises the AgentControl surface.

#include "vxsdk/vxsdk.hpp"

#include <cstdlib>
#include <iostream>
#include <string>

static std::string env(const char* k) {
    const char* v = std::getenv(k);
    return v ? std::string(v) : std::string();
}

static void show(const std::string& label, const std::string& json) {
    std::string head = json.substr(0, 240);
    std::cout << "  " << label << ": " << head
              << (json.size() > 240 ? " ..." : "") << "\n";
}

int main() {
    vx::ClientOptions o;
    o.api_key      = env("VX_API_KEY");
    o.access_token = env("VX_ACCESS_TOKEN");
    o.username     = env("VX_USERNAME");
    o.node_url     = env("VX_NODE_URL");
    o.tenant_id    = env("VX_TENANT_ID");
    if (o.api_key.empty() && o.access_token.empty()) {
        std::cerr << "set VX_API_KEY or VX_ACCESS_TOKEN (and VX_NODE_URL) first\n";
        return 2;
    }

    try {
        vx::Client c(std::move(o));
        std::cout << "vxsdk-cpp smoke test\n";
        std::cout << "  node_url: " << c.ensure_node_url() << "\n";

        show("health", c.health());  // no auth

        if (!env("VX_TENANT_ID").empty()) {
            // llm/providers authenticates with the developer API key; the
            // summary/agents/models routes require a full user JWT.
            show("agentcontrol.llm_providers", c.agentcontrol_llm_providers());
            try { show("agentcontrol.summary", c.agentcontrol_summary()); }
            catch (const vx::VxError& e) { std::cout << "  agentcontrol.summary: " << e.what() << "\n"; }
        } else {
            std::cout << "  (skipping agentcontrol.* — set VX_TENANT_ID)\n";
        }
    } catch (const vx::VxError& e) {
        std::cerr << "VxError: " << e.what()
                  << " [status=" << e.http_status() << "]\n";
        return 1;
    }
    std::cout << "OK\n";
    return 0;
}
