"""
vxcloud — brand-name alias for the `vxsdk` Python SDK.

This module is a pure re-export of `vxsdk`. Every public name available
as `vxsdk.X` is also available as `vxcloud.X`, including the entry-point
class and its lowercase / PascalCase brand aliases:

    import vxcloud
    c = vxcloud.Client.load_from_vxcli()    # canonical
    c = vxcloud.VxCloud.load_from_vxcli()   # PascalCase brand
    c = vxcloud.vxcloud.load_from_vxcli()   # lowercase brand
    c = vxcloud.load_from_vxcli()           # module-level convenience

There is no logic here — `pip install vxcloud` pulls in the pinned
`vxsdk` release, and this file forwards everything to it. If you want
the async client, install with the extra:

    pip install vxcloud[async]
    import vxcloud_async
    async with await vxcloud_async.AsyncClient.load_from_vxcli() as c: ...
"""

from __future__ import annotations

# Star-import re-exports every public name from vxsdk: Client, VxCloud,
# vxcloud, all VxError subclasses, all resource classes, version, etc.
# Per https://docs.python.org/3/reference/simple_stmts.html#the-import-statement
# this honors vxsdk's `__all__` if defined, otherwise all non-underscore names.
from vxsdk import *  # noqa: F401,F403

# vxsdk does not define __all__, so star-import already covers everything,
# but we explicitly re-export the entry class names so static analyzers
# (mypy, pyright) see them on the `vxcloud` module surface.
from vxsdk import (  # noqa: F401
    Client,
    VxCloud,
    vxcloud,
    VxError,
    VxAuthError,
    VxValidationError,
    VxNotFoundError,
    VxRateLimitError,
    VxServerError,
    VxNetworkError,
    __version__ as _vxsdk_version,
)

# Module-level convenience: `vxcloud.load_from_vxcli()` mirrors the way
# `vxsdk.Client.load_from_vxcli()` reads `~/.vxcloud/credentials.json`.
load_from_vxcli = Client.load_from_vxcli

# Track the underlying vxsdk pin for diagnostics. The vxcloud shim version
# is set in pyproject.toml; this exposes the upstream vxsdk version too.
__vxsdk_version__ = _vxsdk_version
