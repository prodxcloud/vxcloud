"""
vxcloud_async — brand-name alias for the `vxsdk_async` Python SDK.

Pure re-export of `vxsdk_async`. Installed automatically when you do:

    pip install vxcloud[async]

Usage mirrors vxsdk_async exactly:

    import vxcloud_async
    async with await vxcloud_async.AsyncClient.load_from_vxcli() as c:
        sess = await c.deploy.fastapi(...)

    # Or via the brand aliases:
    async with await vxcloud_async.VxCloud.load_from_vxcli() as c: ...
    async with await vxcloud_async.vxcloud.load_from_vxcli() as c: ...
"""

from __future__ import annotations

from vxsdk_async import *  # noqa: F401,F403

from vxsdk_async import (  # noqa: F401
    AsyncClient,
    VxCloud,
    vxcloud,
)
