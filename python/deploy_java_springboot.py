#!/usr/bin/env python3
"""Deploy the vxcli java + springboot demo apps via the Python SDK.

Run after `vxcli auth login` so credentials live at ~/.vxcloud/credentials.json.

Examples:
  python3 deploy_java_springboot.py --node-url http://localhost:8801
  python3 deploy_java_springboot.py --kind springboot
"""
from __future__ import annotations

import argparse
import json
import os
import sys

import vxsdk


REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "..", ".."))
APP_DIRS = {
    "java":       os.path.join(REPO_ROOT, "shared", "development", "java"),
    "springboot": os.path.join(REPO_ROOT, "shared", "development", "springboot"),
}


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--kind", choices=["java", "springboot", "both"], default="both")
    p.add_argument("--host", default="54.234.195.74", help="EC2 public IP")
    p.add_argument("--ssh-user", default="ubuntu")
    p.add_argument("--key-pair-name", default="vxcli-test-ec2")
    p.add_argument("--key-pair-pem", default=os.path.join(
        REPO_ROOT, "generated", "06a0043b-f5b3-4578-9f33-46f7dc7dfa66",
        "files", "vxcli-test-ec2.pem"))
    p.add_argument("--java-version", default="17")
    p.add_argument("--app-port", default="8080")
    p.add_argument("--app-name-suffix", default="-sdk",
                   help="Suffix added to app names so SDK runs don't collide with vxcli runs")
    p.add_argument("--node-url", default=None,
                   help="Override tenant node URL (default: from credentials)")
    args = p.parse_args()

    client = vxsdk.Client.load_from_vxcli()
    if args.node_url:
        client.node_url = args.node_url
    print(f"[sdk] node_url = {client.node_url}")

    kinds = ["java", "springboot"] if args.kind == "both" else [args.kind]

    for kind in kinds:
        path = APP_DIRS[kind]
        print(f"\n[sdk] deploying {kind} from {path} ...")
        try:
            result = client.deploy.stack(
                kind,
                host=args.host,
                ssh_user=args.ssh_user,
                key_pair_name=args.key_pair_name,
                key_pair_pem_path=args.key_pair_pem,
                path=path,
                app_name=f"vxcli-{kind}-hello{args.app_name_suffix}",
                app_port=args.app_port,
                java_version=args.java_version,
                build_tool="maven",
            )
            print(f"[sdk] {kind} response status={result.get('status')!r}"
                  f" session_id={result.get('session_id')!r}")
            if result.get("outputs"):
                print(f"[sdk] outputs: {json.dumps(result['outputs'], indent=2)[:500]}")
        except vxsdk.VxError as e:
            print(f"[sdk] {kind} FAILED: {e}", file=sys.stderr)
        except Exception as e:
            print(f"[sdk] {kind} EXCEPTION: {e}", file=sys.stderr)

    return 0


if __name__ == "__main__":
    sys.exit(main())
