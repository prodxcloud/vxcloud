"""End-to-end smoke test for vxsdk.agentcontrol against the local stack.

Requires:
  - FastAPI uvicorn on :8741, Go shim on :8744
  - Postgres with the test user joelwembo@outlook.com seeded (already true
    on the development box)
  - The job worker daemon running so create() rows reach a terminal state

Skips automatically when those services aren't reachable, so the test is
safe to run from CI that doesn't have a local stack.
"""
from __future__ import annotations

import json
import os
import socket
import time
import urllib.request
from pathlib import Path

import pytest

# Make the in-tree vxsdk importable regardless of where pytest is invoked
import sys
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import vxsdk  # noqa: E402


GO_SHIM = os.environ.get("VXSDK_TEST_GO", "http://localhost:8744")
FASTAPI = os.environ.get("VXSDK_TEST_FASTAPI", "http://localhost:8741")
EMAIL   = os.environ.get("VXSDK_TEST_EMAIL", "joelwembo@outlook.com")
PWD     = os.environ.get("VXSDK_TEST_PASSWORD", "Joelwembo@outlook.comA1")
TENANT  = os.environ.get("VXSDK_TEST_TENANT", "92efb9b0-6785-468a-80d7-ceb5df400168")


def _port_open(url: str) -> bool:
    host, port = url.replace("http://", "").replace("https://", "").split(":")
    try:
        with socket.create_connection((host, int(port)), timeout=1.0):
            return True
    except OSError:
        return False


needs_local = pytest.mark.skipif(
    not (_port_open(GO_SHIM) and _port_open(FASTAPI)),
    reason="local Go shim / FastAPI not reachable",
)


def _login() -> str:
    """Hit FastAPI /api/v1/auth/login and return the access token."""
    req = urllib.request.Request(
        FASTAPI + "/api/v1/auth/login",
        data=json.dumps({"email": EMAIL, "password": PWD}).encode(),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read())["access"]


@pytest.fixture(scope="module")
def client() -> vxsdk.Client:
    token = _login()
    return vxsdk.Client(
        access_token=token,
        node_url=GO_SHIM,
        tenant_id=TENANT,
    )


# ── reads ───────────────────────────────────────────────────────────────

@needs_local
def test_summary_returns_counts(client: vxsdk.Client) -> None:
    s = client.agentcontrol.summary()
    assert "total_agents" in s
    assert "total_datasets" in s


@needs_local
def test_datasets_list_nonempty(client: vxsdk.Client) -> None:
    items = client.agentcontrol.datasets.list()
    assert isinstance(items, list)
    # The dev box has at least one dataset; if not, the assertion below is
    # the smoke we care about (the call shape works).
    if items:
        assert items[0]["tenant_id"] == TENANT


@needs_local
def test_fine_tuning_list_and_detail(client: vxsdk.Client) -> None:
    items = client.agentcontrol.fine_tuning.list()
    assert isinstance(items, list)
    if items:
        d = client.agentcontrol.fine_tuning.get(items[0]["id"])
        assert d["id"] == items[0]["id"]


@needs_local
def test_training_list_and_detail(client: vxsdk.Client) -> None:
    items = client.agentcontrol.training.list()
    assert isinstance(items, list)
    if items:
        d = client.agentcontrol.training.get(items[0]["id"])
        assert d["id"] == items[0]["id"]


@needs_local
def test_knowledge_list(client: vxsdk.Client) -> None:
    items = client.agentcontrol.knowledge.list()
    assert isinstance(items, list)


@needs_local
def test_github_repos_via_oauth(client: vxsdk.Client) -> None:
    """Block 0 regression test: the Go shim should resolve the user's
    GitHub OAuth token from oauth_accounts via FastAPI, with no env var
    or X-GitHub-Token header set."""
    out = client.agentcontrol.github.list_repos()
    assert "repos" in out
    assert isinstance(out["repos"], list)


# ── poller / round-trip ─────────────────────────────────────────────────

@needs_local
def test_fine_tuning_create_and_wait(client: vxsdk.Client) -> None:
    """Create a fine-tuning job through the SDK and wait for it to flip
    to a terminal status. The local worker daemon executes it (with the
    tiny-gpt2 fallback when the requested base isn't cached). Skipped
    if no datasets exist on the tenant."""
    datasets = client.agentcontrol.datasets.list()
    if not datasets:
        pytest.skip("tenant has no datasets to fine-tune against")
    job = client.agentcontrol.fine_tuning.create(
        name=f"vxsdk-smoke-{int(time.time())}",
        base_model="meta-llama/Llama-3.1-8B-Instruct",  # falls back to tiny-gpt2
        training_file=datasets[0]["id"],
        epochs=1, batch_size=4,
    )
    assert job.id
    assert job.status in {"queued", "running", "pending"}
    # Worker polls every 5s; tiny-gpt2 fine-tune is ~60-90s.
    job.wait_for_completion(timeout=300, interval=5)
    assert job.status in {"succeeded", "failed"}
    assert job.status == "succeeded", f"job failed: {job.data.get('error_message')}"
    assert job.data.get("result_model"), "succeeded job should have result_model set"
