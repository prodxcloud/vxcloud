#!/usr/bin/env python3
"""Credential-verification harness for the Metal DB provisioning API.

Goal (per the dashboard bug report): prove that the Metal DB wizard path
sends the *right credentials* to the node, end to end, via the Python SDK.

What it does:
  1. Builds a vxsdk.Client from the dev API key + username.
  2. Exchanges the API key for a JWT (auth sanity check).
  3. Resolves the tenant node URL exactly like the web app does.
  4. Wraps the SDK's HTTP layer to CAPTURE and PRINT the exact request
     (URL, headers, body) that goes to:
        - /api/v2/tenant/provision/metaldb/test-connection  (multipart)
        - /api/v2/tenant/provision/metaldb                  (JSON)
     so you can eyeball that hostname / ssh_username / key_pair_name /
     username / organization / db creds are all present and correct.
  5. Runs test-connection against each provided test VM and prints the
     node's verdict. Optionally runs a real provision with --provision.

Usage:
    python3 test_metaldb_credentials.py                 # dry+test-connection
    python3 test_metaldb_credentials.py --provision     # also provision VM #1
    python3 test_metaldb_credentials.py --vm 2          # target VM #2 only

Nothing here hard-codes a private key: the node fetches the SSH key from
the workspace vault by key_pair_name (here: "VPS1").
"""
from __future__ import annotations

import argparse
import json
import sys

import vxsdk

API_KEY = "xc_dev_sRlbQPC8pkP924m1_Xbg1Dpn0kHNMTau-nyNuwCEiXAehBu7NQyJoQ"
USERNAME = "joelwembo"  # platform username (NOT the login email)

# Testing-ground VMs (keypair lives in the workspace vault as "VPS1").
VMS = [
    {"host": "139.99.99.155", "ssh_user": "ubuntu", "key_pair_name": "VPS1"},
    {"host": "34.143.175.212", "ssh_user": "root", "key_pair_name": "VPS1"},
    {"host": "136.110.49.202", "ssh_user": "root", "key_pair_name": "VPS1"},
]

SECRET_KEYS = {"database_password", "postgres_password"}
SECRET_HEADERS = {"authorization", "x-api-key"}


def _redact_headers(h: dict) -> dict:
    out = {}
    for k, v in h.items():
        if k.lower() in SECRET_HEADERS and v:
            out[k] = v[:14] + "…(redacted)"
        else:
            out[k] = v
    return out


def _redact_body(body: dict) -> dict:
    out = {}
    for k, v in body.items():
        out[k] = "••••(set)" if k in SECRET_KEYS and v else v
    return out


def install_request_capture(client: vxsdk.Client) -> None:
    """Monkeypatch the low-level transport to print every outbound request.

    This is the actual "test API that we are sending the right credentials":
    it shows the wire payload, not just what we intended to send.
    """
    orig_do = client._do

    def traced_do(method, url, *, op, headers, body, timeout, target="node"):
        if "metaldb" in url:
            print("\n" + "=" * 78)
            print(f"OUTBOUND  {method} {url}")
            print(f"  op={op}  target={target}  timeout={timeout}s")
            # Headers are merged with auth headers inside _do; show what the
            # SDK is about to add plus the auth header it will attach.
            merged = dict(headers)
            merged.update(client._auth_headers())
            print("  headers:")
            for k, v in _redact_headers(merged).items():
                print(f"    {k}: {v}")
            ct = headers.get("Content-Type", "")
            print(f"  content-type: {ct or '(set by transport)'}")
            if body:
                if "application/json" in ct:
                    try:
                        parsed = json.loads(body.decode())
                        print("  json body (credentials being sent):")
                        print(json.dumps(_redact_body(parsed), indent=4, sort_keys=True))
                    except Exception:
                        print(f"  body: <{len(body)} bytes>")
                else:
                    # multipart — extract the form field names/values
                    text = body.decode("utf-8", "replace")
                    print("  multipart fields (credentials being sent):")
                    for part in text.split("--")[1:-1]:
                        if 'name="' in part:
                            name = part.split('name="', 1)[1].split('"', 1)[0]
                            val = part.split("\r\n\r\n", 1)[1].rsplit("\r\n", 1)[0] \
                                if "\r\n\r\n" in part else ""
                            if name in SECRET_KEYS and val:
                                val = "••••(set)"
                            print(f"    {name} = {val}")
            print("=" * 78)
        return orig_do(method, url, op=op, headers=headers, body=body,
                        timeout=timeout, target=target)

    client._do = traced_do  # type: ignore[method-assign]


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--provision", action="store_true",
                    help="actually provision PostgreSQL on the VM (real deploy)")
    ap.add_argument("--vm", type=int, default=0,
                    help="1-based VM index to target (default: all for test-conn)")
    args = ap.parse_args()

    print(f"vxsdk {vxsdk.__version__}")
    client = vxsdk.Client(api_key=API_KEY, username=USERNAME)
    install_request_capture(client)

    print("\n[1] Exchanging API key for JWT ...")
    try:
        client.authenticate()
    except vxsdk.VxError as e:
        print(f"  ✗ auth failed: {e}")
        return 1
    print(f"  ✓ authenticated as username={client.username!r} "
          f"org={client.whoami.organization!r}")

    print("\n[2] Resolving tenant node URL (same as web app) ...")
    try:
        node = client.ensure_node_url()
    except vxsdk.VxError as e:
        print(f"  ✗ node resolution failed: {e}")
        return 1
    print(f"  ✓ node_url = {node}")

    targets = VMS if args.vm == 0 else [VMS[args.vm - 1]]

    print("\n[3] test-connection (verifying SSH credentials reach the node) ...")
    reachable = []
    for vm in targets:
        print(f"\n--- VM {vm['host']} (ssh={vm['ssh_user']}, key={vm['key_pair_name']}) ---")
        try:
            res = client.metaldb.test_connection(
                vm["host"], vm["ssh_user"], vm["key_pair_name"])
            ok = res.get("success") or res.get("status") == "success"
            print(f"  RESULT success={ok}")
            print(f"  message: {res.get('message') or res.get('error')}")
            if ok:
                reachable.append(vm)
        except vxsdk.VxError as e:
            print(f"  ✗ request error: {e}")

    if args.provision:
        vm = (VMS[args.vm - 1] if args.vm else (reachable[0] if reachable else None))
        if not vm:
            print("\n[4] skipping provision — no reachable VM")
            return 1
        print(f"\n[4] PROVISIONING PostgreSQL on {vm['host']} ...")
        try:
            res = client.metaldb.provision(
                vm["host"], vm["ssh_user"], vm["key_pair_name"],
                database_name="appdb", database_user="appuser",
                database_password="Joelwembo2026!", postgres_password="Joelwembo2026!",
                postgres_version="16",
            )
            print("  RESPONSE:")
            print(json.dumps(res, indent=2)[:4000])
        except vxsdk.VxError as e:
            print(f"  ✗ provision error: {e}")
            return 1

    print("\nDONE.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
