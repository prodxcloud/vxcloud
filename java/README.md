# vxsdk — Java

JDK 11+ SDK for the vxcloud platform. Same wire contract as the Go / Python /
TypeScript / C++ SDKs: API-key exchange, node discovery, and node ops
(`cicd`, `sessions`, `agentcontrol`, `health`). Built on
`java.net.http.HttpClient` with **zero runtime dependencies** — data methods
return the raw JSON body as a `String` so you can parse with Jackson/Gson/etc.

## Layout

```
java/
  src/main/java/io/vxcloud/sdk/
    VxClient.java      the client (builder-constructed)
    VxException.java   the single error type
    Json.java          tiny internal scalar-field extractor (package-private)
  examples/Example.java
  pom.xml
```

## Build

**Maven:**
```bash
mvn -f java package        # jar in target/vxsdk-<version>.jar
```

**Or straight javac** (no Maven needed):
```bash
cd java
javac -d out src/main/java/io/vxcloud/sdk/*.java examples/Example.java
java  -cp out Example       # runs the smoke test (reads VX_* env vars)
```

## Use

```java
import io.vxcloud.sdk.VxClient;
import io.vxcloud.sdk.VxException;

VxClient c = VxClient.builder()
        .apiKey("xc_live_…")                  // or .accessToken("eyJ…")
        .username("alice")
        .nodeUrl("https://node1.vxcloud.io")  // optional; auto-resolved otherwise
        .tenantId("<uuid>")                   // required for agentcontrol.*
        .build();

String health    = c.health();                     // no auth
String providers = c.agentcontrolLlmProviders();    // authenticated
String raw       = c.get("/api/v2/cicd/pipelines"); // generic verb

// training (mirrors python vxsdk Training)
String jobs   = c.agentcontrolTraining();           // GET training/
String job    = c.agentcontrolTrainingGet(id);      // GET training/{id}
String made   = c.agentcontrolTrainingCreate(       // POST training/
        "my-run", "meta-llama/Llama-3.2-1B-Instruct", datasetId, "fine-tuning", 1);

// fine-tuning (mirrors python vxsdk FineTuning)
String fts    = c.agentcontrolFineTuning();          // GET fine-tuning/
String ftJob  = c.agentcontrolFineTuningGet(id);     // GET fine-tuning/{id}
String ftMade = c.agentcontrolFineTuningCreate(      // POST fine-tuning/
        "my-ft", "sshleifer/tiny-gpt2", "file.jsonl", 1, 4, 5e-5);
```

All agentcontrol methods return the raw JSON `String`; poll a training/fine-tuning
job by reading its `status` field via `agentcontrolTrainingGet(id)` until it is
terminal (`completed`/`succeeded`/`failed`).

Errors throw `VxException` — inspect `.httpStatus()`, `.isAuth()`,
`.isRetryable()`, `.detail()`. The client sends `Authorization: Bearer` when
it holds an access token, sends `X-API-Key` otherwise (dropping it on
node-targeted requests that already carry a Bearer, matching the reference
SDKs), attaches `X-Tenant-ID` on agentcontrol calls, and does one lazy token
refresh on the first 401 for api-key clients.

## Credentials for the smoke test

`examples/Example.java` reads `VX_API_KEY` / `VX_ACCESS_TOKEN`, `VX_USERNAME`,
`VX_NODE_URL`, `VX_TENANT_ID` from the environment so nothing is embedded.

> Preview. API may change before v1.0.
