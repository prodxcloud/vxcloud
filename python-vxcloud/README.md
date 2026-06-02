# vxcloud (Python)

Brand-name alias for the [`vxsdk`](https://pypi.org/project/vxsdk/) Python SDK.
This package re-exports everything from `vxsdk` so you can reach the same
surface under the brand name.

```bash
pip install vxcloud           # sync, stdlib-only
pip install vxcloud[async]    # adds httpx + vxsdk_async re-export
```

```python
import vxcloud

c = vxcloud.Client.load_from_vxcli()      # canonical
c = vxcloud.VxCloud.load_from_vxcli()     # PascalCase brand (matches TS SDK)
c = vxcloud.vxcloud.load_from_vxcli()     # lowercase brand
c = vxcloud.load_from_vxcli()             # module-level convenience

vm = c.cloud.create_vm(
    name="api-vm", cloud="aws", region="us-east-1",
    instance_type="t3.small", key_pair_name="AWSPRODKEY2",
)
```

All four entry styles return the same `vxsdk.Client` instance. There is no
behavior difference between `import vxcloud` and `import vxsdk` — pick the
name you and your team prefer.

The `vxcloud` release version is pinned to the matching `vxsdk` release; see
[CHANGELOG.md](./CHANGELOG.md). For the full SDK reference, see the upstream
[`vxsdk` README](https://github.com/vxcloud/platform/blob/main/services/sdk/python/README.md).
