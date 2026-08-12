// Example.java — smoke test / usage example for the Java vxcloud SDK.
//
// Credentials come from the environment so nothing is embedded:
//   VX_API_KEY, VX_ACCESS_TOKEN, VX_USERNAME, VX_NODE_URL, VX_TENANT_ID
//
// Compile + run (from services/sdk/java):
//   javac -d out src/main/java/io/vxcloud/sdk/*.java examples/Example.java
//   java  -cp out Example

import io.vxcloud.sdk.VxClient;
import io.vxcloud.sdk.VxException;

public class Example {
    static String env(String k) {
        String v = System.getenv(k);
        return v == null ? "" : v;
    }

    static void show(String label, String json) {
        String head = json.length() > 240 ? json.substring(0, 240) + " ..." : json;
        System.out.println("  " + label + ": " + head);
    }

    public static void main(String[] args) {
        String apiKey = env("VX_API_KEY");
        String token  = env("VX_ACCESS_TOKEN");
        if (apiKey.isEmpty() && token.isEmpty()) {
            System.err.println("set VX_API_KEY or VX_ACCESS_TOKEN (and VX_NODE_URL) first");
            System.exit(2);
        }
        try {
            VxClient c = VxClient.builder()
                    .apiKey(apiKey)
                    .accessToken(token)
                    .username(env("VX_USERNAME"))
                    .nodeUrl(env("VX_NODE_URL"))
                    .tenantId(env("VX_TENANT_ID"))
                    .build();

            System.out.println("vxsdk-java smoke test");
            System.out.println("  node_url: " + c.ensureNodeUrl());
            show("health", c.health());

            if (!env("VX_TENANT_ID").isEmpty()) {
                // llm/providers authenticates with the developer API key; the
                // summary/agents/models routes require a full user JWT.
                show("agentcontrol.llm_providers", c.agentcontrolLlmProviders());
                try { show("agentcontrol.summary", c.agentcontrolSummary()); }
                catch (VxException e) { System.out.println("  agentcontrol.summary: " + e.getMessage()); }
            } else {
                System.out.println("  (skipping agentcontrol.* — set VX_TENANT_ID)");
            }
            System.out.println("OK");
        } catch (VxException e) {
            System.err.println("VxException: " + e.getMessage() + " [status=" + e.httpStatus() + "]");
            System.exit(1);
        }
    }
}
