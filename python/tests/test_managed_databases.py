from __future__ import annotations

import sys
from pathlib import Path

import pytest

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

import vxsdk  # noqa: E402


class FakeClient:
    username = "tester@example.com"
    node_url = "https://node.example"

    def __init__(self) -> None:
        self.calls: list[dict[str, object]] = []

    def _json(self, method: str, url: str, *, op: str, json_body=None, timeout=None):
        self.calls.append({
            "method": method,
            "url": url,
            "op": op,
            "json_body": json_body,
            "timeout": timeout,
        })
        return {"ok": True, "body": json_body}


def test_vxsdk_create_rds_uses_managed_database_endpoint() -> None:
    fake = FakeClient()
    result = vxsdk.Cloud(fake).create_rds("sdk-rds", password="test-password")
    call = fake.calls[-1]
    body = call["json_body"]

    assert call["method"] == "POST"
    assert call["url"] == "https://node.example/api/v2/tenant/provision/databases"
    assert body["resource_type"] == "rds"
    assert body["resource_name"] == "sdk-rds"
    assert body["configuration"]["engine"] == "mysql"
    assert body["configuration"]["storage_size"] == 20
    assert body["configuration"]["password"] == "test-password"
    assert result["ok"] is True


def test_vxsdk_create_aurora_requires_and_sends_subnets() -> None:
    fake = FakeClient()
    with pytest.raises(ValueError):
        vxsdk.Cloud(fake).create_aurora("sdk-aurora")

    vxsdk.Cloud(fake).create_aurora(
        "sdk-aurora",
        engine="postgres",
        subnet_ids=["subnet-a", "subnet-b"],
        allowed_security_group_ids=["sg-123"],
    )
    body = fake.calls[-1]["json_body"]
    assert body["resource_type"] == "aurora"
    assert body["configuration"]["port"] == 5432
    assert body["configuration"]["subnet_ids"] == ["subnet-a", "subnet-b"]
    assert body["configuration"]["allowed_security_group_ids"] == ["sg-123"]


def test_vxsdk_create_redis_requires_network_ids() -> None:
    fake = FakeClient()
    with pytest.raises(ValueError):
        vxsdk.Cloud(fake).create_redis("sdk-redis")

    vxsdk.Cloud(fake).create_redis(
        "sdk-redis",
        subnet_ids=["subnet-a", "subnet-b"],
        vpc_security_group_ids=["sg-123"],
    )
    body = fake.calls[-1]["json_body"]
    assert body["resource_type"] == "redis"
    assert body["configuration"]["node_type"] == "cache.t3.micro"
    assert body["configuration"]["vpc_security_group_ids"] == ["sg-123"]
