# vxsdk — C++

C++17 SDK for the vxcloud platform. Same wire contract as the Go / Python /
TypeScript / Java SDKs: API-key exchange, node discovery, and node ops
(`cicd`, `sessions`, `agentcontrol`, `health`). The only link dependency is
**libcurl**; there is no JSON dependency — data methods return the raw JSON
response as a `std::string` so you can plug in nlohmann/json, RapidJSON, etc.

## Layout

```
cpp/
  include/vxsdk/vxsdk.hpp   public header (vx::Client, vx::VxError)
  src/vxsdk.cpp             libcurl implementation
  examples/example.cpp      smoke test / usage
  CMakeLists.txt
```

## Build

**CMake** (finds libcurl via `find_package(CURL)`):
```bash
cmake -S . -B build
cmake --build build
./build/vxsdk_example        # runs the smoke test (reads VX_* env vars)
```

**Or straight g++** (Debian/Ubuntu: `apt-get install libcurl4-openssl-dev`):
```bash
g++ -std=c++17 -O2 -Iinclude -c src/vxsdk.cpp -o vxsdk.o
g++ -std=c++17 -O2 -Iinclude examples/example.cpp vxsdk.o -lcurl -o vxsdk_example
```

## Use

```cpp
#include "vxsdk/vxsdk.hpp"

vx::ClientOptions o;
o.api_key   = "xc_live_…";                 // or o.access_token = "eyJ…";
o.username  = "alice";
o.node_url  = "https://node1.vxcloud.io";  // optional; auto-resolved otherwise
o.tenant_id = "<uuid>";                    // required for agentcontrol.*

vx::Client c(std::move(o));
std::string health    = c.health();                     // no auth
std::string providers = c.agentcontrol_llm_providers(); // authenticated
std::string raw       = c.get("/api/v2/cicd/pipelines"); // generic verb
```

Errors throw `vx::VxError` — inspect `.http_status()`, `.is_auth()`,
`.is_retryable()`, `.detail()`. The client sends `Authorization: Bearer` when
it holds an access token, sends `X-API-Key` otherwise (dropping it on
node-targeted requests that already carry a Bearer, matching the reference
SDKs), attaches `X-Tenant-ID` on agentcontrol calls, and does one lazy
token refresh on the first 401 for api-key clients.

## Credentials for the smoke test

`examples/example.cpp` reads `VX_API_KEY` / `VX_ACCESS_TOKEN`, `VX_USERNAME`,
`VX_NODE_URL`, `VX_TENANT_ID` from the environment so nothing is embedded.

> Preview. API may change before v1.0.
