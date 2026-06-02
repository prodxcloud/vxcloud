#!/usr/bin/env python3
"""Deploy an app to a vxcloud tenant VM using vxsdk-py.

Two modes:
  --mode container  (default)  → c.deploy.container(...)  → docker run on the VM
  --mode stack                 → c.deploy.stack(kind, ...) → git clone + build + nginx

Container mode is recommended for the preview demo — it's universally
supported by the platform. Stack mode hits server-side template bugs for
several stacks (python/nodejs/nextjs/static, and intermittently golang).

Reads credentials from ~/.vxcloud/credentials.json (the file `vxcli auth
login` writes); falls back to VXSDK_API_KEY env var.

Examples:
  python3 deploy_app.py
  python3 deploy_app.py --image redis:7 --name my-redis --ports 6380:6379
  python3 deploy_app.py --mode stack --kind golang \\
      --repo-url https://github.com/joelwembo/va-sample-golang.git
"""

from __future__ import annotations

import argparse
import os
import sys
import time
import urllib.error
import urllib.request

import vxsdk


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0],
                                formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("--mode", choices=("container", "stack"), default="container")

    # SSH target
    p.add_argument("--host", default=os.environ.get("TARGET_HOST", "44.220.145.108"))
    p.add_argument("--ssh-user", default=os.environ.get("SSH_USER", "ubuntu"))
    p.add_argument("--key-pair-name", default=os.environ.get("KEY_PAIR_NAME", "AWSPRODKEY1.PEM"))

    # Container mode
    p.add_argument("--image", default=os.environ.get("IMAGE", "traefik/whoami:latest"),
                   help="Docker image (default: %(default)s)")
    p.add_argument("--name", default=os.environ.get("NAME", "py-vxsdk-whoami"))
    p.add_argument("--ports", default=os.environ.get("PORTS", "8085:80"),
                   help='comma-separated "host:container" pairs (default: %(default)s)')
    p.add_argument("--env", action="append", default=[],
                   help="KEY=VALUE (repeatable)")
    p.add_argument("--restart", default="unless-stopped")

    # Stack mode
    p.add_argument("--kind", default="golang", help="stack kind for --mode stack")
    p.add_argument("--repo-url",
                   default="https://github.com/joelwembo/va-sample-golang.git")
    p.add_argument("--branch", default="main")
    p.add_argument("--app-name", default="py-deployed-app")
    p.add_argument("--http-port", default="8085")

    p.add_argument("--no-poll", action="store_true",
                   help="skip the post-deploy liveness check")
    return p.parse_args()


def make_client() -> vxsdk.Client:
    if os.environ.get("VXSDK_API_KEY"):
        return vxsdk.Client(
            api_key=os.environ["VXSDK_API_KEY"],
            username=os.environ.get("VXSDK_USERNAME", ""),
            node_url=os.environ.get("VXSDK_NODE_URL", ""),
        )
    return vxsdk.Client.load_from_vxcli()


def poll_url(url: str, attempts: int = 20, every: float = 4.0) -> int | None:
    last: int | None = None
    for _ in range(attempts):
        try:
            with urllib.request.urlopen(url, timeout=8) as resp:
                return resp.status
        except urllib.error.HTTPError as e:
            last = e.code
        except Exception:
            last = None
        time.sleep(every)
    return last


def deploy_container(c: vxsdk.Client, args: argparse.Namespace) -> dict:
    ports = [p.strip() for p in args.ports.split(",") if p.strip()]
    print(f"→ container: {args.image}  {ports}  on {args.host}")
    return c.deploy.container(
        host=args.host, ssh_user=args.ssh_user, key_pair_name=args.key_pair_name,
        image=args.image, name=args.name,
        ports=ports, env=args.env or None,
        restart_policy=args.restart,
        timeout=600,
    )


def deploy_stack(c: vxsdk.Client, args: argparse.Namespace) -> dict:
    print(f"→ stack:    {args.kind}  {args.repo_url}@{args.branch}  on {args.host}:{args.http_port}")
    return c.deploy.stack(
        args.kind,
        host=args.host, ssh_user=args.ssh_user, key_pair_name=args.key_pair_name,
        repo_url=args.repo_url, branch=args.branch,
        app_name=args.app_name, git_provider="github",
        http_port=args.http_port, timeout=900,
    )


def main() -> int:
    args = parse_args()
    print(f"  mode:  {args.mode}")
    print(f"  host:  {args.host}")
    print()

    try:
        c = make_client()
    except vxsdk.VxError as e:
        print(f"× auth: {e}", file=sys.stderr)
        return 2
    print(f"  authenticated: {c.username!r}")
    print(f"  node:          {c.node_url}")
    print()

    try:
        if args.mode == "container":
            result = deploy_container(c, args)
        else:
            result = deploy_stack(c, args)
    except vxsdk.VxAuthError as e:
        print(f"× auth rejected: {e}", file=sys.stderr); return 3
    except vxsdk.VxValidationError as e:
        print(f"× bad request: {e}", file=sys.stderr); return 4
    except vxsdk.VxError as e:
        print(f"× upstream error: {e}", file=sys.stderr); return 5

    print("\n✓ Deployment initiated")
    for k in ("session_id", "resource_name", "access_url", "status", "exit_code", "execution_time"):
        v = result.get(k)
        if v not in (None, "", 0):
            print(f"  {k:14} {v}")

    if args.no_poll:
        return 0

    # Decide what to poll.
    url = result.get("access_url")
    if not url:
        if args.mode == "container":
            host_port = args.ports.split(",")[0].split(":")[0]
            url = f"http://{args.host}:{host_port}/"
        else:
            url = f"http://{args.host}:{args.http_port}/"
    print(f"\n→ Polling {url}")
    code = poll_url(url, attempts=20, every=3.0)
    if code == 200:
        print(f"✓ {url} returned HTTP 200 — app is live")
        return 0
    print(f"× {url} did not return HTTP 200 (last status: {code})", file=sys.stderr)
    print(f"  ssh ubuntu@{args.host} 'docker ps && docker logs {args.name}'", file=sys.stderr)
    return 6


if __name__ == "__main__":
    sys.exit(main())
