#!/usr/bin/env python3
"""Async demo: fan out three concurrent deploys with vxsdk_async.

Drops three separate redis containers onto inst3 in parallel via
asyncio.gather(). Total wall clock ≈ time of the slowest deploy
(usually 15-30 s) instead of 3× that.

  python3 deploy_async.py
"""

from __future__ import annotations

import asyncio
import sys
import time

import vxsdk_async as vx


HOST = "44.220.145.108"
KEY = "AWSPRODKEY1.PEM"


async def deploy_one(c: vx.AsyncClient, *, name: str, port: int) -> dict:
    return await c.deploy.container(
        host=HOST, ssh_user="ubuntu", key_pair_name=KEY,
        image="redis:7-alpine",
        name=name,
        ports=[f"{port}:6379"],
        restart_policy="unless-stopped",
        timeout=600,
    )


async def main() -> int:
    async with await vx.AsyncClient.load_from_vxcli() as c:
        print(f"  authenticated: {c.username!r}")
        print(f"  node:          {c.node_url}\n")

        targets = [
            ("py-async-redis-1", 6391),
            ("py-async-redis-2", 6392),
            ("py-async-redis-3", 6393),
        ]
        print("→ launching 3 redis container deploys in parallel…")
        t0 = time.time()
        results = await asyncio.gather(
            *(deploy_one(c, name=n, port=p) for n, p in targets),
            return_exceptions=True,
        )
        wall = time.time() - t0
        print(f"\n✓ wall clock: {wall:.1f}s\n")

        for (name, port), r in zip(targets, results):
            if isinstance(r, Exception):
                print(f"  × {name:24}  {type(r).__name__}: {r}")
                continue
            print(f"  ✓ {name:24}  port=:{port}  session={r.get('session_id', '')[:8]}…  "
                  f"status={r.get('status')}  exec={r.get('execution_time')}")

        return 0 if all(not isinstance(r, Exception) for r in results) else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
