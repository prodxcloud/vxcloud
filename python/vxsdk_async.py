"""vxsdk async — asyncio + httpx variant of vxsdk.Client.

Mirrors the sync ``vxsdk`` API one-for-one. Use this when you need to
fan out concurrent operations (multi-host deploys, batch installs,
parallel marketplace lookups) or when embedding the SDK in an async
service (FastAPI, aiohttp, asyncio worker, etc.).

Requires ``httpx`` (`pip install httpx`).

Example:

    import asyncio, vxsdk_async as vx

    async def main():
        async with await vx.AsyncClient.load_from_vxcli() as c:
            # concurrent: deploy three redis containers in parallel
            results = await asyncio.gather(
                c.deploy.container(host=H, ssh_user="ubuntu",
                                   key_pair_name=KEY, image="redis:7",
                                   name="r1", ports=["6381:6379"]),
                c.deploy.container(host=H, ssh_user="ubuntu",
                                   key_pair_name=KEY, image="redis:7",
                                   name="r2", ports=["6382:6379"]),
                c.deploy.container(host=H, ssh_user="ubuntu",
                                   key_pair_name=KEY, image="redis:7",
                                   name="r3", ports=["6383:6379"]),
            )

    asyncio.run(main())

The class hierarchy and method signatures match ``vxsdk`` (sync) exactly,
so a sync codebase migrating to async only needs to swap
``vxsdk.Client`` for ``vxsdk_async.AsyncClient`` and add ``async``/``await``
in the obvious places.
"""

from __future__ import annotations

import asyncio
import json
import urllib.parse
import uuid
from typing import Any, AsyncIterator, Iterable

import httpx

from vxsdk import (
    DEFAULT_INFINITY_URL, DEFAULT_LONG_TIMEOUT, DEFAULT_TIMEOUT,
    STACK_TARGETS, VxAuthError, VxError, VxNetworkError, VxValidationError,
    Whoami, _from_http, _http_reason, _is_retryable, _load_credentials_file,
    _multipart_body, __version__,
    # Leads errors live in vxsdk so BOTH flavors raise the SAME class —
    # re-declaring them here would mean `except VxLeadQuotaExhaustedError`
    # written against the sync client silently fails to catch the async one.
    VxLeadQuotaExhaustedError, VxLeadErasedError,
)

__all__ = [
    "AsyncClient", "VxCloud", "vxcloud",
    "VxLeadQuotaExhaustedError", "VxLeadErasedError",
]


#: The literal the server masks with (``j•••@acme.com``). Three U+2022 BULLET
#: characters, written as escapes so the comparison cannot be broken by a
#: file-encoding accident somewhere in the toolchain.
_EMAIL_MASK_MARK = "•••"


# ── Resource modules ───────────────────────────────────────────────────

class _AsyncResource:
    def __init__(self, client: "AsyncClient"):
        self.client = client


class _AsyncPipelines(_AsyncResource):
    async def list(self) -> list[dict[str, Any]]:
        body = await self.client._json("GET", self.client.node_url + "/api/v2/cicd/pipelines",
                                       op="cicd.pipelines.list")
        return body.get("data", [])

    async def show(self, pipeline_id: str) -> dict[str, Any]:
        body = await self.client._json("GET",
            f"{self.client.node_url}/api/v2/cicd/pipelines/{pipeline_id}",
            op="cicd.pipelines.show")
        return body.get("data", body)

    async def trigger(self, pipeline_id: str, branch: str = "main") -> dict[str, Any]:
        body = await self.client._json("POST",
            f"{self.client.node_url}/api/v2/cicd/pipelines/{pipeline_id}/trigger",
            op="cicd.pipelines.trigger", json_body={"branch": branch})
        return body.get("data", body)


class _AsyncBuilds(_AsyncResource):
    async def show(self, build_id: str) -> dict[str, Any]:
        body = await self.client._json("GET",
            f"{self.client.node_url}/api/v2/cicd/builds/{build_id}",
            op="cicd.builds.show")
        return body.get("data", body)


class _AsyncCICD(_AsyncResource):
    @property
    def pipelines(self) -> _AsyncPipelines:
        return _AsyncPipelines(self.client)

    @property
    def builds(self) -> _AsyncBuilds:
        return _AsyncBuilds(self.client)


class _AsyncSessions(_AsyncResource):
    async def list(self) -> list[dict[str, Any]]:
        query = urllib.parse.urlencode({"username": self.client.username})
        body = await self.client._json("GET",
            self.client.node_url + f"/api/v2/tenant/sessions?{query}",
            op="sessions.list")
        if isinstance(body, list):
            return body
        return body.get("sessions") or body.get("files") or []


class _AsyncInstall(_AsyncResource):
    async def script(
        self, *, host: str, ssh_user: str, key_pair_name: str,
        script: bytes | str, script_name: str = "install.sh",
        args: Iterable[str] | None = None, env: Iterable[str] | None = None,
        workspace_user: str | None = None, organization: str | None = None,
        timeout: float = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        if isinstance(script, str):
            script = script.encode("utf-8")
        if not script:
            raise ValueError("install.script: script is empty")
        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)
        fields["mode"] = "script"
        fields["script_name"] = script_name
        if args:
            fields["script_args"] = "\x00".join(args)
        if env:
            fields["script_env"] = "\n".join(env)
        files = [("script_file", script_name, script, "application/x-shellscript")]
        return await self.client._multipart(
            self.client.node_url + "/api/v2/tenant/install/script",
            fields, files, op="install.script", timeout=timeout)

    async def compose(
        self, *, host: str, ssh_user: str, key_pair_name: str,
        stack_name: str, compose: bytes | str,
        env_file: bytes | str | None = None,
        registry_slug: str | None = None,
        docker_user: str | None = None, docker_password: str | None = None,
        workspace_user: str | None = None, organization: str | None = None,
        timeout: float = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        if isinstance(compose, bytes):
            compose = compose.decode("utf-8")
        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)
        fields["stack_name"] = stack_name
        fields["compose_content"] = compose
        fields["cloud_provider"] = "docker"
        if env_file is not None:
            if isinstance(env_file, bytes):
                env_file = env_file.decode("utf-8")
            fields["env_file_content"] = env_file
        if registry_slug:
            fields["docker_registry_slug"] = registry_slug
        if docker_user:
            fields["docker_username"] = docker_user
        if docker_password:
            fields["docker_password"] = docker_password
        return await self.client._multipart(
            self.client.node_url + "/api/v2/tenant/provision/docker-compose/custom",
            fields, [], op="install.compose", timeout=timeout)


class _AsyncDeploy(_AsyncResource):
    async def container(
        self, *, host: str, ssh_user: str, key_pair_name: str, image: str,
        name: str | None = None, ports: list[str] | None = None,
        volumes: list[str] | None = None, env: list[str] | None = None,
        restart_policy: str = "unless-stopped", network: str | None = None,
        command: str | None = None,
        docker_user: str | None = None, docker_password: str | None = None,
        registry_slug: str | None = None,
        workspace_user: str | None = None, organization: str | None = None,
        timeout: float = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        if not image:
            raise ValueError("deploy.container: image is required")
        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)
        fields["image"] = image
        fields["restart_policy"] = restart_policy
        fields["cloud_provider"] = "docker"
        if name: fields["container_name"] = name
        if ports: fields["ports"] = ",".join(ports)
        if volumes: fields["volumes"] = ",".join(volumes)
        if env: fields["environment_vars"] = ",".join(env)
        if network: fields["network"] = network
        if command: fields["command"] = command
        if registry_slug: fields["docker_registry_slug"] = registry_slug
        if docker_user: fields["docker_username"] = docker_user
        if docker_password: fields["docker_password"] = docker_password
        return await self.client._multipart(
            self.client.node_url + "/api/v2/tenant/container/deploy",
            fields, [], op="deploy.container", timeout=timeout)

    async def stack(
        self, kind: str, *, host: str, ssh_user: str, key_pair_name: str,
        repo_url: str, branch: str = "main",
        app_name: str | None = None,
        git_provider: str | None = None,
        git_username: str | None = None, git_token: str | None = None,
        build_mode: str | None = None, entry: str | None = None,
        requirements: str | None = None, framework: str | None = None,
        go_version: str | None = None,
        http_port: str | None = None, https_port: str | None = None,
        app_port: str | None = None, env_vars: str | None = None,
        workspace_user: str | None = None, organization: str | None = None,
        timeout: float = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        target = STACK_TARGETS.get(kind)
        if not target:
            raise ValueError(f"deploy.stack: unknown stack {kind!r}")
        if not repo_url:
            raise ValueError("deploy.stack: repo_url is required")
        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)
        fields[target.git_field] = repo_url
        fields[target.branch_field] = branch
        for k, v in [
            ("app_name", app_name), ("git_provider", git_provider),
            ("git_username", git_username), ("git_token", git_token),
            ("build_mode", build_mode), ("entry", entry),
            ("requirements", requirements), ("framework", framework),
            ("go_version", go_version), ("http_port", http_port),
            ("https_port", https_port), ("app_port", app_port),
            ("env_vars", env_vars),
        ]:
            if v: fields[k] = v
        return await self.client._multipart(
            self.client.node_url + target.path, fields, [],
            op=f"deploy.stack.{kind}", timeout=timeout)


class _AsyncMarketplaceList(_AsyncResource):
    PATH = ""
    KEY = ""

    async def list(self) -> list[dict[str, Any]]:
        body = await self.client._json("GET", self.client.node_url + self.PATH,
                                       op=f"marketplace.{self.KEY}.list")
        return body.get(self.KEY, [])

    async def show(self, item_id: str) -> dict[str, Any]:
        return await self.client._json("GET",
            f"{self.client.node_url}{self.PATH}/{item_id}",
            op=f"marketplace.{self.KEY}.show")


class _AsyncAgents(_AsyncMarketplaceList):
    PATH = "/api/v2/marketplace/agents"
    KEY = "agents"

    async def deploy(self, agent_id: str, *, host: str, ssh_user: str,
                     key_pair_name: str, agent_name: str | None = None,
                     http_port: str = "80", app_port: str | None = None,
                     system_prompt: str | None = None,
                     env_vars: str | None = None,
                     version: str = "1.0.0") -> dict[str, Any]:
        body = {
            "agent_id": agent_id, "hostname": host, "ssh_username": ssh_user,
            "key_pair_name": key_pair_name, "username": self.client.username,
            "http_port": http_port, "version": version,
        }
        for k, v in [("agent_name", agent_name), ("app_port", app_port),
                     ("system_prompt", system_prompt), ("env_vars", env_vars)]:
            if v: body[k] = v
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/marketplace/agents/deploy",
            op="marketplace.agents.deploy", json_body=body)


class _AsyncModels(_AsyncMarketplaceList):
    PATH = "/api/v2/marketplace/models"
    KEY = "models"


class _AsyncSolutions(_AsyncMarketplaceList):
    PATH = "/api/v2/marketplace/templates"
    KEY = "templates"

    async def provision(self, template_id: str, *, resource_name: str,
                        cloud_provider: str, region: str,
                        environment: str = "development",
                        variables: dict[str, Any] | None = None) -> dict[str, Any]:
        body = {
            "template_name": template_id, "resource_name": resource_name,
            "cloud_provider": cloud_provider, "region": region,
            "environment": environment, "variables": variables or {},
            "username": self.client.username,
        }
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/marketplace/provision",
            op="marketplace.solutions.provision", json_body=body,
            timeout=DEFAULT_LONG_TIMEOUT)


class _AsyncMarketplace(_AsyncResource):
    @property
    def agents(self) -> _AsyncAgents:
        return _AsyncAgents(self.client)

    @property
    def models(self) -> _AsyncModels:
        return _AsyncModels(self.client)

    @property
    def solutions(self) -> _AsyncSolutions:
        return _AsyncSolutions(self.client)


class _AsyncCloud(_AsyncResource):
    async def _provision(self, op: str, path: str, *, app: str,
                         cloud: str = "aws", region: str = "us-east-1",
                         env: str = "development", resource_type: str,
                         **extras: Any) -> dict[str, Any]:
        body: dict[str, Any] = {
            "app_name": app, "resource_name": app, "instance_name": app,
            "network_name": app, "key_name": app, "role_name": app,
            "hostname": app, "cloud_provider": cloud, "region": region,
            "environment": env, "resource_type": resource_type,
            "username": self.client.username,
        }
        body.update(extras)
        return await self.client._json("POST",
            self.client.node_url + path, op=op, json_body=body,
            timeout=DEFAULT_LONG_TIMEOUT)

    async def create_s3_bucket(self, name: str, region: str = "us-east-1",
                               cloud: str = "aws") -> dict[str, Any]:
        return await self._provision("cloud.s3.create_bucket",
            "/api/v2/tenant/provision/storage",
            app=name, cloud=cloud, region=region, resource_type="s3")

    async def create_iam_policy(self, name: str,
                                policy_document: str | dict[str, Any]) -> dict[str, Any]:
        if isinstance(policy_document, dict):
            policy_document = json.dumps(policy_document)
        return await self._provision("cloud.iam.create_policy",
            "/api/v2/tenant/provision/security",
            app=name, resource_type="policy", policy_document=policy_document)

    async def create_iam_role(self, name: str,
                              assume_role_policy: str | dict[str, Any]) -> dict[str, Any]:
        if isinstance(assume_role_policy, dict):
            assume_role_policy = json.dumps(assume_role_policy)
        return await self._provision("cloud.iam.create_role",
            "/api/v2/tenant/provision/security",
            app=name, resource_type="role", assume_role_policy=assume_role_policy)

    async def create_vm(self, name: str, *, cloud: str = "aws",
                        region: str = "us-east-1",
                        instance_type: str = "t2.micro",
                        volume_size: int = 30) -> dict[str, Any]:
        return await self._provision("cloud.vm.create",
            "/api/v2/tenant/provision/vm",
            app=name, cloud=cloud, region=region, resource_type="vm",
            instance_type=instance_type, volume_size=volume_size)

    async def _managed_database(self, op: str, name: str, resource_type: str, *,
                                cloud: str = "aws", region: str = "us-east-1",
                                configuration: dict[str, Any] | None = None,
                                tags: dict[str, str] | None = None) -> dict[str, Any]:
        config = dict(configuration or {})
        config.setdefault("region", region)
        config.setdefault("environment", config.get("env", "development"))
        config.setdefault("company_name", self.client.username)
        body = {
            "username": self.client.username,
            "cloud_provider": cloud,
            "resource_type": resource_type,
            "resource_name": name,
            "configuration": config,
            "tags": tags or {"Name": name, "ManagedBy": "vxsdk"},
        }
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/tenant/provision/databases",
            op=op, json_body=body, timeout=DEFAULT_LONG_TIMEOUT)

    async def create_rds(self, name: str, *, cloud: str = "aws",
                         region: str = "us-east-1", engine: str = "mysql",
                         version: str = "8.0", instance_type: str = "db.t3.micro",
                         storage_size: int = 20, multi_az: bool = False,
                         backup_retention: int = 7, encryption: bool = True,
                         publicly_accessible: bool = False, username: str = "admin",
                         password: str = "", db_name: str | None = None,
                         port: int | None = None, vpc_id: str = "",
                         subnet_ids: list[str] | None = None,
                         allowed_security_group_ids: list[str] | None = None,
                         tags: dict[str, str] | None = None) -> dict[str, Any]:
        config: dict[str, Any] = {
            "engine": engine,
            "version": version,
            "instance_type": instance_type,
            "storage_size": storage_size,
            "multi_az": multi_az,
            "backup_retention": backup_retention,
            "encryption": encryption,
            "publicly_accessible": publicly_accessible,
            "username": username,
            "password": password,
            "db_name": db_name or name,
            "port": port or (5432 if engine in {"postgres", "postgresql"} else 3306),
            "vpc_id": vpc_id,
        }
        if subnet_ids:
            config["subnet_ids"] = subnet_ids
        if allowed_security_group_ids:
            config["allowed_security_group_ids"] = allowed_security_group_ids
        return await self._managed_database("cloud.database.create_rds", name, "rds",
            cloud=cloud, region=region, configuration=config, tags=tags)

    async def create_aurora(self, name: str, *, cloud: str = "aws",
                            region: str = "us-east-1", engine: str = "mysql",
                            version: str = "8.0", instance_type: str = "db.t3.medium",
                            instance_count: int = 2, backup_retention: int = 7,
                            encryption: bool = True, publicly_accessible: bool = False,
                            username: str = "admin", password: str = "",
                            db_name: str | None = None, port: int | None = None,
                            vpc_id: str = "", subnet_ids: list[str] | None = None,
                            allowed_security_group_ids: list[str] | None = None,
                            tags: dict[str, str] | None = None) -> dict[str, Any]:
        if not subnet_ids or len(subnet_ids) < 2:
            raise ValueError("create_aurora: subnet_ids must include at least two subnets")
        config: dict[str, Any] = {
            "engine": engine,
            "version": version,
            "instance_type": instance_type,
            "instance_count": instance_count,
            "backup_retention": backup_retention,
            "encryption": encryption,
            "publicly_accessible": publicly_accessible,
            "username": username,
            "password": password,
            "db_name": db_name or name,
            "port": port or (5432 if engine in {"postgres", "postgresql"} else 3306),
            "vpc_id": vpc_id,
            "subnet_ids": subnet_ids,
        }
        if allowed_security_group_ids:
            config["allowed_security_group_ids"] = allowed_security_group_ids
        return await self._managed_database("cloud.database.create_aurora", name, "aurora",
            cloud=cloud, region=region, configuration=config, tags=tags)

    async def create_redis(self, name: str, *, cloud: str = "aws",
                           region: str = "us-east-1",
                           node_type: str = "cache.t3.micro",
                           num_cache_nodes: int = 1,
                           subnet_ids: list[str] | None = None,
                           vpc_security_group_ids: list[str] | None = None,
                           tags: dict[str, str] | None = None) -> dict[str, Any]:
        if not subnet_ids:
            raise ValueError("create_redis: subnet_ids is required")
        if not vpc_security_group_ids:
            raise ValueError("create_redis: vpc_security_group_ids is required")
        return await self._managed_database("cloud.database.create_redis", name, "redis",
            cloud=cloud, region=region,
            configuration={
                "node_type": node_type,
                "num_cache_nodes": num_cache_nodes,
                "subnet_ids": subnet_ids,
                "vpc_security_group_ids": vpc_security_group_ids,
            },
            tags=tags)


class _AsyncNodes(_AsyncResource):
    async def list(self) -> list[dict[str, Any]]:
        body = await self.client._json("GET",
            self.client.infinity_url + "/api/v1/auth/nodes/",
            op="nodes.list")
        return body.get("data", body) if isinstance(body, dict) else body

    async def default(self) -> dict[str, Any] | None:
        for n in await self.list():
            if n.get("is_default_node"):
                return n
        return None


# ── Async VXCOMPUTER / Robotic / VxChrono ──────────────────────────────

class _AsyncVxComputer(_AsyncResource):
    async def info(self) -> dict[str, Any]:
        return await self.client._json("GET", self.client.node_url + "/api/v2/vxcomputer/info",
                                       op="vxcomputer.info")

    async def health(self) -> dict[str, Any]:
        return await self.client._json("GET", self.client.node_url + "/api/v2/vxcomputer/health",
                                       op="vxcomputer.health")

    async def classify(self, command: str) -> dict[str, Any]:
        if not command:
            raise ValueError("vxcomputer.classify: command is required")
        q = urllib.parse.urlencode({"command": command})
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/vxcomputer/policy/classify?{q}",
            op="vxcomputer.classify")

    async def run(self, objective: str, *, channel: str = "chat",
                  session_id: str = "") -> dict[str, Any]:
        if not objective:
            raise ValueError("vxcomputer.run: objective is required")
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/vxcomputer/run",
            op="vxcomputer.run",
            json_body={"objective": objective, "channel": channel,
                       "session_id": session_id})

    async def resolve_approval(self, run_id: str, step_id: str, command: str,
                               *, decision: str = "approve", ttl_seconds: int = 900,
                               approver: str = "") -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/vxcomputer/approval/resolve",
            op="vxcomputer.resolve_approval",
            json_body={"run_id": run_id, "step_id": step_id, "command": command,
                       "decision": decision, "ttl_seconds": ttl_seconds,
                       "approver": approver or self.client.username})

    async def audit_verify(self) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + "/api/v2/vxcomputer/audit/verify",
            op="vxcomputer.audit_verify")


class _AsyncRobotic(_AsyncResource):
    async def info(self) -> dict[str, Any]:
        return await self.client._json("GET", self.client.node_url + "/api/v2/robotic/info",
                                       op="robotic.info")

    async def list(self) -> dict[str, Any]:
        return await self.client._json("GET", self.client.node_url + "/api/v2/robotic/robots",
                                       op="robotic.list")

    async def register(self, spec: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST", self.client.node_url + "/api/v2/robotic/robots",
                                       op="robotic.register", json_body=spec)

    async def get(self, robot_id: str) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}",
            op="robotic.get")

    async def delete(self, robot_id: str) -> dict[str, Any]:
        return await self.client._json("DELETE",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}",
            op="robotic.delete")

    async def command(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/command",
            op="robotic.command", json_body=payload)

    async def command_status(self, command_id: str) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/robotic/commands/{command_id}",
            op="robotic.command_status")

    async def emergency_stop(self, robot_id: str) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/emergency-stop",
            op="robotic.emergency_stop", json_body={})

    async def telemetry(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/telemetry",
            op="robotic.telemetry", json_body=payload)

    async def resolve_approval(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/approval/resolve",
            op="robotic.resolve_approval", json_body=payload)

    async def plan(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Autonomous LLM mission plan (payload: objective, execute, provider, model)."""
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/plan",
            op="robotic.plan", json_body=payload)

    async def schedule(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Schedule a recurring mission via vxchrono (payload: objective, schedule_type, cadence_minutes|cron_expr)."""
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/schedule",
            op="robotic.schedule", json_body=payload)

    async def fleet_command(self, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/robotic/fleet/command",
            op="robotic.fleet_command", json_body=payload)

    async def fleet_mission(self, payload: dict[str, Any]) -> dict[str, Any]:
        """Multi-robot mission via the workflow engine + per-robot LLM plan
        (payload: objective, robot_ids|robot_type|tags)."""
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/robotic/fleet/mission",
            op="robotic.fleet_mission", json_body=payload)


class _AsyncVxChrono(_AsyncResource):
    async def init(self) -> dict[str, Any]:
        return await self.client._json("POST", self.client.node_url + "/api/v2/vxchrono/init",
                                       op="vxchrono.init", json_body={})

    async def create_goal(self, goal: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST", self.client.node_url + "/api/v2/vxchrono/goals",
                                       op="vxchrono.create_goal", json_body=goal)

    async def list_goals(self) -> dict[str, Any]:
        return await self.client._json("GET", self.client.node_url + "/api/v2/vxchrono/goals",
                                       op="vxchrono.list_goals")

    async def get_goal(self, goal_id: str) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.get_goal")

    async def update_goal(self, goal_id: str, patch: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("PATCH",
            self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.update_goal", json_body=patch)

    async def delete_goal(self, goal_id: str) -> dict[str, Any]:
        return await self.client._json("DELETE",
            self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.delete_goal")

    async def schedule(self, goal_id: str, schedule: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}/schedule",
            op="vxchrono.schedule", json_body=schedule)

    async def launch_run(self, goal_id: str, payload: dict[str, Any] | None = None) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}/run",
            op="vxchrono.launch_run", json_body=payload or {})

    async def get_run(self, run_id: str) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}",
            op="vxchrono.get_run")

    async def pause_run(self, run_id: str) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/pause",
            op="vxchrono.pause_run", json_body={})

    async def resume_run(self, run_id: str) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/resume",
            op="vxchrono.resume_run", json_body={})

    async def stop_run(self, run_id: str) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/stop",
            op="vxchrono.stop_run", json_body={})

    async def dispatch_scheduler(self) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/vxchrono/scheduler/dispatch",
            op="vxchrono.dispatch_scheduler", json_body={})


class _AsyncWorkflow(_AsyncResource):
    """Async Workflow orchestration — n8n-style visual workflow engine.
    Mirrors /api/v2/workflow/* (see vxsdk.Workflow for the sync version)."""

    async def list(self) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + "/api/v2/workflow/workflows",
            op="workflow.list")

    async def get(self, workflow_id: str) -> dict[str, Any]:
        if not workflow_id:
            raise ValueError("workflow.get: workflow_id is required")
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/workflow/workflows/{workflow_id}",
            op="workflow.get")

    async def create(self, definition: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/workflows",
            op="workflow.create", json_body=definition)

    async def delete(self, workflow_id: str) -> dict[str, Any]:
        if not workflow_id:
            raise ValueError("workflow.delete: workflow_id is required")
        return await self.client._json("DELETE",
            self.client.node_url + f"/api/v2/workflow/workflows/{workflow_id}",
            op="workflow.delete")

    async def save(self, definition: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/save",
            op="workflow.save", json_body=definition)

    async def publish(self, definition: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/publish",
            op="workflow.publish", json_body=definition)

    async def validate(self, definition: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/validate",
            op="workflow.validate", json_body=definition)

    async def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/execute",
            op="workflow.execute", json_body=payload)

    async def test_node(self, payload: dict[str, Any]) -> dict[str, Any]:
        return await self.client._json("POST",
            self.client.node_url + "/api/v2/workflow/test-node",
            op="workflow.test_node", json_body=payload)

    async def list_executions(self) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + "/api/v2/workflow/executions",
            op="workflow.list_executions")

    async def get_execution(self, execution_id: str) -> dict[str, Any]:
        if not execution_id:
            raise ValueError("workflow.get_execution: execution_id is required")
        return await self.client._json("GET",
            self.client.node_url + f"/api/v2/workflow/executions/{execution_id}",
            op="workflow.get_execution")

    async def cancel_execution(self, execution_id: str) -> dict[str, Any]:
        if not execution_id:
            raise ValueError("workflow.cancel_execution: execution_id is required")
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/workflow/executions/{execution_id}/cancel",
            op="workflow.cancel_execution", json_body={})

    async def delete_execution(self, execution_id: str) -> dict[str, Any]:
        if not execution_id:
            raise ValueError("workflow.delete_execution: execution_id is required")
        return await self.client._json("DELETE",
            self.client.node_url + f"/api/v2/workflow/executions/{execution_id}",
            op="workflow.delete_execution")

    async def export(self, definition: dict[str, Any], fmt: str = "json") -> dict[str, Any]:
        if fmt not in ("json", "yaml"):
            raise ValueError("workflow.export: fmt must be 'json' or 'yaml'")
        return await self.client._json("POST",
            self.client.node_url + f"/api/v2/workflow/export/{fmt}",
            op="workflow.export", json_body=definition)

    async def health(self) -> dict[str, Any]:
        return await self.client._json("GET",
            self.client.node_url + "/api/v2/workflow/health",
            op="workflow.health")


# ── Async Workspace (mirrors vxsdk.Workspace) ──────────────────────────

class _AsyncWorkspace(_AsyncResource):
    """Async equivalent of vxsdk.Workspace — workspace + org lifecycle,
    cloud / AI / Git / messaging / payment / SMTP / SSL / OAuth / OKTA /
    Vault credential storage, Docker credentials + Docker registry endpoints,
    free-form Random credentials, Servers list, VM keypairs, API tokens.
    All endpoints live under /api/v2/setup/* or /api/v2/vault/*; bodies are
    sent over TLS and never logged by the SDK.
    """

    _AI_KEY_PREFIX = {
        "openai": "OPENAI", "anthropic": "ANTHROPIC", "gemini": "GEMINI",
        "deepseek": "DEEPSEEK", "qwen": "QWEN", "huggingface": "HUGGINGFACE",
        "azure-openai": "AZURE_OPENAI", "llama": "LLAMA", "mistral": "MISTRAL",
        "cohere": "COHERE", "perplexity": "PERPLEXITY", "groq": "GROQ",
        "hermes": "HERMES", "openclaw": "OPENCLAW", "ollama": "OLLAMA",
        "brave": "BRAVE",
    }

    _VALID_DOCKER_REGISTRY_TYPES = {
        "dockerhub", "ecr", "gcr", "acr", "ghcr", "gitlab", "quay", "harbor", "jfrog", "custom",
    }

    # ── workspace + organization ──
    async def create_workspace(self, name: str, region: str = "") -> dict[str, Any]:
        return await self._post("/api/v2/setup/workspace",
                                {"workspace_name": name, "region": region},
                                op="workspace.create_workspace")

    async def create_organization(self, name: str, plan: str = "") -> dict[str, Any]:
        return await self._post("/api/v2/setup/organization",
                                {"org_name": name, "plan": plan},
                                op="workspace.create_organization")

    async def delete_workspace(self) -> dict[str, Any]:
        return await self.client._json(
            "DELETE", self.client.node_url + "/api/v2/setup/workspace",
            op="workspace.delete_workspace")

    # ── cloud provider creds ──
    async def store_aws_credentials(self, *, access_key_id: str, secret_access_key: str,
                                    region: str = "us-east-1", iam_user: str = "",
                                    account_id: str = "") -> dict[str, Any]:
        body: dict[str, Any] = {
            "AWS_ACCESS_KEY_ID": access_key_id,
            "AWS_SECRET_ACCESS_KEY": secret_access_key,
            "AWS_REGION": region,
        }
        if iam_user:
            body["AWS_IAM_USER"] = iam_user
        if account_id:
            body["AWS_ACCOUNT_ID"] = account_id
        return await self._post("/api/v2/setup/aws-credentials", body,
                                op="workspace.store_aws_credentials")

    async def store_azure_credentials(self, *, client_id: str, client_secret: str,
                                      tenant_id: str, subscription_id: str) -> dict[str, Any]:
        return await self._post("/api/v2/setup/azure-credentials", {
            "AZURE_CLIENT_ID": client_id,
            "AZURE_CLIENT_SECRET": client_secret,
            "AZURE_TENANT_ID": tenant_id,
            "AZURE_SUBSCRIPTION_ID": subscription_id,
        }, op="workspace.store_azure_credentials")

    async def store_gcp_credentials(self, *, project_id: str,
                                    service_account_key: str) -> dict[str, Any]:
        return await self._post("/api/v2/setup/gcp-credentials", {
            "GCP_PROJECT_ID": project_id,
            "GCP_SERVICE_ACCOUNT_KEY": service_account_key,
        }, op="workspace.store_gcp_credentials")

    async def get_all_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/setup/get-all-credentials", {},
                                op="workspace.get_all_credentials")

    # ── API tokens ──
    async def create_api_token(self, name: str, expires_in_days: int = 90) -> dict[str, Any]:
        return await self._post("/api/v2/setup/api-token",
                                {"token_name": name, "expires_in_days": expires_in_days},
                                op="workspace.create_api_token")

    async def get_api_token(self, name: str) -> dict[str, Any]:
        return await self._post("/api/v2/setup/get-api-token", {"token_name": name},
                                op="workspace.get_api_token")

    # ── AI provider credentials ──
    async def store_ai_credentials(self, provider: str, *,
                                   api_key: str = "", org_id: str = "",
                                   endpoint: str = "", model: str = "") -> dict[str, Any]:
        if not provider:
            raise ValueError("workspace.store_ai_credentials: provider is required")
        prefix = self._AI_KEY_PREFIX.get(provider)
        if prefix is None:
            raise ValueError(
                f"workspace.store_ai_credentials: unknown provider {provider!r}")
        body: dict[str, Any] = {}
        if api_key:
            body[f"{prefix}_API_KEY"] = api_key
        if model:
            body[f"{prefix}_MODEL"] = model
        if endpoint:
            if provider == "azure-openai":
                body["AZURE_OPENAI_ENDPOINT"] = endpoint
            else:
                body[f"{prefix}_BASE_URL"] = endpoint
        if org_id:
            body[f"{prefix}_ORGANIZATION"] = org_id
        return await self._post(f"/api/v2/setup/ai-{provider}-credentials", body,
                                op=f"workspace.store_ai_credentials.{provider}")

    async def get_all_ai_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/setup/ai-get-all-credentials", {},
                                op="workspace.get_all_ai_credentials")

    # ── git / messaging / payment / smtp / ssl / oauth / okta / vault ──
    async def store_git_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/git-credentials", body,
                                op="workspace.store_git_credentials")

    async def store_gitlab_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/gitlab-credentials", body,
                                op="workspace.store_gitlab_credentials")

    async def store_kubeconfig(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/kubeconfig-credentials", body,
                                op="workspace.store_kubeconfig")

    async def store_oauth_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/oauth-credentials", body,
                                op="workspace.store_oauth_credentials")

    async def store_okta_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/okta-credentials", body,
                                op="workspace.store_okta_credentials")

    async def store_cyberark_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/cyberark-credentials", body,
                                op="workspace.store_cyberark_credentials")

    async def store_external_vault_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/external-vault-credentials", body,
                                op="workspace.store_external_vault_credentials")

    async def get_vault_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/get-vault-credentials", body,
                                op="workspace.get_vault_credentials")

    async def store_payment_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/payment-credentials", body,
                                op="workspace.store_payment_credentials")

    async def get_all_payment_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/setup/payment-get-all-credentials", {},
                                op="workspace.get_all_payment_credentials")

    async def store_smtp_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/smtp-provider-credentials", body,
                                op="workspace.store_smtp_credentials")

    async def get_all_smtp_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/setup/smtp-get-all-credentials", {},
                                op="workspace.get_all_smtp_credentials")

    async def store_messaging_bot_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/messaging-bot-credentials", body,
                                op="workspace.store_messaging_bot_credentials")

    async def get_all_messaging_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/setup/messaging-get-all-credentials", {},
                                op="workspace.get_all_messaging_credentials")

    async def store_ssl_certificate_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return await self._post("/api/v2/setup/ssl-certificate-credentials", body,
                                op="workspace.store_ssl_certificate_credentials")

    async def delete_credential(self, name: str) -> dict[str, Any]:
        if not name:
            raise ValueError("workspace.delete_credential: name is required")
        return await self._post("/api/v2/setup/delete-credential", {"name": name},
                                op="workspace.delete_credential")

    # ── docker credentials (multi-registry under docker/registries/<slug>) ──
    async def store_docker_credentials(self, *, registry_name: str, docker_username: str,
                                       docker_password: str, docker_email: str = "",
                                       docker_server: str = "", registry_type: str = "") -> dict[str, Any]:
        if not registry_name:
            raise ValueError("workspace.store_docker_credentials: registry_name is required")
        body: dict[str, Any] = {
            "DOCKER_USERNAME": docker_username,
            "DOCKER_PASSWORD": docker_password,
            "DOCKER_REGISTRY_NAME": registry_name,
        }
        if docker_email:
            body["DOCKER_EMAIL"] = docker_email
        if docker_server:
            body["DOCKER_SERVER"] = docker_server
        if registry_type:
            body["DOCKER_REGISTRY_TYPE"] = registry_type
        return await self._post("/api/v2/setup/docker-credentials", body,
                                op="workspace.store_docker_credentials")

    async def get_all_docker_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/vault/get-docker-credentials", {},
                                op="workspace.get_all_docker_credentials")

    async def get_docker_credentials_by_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.get_docker_credentials_by_registry: registry_slug is required")
        return await self._post("/api/v2/vault/get-single-docker-credentials",
                                {"registry_slug": registry_slug},
                                op="workspace.get_docker_credentials_by_registry")

    # ── docker REGISTRY endpoints (distinct from credentials) ──
    async def store_docker_registry(self, *, registry_name: str, registry_type: str,
                                    registry_url: str, namespace: str = "", region: str = "",
                                    default_credential_slug: str = "", description: str = "",
                                    is_default: bool = False) -> dict[str, Any]:
        if not registry_name:
            raise ValueError("workspace.store_docker_registry: registry_name is required")
        if registry_type not in self._VALID_DOCKER_REGISTRY_TYPES:
            raise ValueError(
                f"workspace.store_docker_registry: registry_type must be one of "
                f"{sorted(self._VALID_DOCKER_REGISTRY_TYPES)}")
        if not registry_url:
            raise ValueError("workspace.store_docker_registry: registry_url is required")
        body: dict[str, Any] = {
            "registry_name": registry_name,
            "registry_type": registry_type,
            "registry_url": registry_url,
            "is_default": bool(is_default),
        }
        if namespace:
            body["namespace"] = namespace
        if region:
            body["region"] = region
        if default_credential_slug:
            body["default_credential_slug"] = default_credential_slug
        if description:
            body["description"] = description
        return await self._post("/api/v2/setup/docker-registry", body,
                                op="workspace.store_docker_registry")

    async def get_all_docker_registries(self) -> dict[str, Any]:
        return await self._post("/api/v2/vault/get-docker-registries", {},
                                op="workspace.get_all_docker_registries")

    async def get_docker_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.get_docker_registry: registry_slug is required")
        return await self._post("/api/v2/vault/get-single-docker-registry",
                                {"registry_slug": registry_slug},
                                op="workspace.get_docker_registry")

    async def delete_docker_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.delete_docker_registry: registry_slug is required")
        return await self._post("/api/v2/vault/delete-docker-registry",
                                {"registry_slug": registry_slug},
                                op="workspace.delete_docker_registry")

    # ── random / generic credentials (free-form bucket) ──
    async def store_random_credential(self, *, credential_name: str,
                                      credential_type: str = "", description: str = "",
                                      fields: dict[str, Any] | None = None,
                                      json_blob: str = "") -> dict[str, Any]:
        if not credential_name:
            raise ValueError("workspace.store_random_credential: credential_name is required")
        if json_blob:
            try:
                json.loads(json_blob)
            except (ValueError, TypeError) as e:
                raise ValueError(
                    f"workspace.store_random_credential: json_blob is not valid JSON: {e}")
        body: dict[str, Any] = {"credential_name": credential_name}
        if credential_type:
            body["credential_type"] = credential_type
        if description:
            body["description"] = description
        if fields is not None:
            body["fields"] = fields
        if json_blob:
            body["json_blob"] = json_blob
        return await self._post("/api/v2/setup/random-credentials", body,
                                op="workspace.store_random_credential")

    async def get_all_random_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/vault/get-random-credentials", {},
                                op="workspace.get_all_random_credentials")

    async def get_random_credential(self, credential_slug: str) -> dict[str, Any]:
        if not credential_slug:
            raise ValueError("workspace.get_random_credential: credential_slug is required")
        return await self._post("/api/v2/vault/get-single-random-credential",
                                {"credential_slug": credential_slug},
                                op="workspace.get_random_credential")

    async def delete_random_credential(self, credential_slug: str) -> dict[str, Any]:
        if not credential_slug:
            raise ValueError("workspace.delete_random_credential: credential_slug is required")
        return await self._post("/api/v2/vault/delete-random-credential",
                                {"credential_slug": credential_slug},
                                op="workspace.delete_random_credential")

    # ── servers list (developer host inventory) ──
    async def store_server(self, *, name: str, ip_address: str, hostname: str = "",
                           port: int = 22, description: str = "",
                           keypair_name: str = "", keypair_location: str = "",
                           tags: list[str] | None = None) -> dict[str, Any]:
        if not name:
            raise ValueError("workspace.store_server: name is required")
        if not ip_address:
            raise ValueError("workspace.store_server: ip_address is required")
        if not (1 <= int(port) <= 65535):
            raise ValueError("workspace.store_server: port must be between 1 and 65535")
        body: dict[str, Any] = {
            "name": name,
            "ip_address": ip_address,
            "port": int(port),
        }
        if hostname:
            body["hostname"] = hostname
        if description:
            body["description"] = description
        if keypair_name:
            body["keypair_name"] = keypair_name
        if keypair_location:
            body["keypair_location"] = keypair_location
        if tags:
            body["tags"] = [str(t) for t in tags if str(t).strip()]
        return await self._post("/api/v2/setup/server", body, op="workspace.store_server")

    async def get_all_servers(self) -> dict[str, Any]:
        return await self._post("/api/v2/vault/get-servers", {},
                                op="workspace.get_all_servers")

    async def get_server(self, server_slug: str) -> dict[str, Any]:
        if not server_slug:
            raise ValueError("workspace.get_server: server_slug is required")
        return await self._post("/api/v2/vault/get-single-server",
                                {"server_slug": server_slug}, op="workspace.get_server")

    async def delete_server(self, server_slug: str) -> dict[str, Any]:
        if not server_slug:
            raise ValueError("workspace.delete_server: server_slug is required")
        return await self._post("/api/v2/vault/delete-server",
                                {"server_slug": server_slug}, op="workspace.delete_server")

    # ── VM keypairs ──
    async def store_vm_credentials(self, *, key_pair_name: str, ssh_public_key: str = "",
                                   ssh_private_key: str = "", vm_password: str = "") -> dict[str, Any]:
        if not key_pair_name:
            raise ValueError("workspace.store_vm_credentials: key_pair_name is required")
        body: dict[str, Any] = {"key_pair_name": key_pair_name}
        if ssh_public_key:
            body["SSH_PUBLIC_KEY"] = ssh_public_key
        if ssh_private_key:
            body["SSH_PRIVATE_KEY"] = ssh_private_key
        if vm_password:
            body["VM_PASSWORD"] = vm_password
        return await self._post("/api/v2/setup/vm-credentials", body,
                                op="workspace.store_vm_credentials")

    async def get_all_vm_credentials(self) -> dict[str, Any]:
        return await self._post("/api/v2/vault/get-vm-credentials", {},
                                op="workspace.get_all_vm_credentials")

    async def get_vm_credentials_by_keypair(self, key_pair_name: str) -> dict[str, Any]:
        if not key_pair_name:
            raise ValueError("workspace.get_vm_credentials_by_keypair: key_pair_name is required")
        return await self._post("/api/v2/vault/get-single-vm-credentials",
                                {"key_pair_name": key_pair_name},
                                op="workspace.get_vm_credentials_by_keypair")

    # ── GitHub credentials ──
    async def store_github_credentials(self, *, github_token: str, github_token_name: str = "default",
                                       github_user: str = "", ssh_public_key: str = "",
                                       ssh_private_key: str = "") -> dict[str, Any]:
        if not github_token:
            raise ValueError("workspace.store_github_credentials: github_token is required")
        body: dict[str, Any] = {
            "GITHUB_TOKEN": github_token,
            "GITHUB_TOKEN_NAME": github_token_name,
        }
        if github_user:
            body["GITHUB_USER"] = github_user
        if ssh_public_key:
            body["SSH_PUBLIC_KEY"] = ssh_public_key
        if ssh_private_key:
            body["SSH_PRIVATE_KEY"] = ssh_private_key
        return await self._post("/api/v2/setup/github-credentials", body,
                                op="workspace.store_github_credentials")

    # ── internal ──
    async def _post(self, path: str, body: dict[str, Any], *, op: str) -> dict[str, Any]:
        # Mirror vxsdk.Workspace._post — inject username + organization from
        # the authenticated client unless the caller overrode them.
        merged = dict(body)
        merged.setdefault("username", self.client.username)
        org = getattr(self.client, "organization", None) or self.client.username
        merged.setdefault("organization", org)
        return await self.client._json(
            "POST", self.client.node_url + path,
            op=op, json_body=merged,
        )


# ── Async SalesShift — tracked email + the global leads pool ───────────

class _AsyncSalesShift(_AsyncResource):
    """Async equivalent of vxsdk.SalesShift — tracked email plus the whole
    leads-pool surface described in ``salesshift/doc/LEADS_CLIENT_CONTRACT.md``.

    Two halves, and the line between them is the most important thing here:

    * **email / stats** — operates on *Contacts*. Mailable. ``send_email``
      puts a real message on the wire through the org's BYOK provider.
    * **leads** — the global pool. **NOT mailable.** A lead is a scraped or
      purchased record carrying no consent metadata. The only route from a
      lead to something you may send to is :meth:`convert_lead` /
      :meth:`convert_from_pool`, which creates a Contact and is where consent
      is written. Never pass a lead — or a lead's address — into
      :meth:`send_email` or any sequence/campaign call; a scraped record
      entering a sending path is how a tenant's sending domain dies.

    Two more rules this class is built around:

    * **An unrevealed address is a mask** (``j•••@acme.com``), not an address.
      ``has_email`` says an address EXISTS; ``email_revealed`` says whether you
      may see it. Never render a mask as if it were real, never let it be
      copied as one. :meth:`send_email` refuses one outright.
    * **Reveal spends metered quota.** :meth:`reveal_lead` and
      :meth:`convert_from_pool` (with ``reveal_if_needed=True``) both charge.
      Show :meth:`reveal_quota` before a batch and :meth:`describe_convert`
      after one.

    Base URLs: every leads endpoint is on the **Infinity control plane**
    (``/api/v1/salesshift/*``), never on the tenant node — so each URL below is
    built from ``client.infinity_url`` explicitly, exactly as the email methods
    do. Only :meth:`get_worker_health` talks to the node.
    """

    #: `POST /leads/save` and `POST /leads/convert-from-pool` both cap here.
    #: Enforced client-side too: convert-from-pool reveals, so an oversized
    #: batch that 400s after the caller has already assembled 500 ids is a
    #: worse experience than being told before the round trip.
    MAX_BATCH = 200
    #: `POST /leads/search` clamps to this server-side; mirrored so a caller
    #: asking for 500 gets 100 rows and a documented reason, not a surprise.
    MAX_PAGE_SIZE = 100
    #: `GET /leads` (saved leads) caps here.
    MAX_LIST_LIMIT = 500

    # ── email ──

    async def send_email(self, to_email: str, subject: str, body_html: str, *,
                         first_name: str = "", last_name: str = "") -> dict[str, Any]:
        """Send one tracked email. Merge tags like ``{{first_name}}`` resolve
        against the contact record; suppressed recipients are rejected.

        ``to_email`` must be a **Contact** address. Passing a pool lead here is
        the single most expensive mistake this SDK can make — leads carry no
        consent, so convert first. A masked address is rejected below rather
        than delivered to a mailbox that does not exist.
        """
        if not to_email or not subject or not body_html:
            raise VxValidationError("salesshift.send_email",
                                    "to_email, subject and body_html are required")
        if _EMAIL_MASK_MARK in to_email:
            raise VxValidationError(
                "salesshift.send_email",
                f"{to_email!r} is a MASKED pool address, not a real one — reveal "
                "the lead and convert it to a Contact before sending")
        payload: dict[str, Any] = {
            "to_email": to_email, "subject": subject, "body_html": body_html,
        }
        if first_name:
            payload["first_name"] = first_name
        if last_name:
            payload["last_name"] = last_name
        return await self.client._json(
            "POST", self.client.infinity_url + "/api/v1/salesshift/email/send",
            op="salesshift.send_email", json_body=payload,
        )

    async def list_emails(self, status: str = "") -> list[dict[str, Any]]:
        """Tracked outbound emails with engagement state (opens/clicks/replies)."""
        url = self.client.infinity_url + "/api/v1/salesshift/emails"
        if status:
            url += f"?status={urllib.parse.quote(status)}"
        body = await self.client._json("GET", url, op="salesshift.list_emails")
        return list(body.get("data") or [])

    async def get_stats(self) -> dict[str, Any]:
        """Live dashboard stats (contacts, deals, email funnel)."""
        return await self.client._json(
            "GET", self.client.infinity_url + "/api/v1/salesshift/stats",
            op="salesshift.get_stats",
        )

    async def get_worker_health(self) -> dict[str, Any]:
        """Health of the tenant-node Go email worker (:8744).

        The one method here that is node-scoped, so it needs ``node_url`` on the
        client (set it explicitly or load it from vxcli).
        """
        if not self.client.node_url:
            raise VxError("salesshift.get_worker_health",
                          "no node_url configured — pass node_url= to AsyncClient "
                          "or use AsyncClient.load_from_vxcli()")
        return await self.client._json(
            "GET", self.client.node_url + "/api/v2/salesshift/email/health",
            op="salesshift.get_worker_health",
        )

    # ── leads: transport ──

    def _leads_url(self, path: str) -> str:
        """Absolute Infinity URL. Leads never resolve against ``node_url``."""
        return self.client.infinity_url + "/api/v1/salesshift" + path

    async def _leads(self, method: str, path: str, *, op: str,
                     json_body: Any | None = None) -> Any:
        """One call, with 402 and 410 promoted to types callers can branch on."""
        try:
            return await self.client._json(method, self._leads_url(path),
                                           op=op, json_body=json_body)
        except VxError as exc:
            if exc.http_status == 402:
                raise VxLeadQuotaExhaustedError(
                    exc.op, "reveal allowance spent — nothing was charged for "
                            "this attempt", exc.http_status, exc.detail) from exc
            if exc.http_status == 410:
                raise VxLeadErasedError(
                    exc.op, "this person was erased at their own request — "
                            "terminal, and not retryable",
                    exc.http_status, exc.detail) from exc
            raise

    @staticmethod
    def _ids(op: str, field: str, ids: Iterable[str], *,
             cap: int | None = None) -> list[str]:
        """Normalise an id batch. ``cap`` only where the SERVER caps — this
        must not invent a limit the API does not have."""
        out = [str(i) for i in (ids or []) if str(i)]
        if not out:
            raise VxValidationError(op, f"{field} is required")
        if cap is not None and len(out) > cap:
            raise VxValidationError(op, f"max {cap} per call — got {len(out)}")
        return out

    # ── leads: the pool ──

    async def search_leads(self, filters: dict[str, Any] | None = None, *,
                           result_type: str = "person", cursor: str = "",
                           limit: int = 25, sort_field: str = "",
                           sort_desc: bool = True) -> dict[str, Any]:
        """Search the global pool. Returns the ``data`` envelope:
        ``items``, ``total``, ``total_display``, ``total_is_estimate``,
        ``next_cursor``, ``search_backend`` and ``sort``.

        Three things the caller must get right:

        * **Render ``data["sort"]``, not the sort you asked for.** An unknown
          field degrades to ``score`` desc server-side, silently. The echo is
          the sort that was actually applied.
        * **``total`` is capped at 10,000.** When ``total_is_estimate`` is true
          render ``total_display`` (``"10,000+"``) — never the raw number.
        * **``next_cursor`` is opaque.** Do not parse it, do not build one, and
          do not carry one across a sort or filter change: a keyset position is
          only meaningful inside the ordering it came from, so reusing it after
          a re-sort compares the wrong column and silently drops or repeats
          rows. Start a changed search from ``cursor=""``.

        Every address in ``items`` is masked unless that row's
        ``email_revealed`` is true. ``limit`` is clamped to
        :attr:`MAX_PAGE_SIZE`, matching the server.
        """
        if result_type not in ("person", "company"):
            raise VxValidationError("salesshift.search_leads",
                                    "result_type must be 'person' or 'company'")
        body: dict[str, Any] = {
            "filters": dict(filters or {}),
            "result_type": result_type,
            "cursor": cursor or "",
            "limit": max(1, min(int(limit), self.MAX_PAGE_SIZE)),
        }
        if sort_field:
            body["sort"] = {"field": sort_field, "desc": bool(sort_desc)}
        out = await self._leads("POST", "/leads/search",
                                op="salesshift.search_leads", json_body=body)
        return out.get("data") or {}

    async def search_all_leads(self, filters: dict[str, Any] | None = None, *,
                               result_type: str = "person",
                               page_size: int = MAX_PAGE_SIZE,
                               sort_field: str = "", sort_desc: bool = True,
                               max_pages: int = 100
                               ) -> AsyncIterator[dict[str, Any]]:
        """Walk :meth:`search_leads` to exhaustion, yielding one item at a time.

        Follows ``next_cursor`` until the server stops issuing one. Filters and
        sort are snapshotted at the first page and held fixed for the whole
        walk — that is not a convenience, it is the cursor rule: a keyset
        position taken under one ordering is meaningless under another.

        **Bounded.** Stops after ``max_pages`` pages even if a cursor remains.
        The default of 100 pages x 100 rows is 10,000 — the same ceiling the
        server counts to — so an unfiltered walk cannot become an unbounded
        crawl of the pool. Raise it deliberately, or narrow ``filters``.

        Reads nothing metered: every address yielded is still masked unless
        this org has already revealed that row. Iterating does not spend quota.

        Example::

            async for lead in c.salesshift.search_all_leads(
                    {"seniorities": ["director"], "countries": ["AU"]},
                    sort_field="score"):
                print(lead["full_name"], lead["email"])   # may be j•••@acme.com
        """
        if max_pages < 1:
            raise VxValidationError("salesshift.search_all_leads",
                                    "max_pages must be at least 1")
        pinned = dict(filters or {})
        cursor = ""
        seen: set[str] = set()
        for _ in range(max_pages):
            page = await self.search_leads(
                pinned, result_type=result_type, cursor=cursor,
                limit=page_size, sort_field=sort_field, sort_desc=sort_desc)
            for item in (page.get("items") or []):
                yield item
            cursor = page.get("next_cursor") or ""
            # A cursor that does not advance means the page is repeating —
            # keep walking and this becomes an infinite loop that also spends
            # the caller's rate budget.
            if not cursor or cursor in seen:
                return
            seen.add(cursor)

    async def lead_facets(self, filters: dict[str, Any] | None = None) -> dict[str, Any]:
        """Counts per seniority / department / country / email_status for the
        given filters. Pool-wide and tenant-blind — the number of Directors in
        the pool is the same for every org — so nothing here is masked or
        metered."""
        out = await self._leads("POST", "/leads/facets",
                                op="salesshift.lead_facets",
                                json_body={"filters": dict(filters or {})})
        return out.get("data") or {}

    async def reveal_quota(self) -> dict[str, Any]:
        """``{used, allowance, remaining, display}`` for the current period.

        Free to call. Show it **before** any batch that reveals — that is the
        only moment the cost of :meth:`convert_from_pool` is still avoidable.
        """
        out = await self._leads("GET", "/leads/quota", op="salesshift.reveal_quota")
        return out.get("data") or {}

    async def reveal_lead(self, pool_person_id: str) -> dict[str, Any]:
        """Un-mask one person. **Spends one reveal from the metered allowance**
        (free if this org already revealed this row).

        Returns ``{pool_id, email, phone, linkedin_url, quota}`` — the returned
        ``quota`` is authoritative; render it rather than decrementing a local
        counter.

        Raises :class:`VxLeadQuotaExhaustedError` (402) when the allowance is
        spent — **nothing was charged**; :class:`VxLeadErasedError` (410) when
        the person was erased, which is terminal; and ``VxNotFoundError`` (404)
        when the id is not in the pool.
        """
        if not pool_person_id:
            raise VxValidationError("salesshift.reveal_lead",
                                    "pool_person_id is required")
        out = await self._leads("POST", "/leads/reveal",
                                op="salesshift.reveal_lead",
                                json_body={"pool_person_id": str(pool_person_id)})
        return out.get("data") or {}

    async def save_leads(self, pool_person_ids: Iterable[str]) -> dict[str, Any]:
        """Copy pool rows into this org's saved leads. Max 200 per call.

        A **snapshot**, not a reference: the pool is re-crawled continuously and
        a saved list must not mutate under the person who qualified it. Saving
        is free and reveals nothing — a row saved while masked keeps
        ``email=null`` until it is revealed.

        Returns the flat ``{success, saved, already_saved}`` body.
        """
        ids = self._ids("salesshift.save_leads", "pool_person_ids",
                        pool_person_ids, cap=self.MAX_BATCH)
        return await self._leads("POST", "/leads/save",
                                 op="salesshift.save_leads",
                                 json_body={"pool_person_ids": ids})

    async def get_pool_person(self, pool_id: str) -> dict[str, Any]:
        """Full detail for one pool person, plus this org's relationship to
        them (``email_revealed``, ``saved_lead_id``, ``existing_contact_id``).

        Masking applies exactly as it does in search — a detail view is not a
        back door around the meter. Raises :class:`VxLeadErasedError` on 410.
        """
        if not pool_id:
            raise VxValidationError("salesshift.get_pool_person",
                                    "pool_id is required")
        out = await self._leads("GET", f"/leads/pool/{urllib.parse.quote(str(pool_id))}",
                                op="salesshift.get_pool_person")
        return out.get("data") or {}

    async def get_pool_company(self, company_id: str) -> dict[str, Any]:
        """A pool company with its people split into ``new_prospects`` and
        ``existing_contacts`` — what is left to work versus what this org
        already owns, so nobody re-buys a record they have."""
        if not company_id:
            raise VxValidationError("salesshift.get_pool_company",
                                    "company_id is required")
        out = await self._leads("GET", f"/leads/company/{urllib.parse.quote(str(company_id))}",
                                op="salesshift.get_pool_company")
        return out.get("data") or {}

    # ── leads: the tenant's saved copies ──

    async def list_leads(self, status: str = "", *, limit: int = 100) -> list[dict[str, Any]]:
        """This org's saved leads. Still not mailable.

        Each row carries both ``email`` (null until revealed) and
        ``email_masked`` + ``has_email`` from the live pool row, so a lead saved
        before being revealed reads as "not paid for yet" rather than "has no
        address" — the difference between a good prospect and a skipped one.
        """
        q: dict[str, Any] = {"limit": max(1, min(int(limit), self.MAX_LIST_LIMIT))}
        if status:
            q["status"] = status
        out = await self._leads("GET", "/leads?" + urllib.parse.urlencode(q),
                                op="salesshift.list_leads")
        return list(out.get("data") or [])

    async def get_lead(self, lead_id: str) -> dict[str, Any]:
        """One saved lead, the live pool row behind it (``pool``), and ``drift``
        — the fields that have changed since the snapshot was taken. A non-empty
        ``drift`` is the warning you want before working the list, not after a
        bounce."""
        if not lead_id:
            raise VxValidationError("salesshift.get_lead", "lead_id is required")
        out = await self._leads("GET", f"/leads/{urllib.parse.quote(str(lead_id))}",
                                op="salesshift.get_lead")
        return out.get("data") or {}

    async def update_lead(self, lead_id: str, *, status: str | None = None,
                          score: int | None = None, notes: str | None = None,
                          tags: list[str] | None = None,
                          disqualify_reason: str | None = None,
                          owner_id: str | None = None) -> dict[str, Any]:
        """Patch a saved lead. Only the arguments you pass are sent — ``None``
        means "leave it alone", never "clear it"."""
        if not lead_id:
            raise VxValidationError("salesshift.update_lead", "lead_id is required")
        patch: dict[str, Any] = {}
        for key, value in (("status", status), ("score", score), ("notes", notes),
                           ("tags", tags), ("disqualify_reason", disqualify_reason),
                           ("owner_id", owner_id)):
            if value is not None:
                patch[key] = value
        if not patch:
            raise VxValidationError("salesshift.update_lead",
                                    "nothing to update — pass at least one field")
        out = await self._leads("PATCH", f"/leads/{urllib.parse.quote(str(lead_id))}",
                                op="salesshift.update_lead", json_body=patch)
        return out.get("data") or {}

    # ── leads: convert — the one-way gate into mailable Contacts ──

    async def convert_lead(self, lead_id: str, *,
                           lifecycle_stage: str = "lead") -> dict[str, Any]:
        """Saved lead → Contact. **The moment a record becomes mailable**, and
        the only supported route there: this is where consent metadata is
        written, which is why nothing else may feed a sending path.

        The lead row is kept as an audit trail, never moved. Returns the flat
        body: ``{success, contact_id, reused_existing_contact}``, or
        ``{success, already_converted: true, contact_id}`` when it had already
        been converted — check ``already_converted`` before reporting a new one.

        400 (``VxValidationError``) means either "reveal the address first" or
        "this record was erased and cannot be converted" — the ``detail`` says
        which, so surface it verbatim rather than as a generic failure.
        """
        if not lead_id:
            raise VxValidationError("salesshift.convert_lead", "lead_id is required")
        return await self._leads(
            "POST", f"/leads/{urllib.parse.quote(str(lead_id))}/convert",
            op="salesshift.convert_lead",
            json_body={"lifecycle_stage": lifecycle_stage})

    async def bulk_convert_leads(self, lead_ids: Iterable[str]) -> dict[str, Any]:
        """Many saved leads → Contacts.

        Spends no quota — it converts only leads whose address this org already
        owns. Returns ``{success, converted, skipped_no_email,
        already_converted}``; render every bucket, not just ``converted``.
        :meth:`describe_convert` does that for you.
        """
        # No cap: the server does not impose one here, and inventing a
        # client-side limit would reject a batch the API would have accepted.
        ids = self._ids("salesshift.bulk_convert_leads", "lead_ids", lead_ids)
        return await self._leads("POST", "/leads/bulk-convert",
                                 op="salesshift.bulk_convert_leads",
                                 json_body={"lead_ids": ids})

    async def convert_from_pool(self, pool_person_ids: Iterable[str], *,
                                reveal_if_needed: bool = True,
                                lifecycle_stage: str = "lead") -> dict[str, Any]:
        """Pool → Contact in one step: save, reveal if needed, convert. Max 200.

        **This spends metered quota**, one reveal per not-yet-revealed id, up to
        the remaining allowance. Call :meth:`reveal_quota` first and show the
        user what the batch will cost — the cost is only avoidable before the
        call. Pass ``reveal_if_needed=False`` to convert only rows already
        revealed and spend nothing; the rest come back as ``skipped_no_quota``.

        The response **accounts for every id passed in**::

            converted, revealed_now, already_converted,
            skipped_no_quota, skipped_no_email, skipped_erased,
            contact_ids, quota

        Printing only ``converted`` hides a partial spend, which is exactly how
        trust in the meter is lost. Pass the whole report to
        :meth:`describe_convert`.

        When the allowance runs out mid-batch the server converts what it can
        and reports the rest — it does not raise 402. A 402 here would mean the
        request was refused outright.
        """
        ids = self._ids("salesshift.convert_from_pool", "pool_person_ids",
                        pool_person_ids, cap=self.MAX_BATCH)
        return await self._leads("POST", "/leads/convert-from-pool",
                                 op="salesshift.convert_from_pool",
                                 json_body={"pool_person_ids": ids,
                                            "reveal_if_needed": bool(reveal_if_needed),
                                            "lifecycle_stage": lifecycle_stage})

    @staticmethod
    def describe_convert(report: dict[str, Any]) -> str:
        """Render a convert report so **every** id is accounted for.

        Takes the body of :meth:`convert_from_pool` or
        :meth:`bulk_convert_leads` and returns lines a human can read. Every
        non-zero outcome is listed, never just the successes: a partial spend
        reported as a success is the failure mode rule 4 of the client contract
        exists to prevent. Buckets the endpoint did not return are omitted, and
        a zeroed report still says so explicitly rather than printing nothing.

        Example::

            report = await c.salesshift.convert_from_pool(ids)
            print(c.salesshift.describe_convert(report))
            # 4 converted, 1 already a contact, 2 skipped: no reveal quota left
            # 4 reveals spent. Quota now 7 / 200 (193 left).
        """
        report = report or {}
        labels = [
            ("converted", "{n} converted"),
            ("already_converted", "{n} already a contact"),
            ("skipped_no_quota", "{n} skipped: no reveal quota left"),
            ("skipped_no_email", "{n} skipped: no email address on record"),
            ("skipped_erased", "{n} skipped: erased at the person's request"),
        ]
        parts = [tpl.format(n=int(report.get(key) or 0))
                 for key, tpl in labels
                 if key in report and int(report.get(key) or 0) > 0]
        lines = [", ".join(parts) if parts else "Nothing converted — 0 of 0."]

        revealed = int(report.get("revealed_now") or 0)
        if revealed:
            lines.append(f"{revealed} reveal{'s' if revealed != 1 else ''} spent.")
        quota = report.get("quota") or {}
        if quota:
            display = (quota.get("display")
                       or f"{quota.get('used')} / {quota.get('allowance')}")
            lines.append(f"Quota now {display} ({quota.get('remaining')} left).")
        return "\n".join(lines)

    # ── leads: erasure ──

    async def request_erasure(self, email: str = "", *, linkedin_url: str = "",
                              note: str = "", reason: str = "gdpr_erasure",
                              confirm: bool = False) -> dict[str, Any]:
        """Right to be forgotten. **Global and irreversible.**

        This does not remove the person from *your* org — it deactivates them in
        the shared pool, records a hash so no future crawl can resurrect them,
        and strips the address from every tenant's saved copy. **Every tenant,
        not just the caller's.** There is no undo.

        Because of that, ``confirm=True`` is mandatory: an erasure must be an
        explicit act, never something a loop does by accident. Anything you
        build on top of this must say "all tenants" and "cannot be undone"
        before it calls.

        Identify the person by ``email`` or ``linkedin_url`` (at least one).
        Returns ``{success, pool_rows_erased, saved_leads_flagged,
        already_recorded}`` — ``already_recorded`` true means the erasure was
        already on file, which is a success, not a no-op to hide.
        """
        if not confirm:
            raise VxValidationError(
                "salesshift.request_erasure",
                "erasure is GLOBAL (every tenant, not just yours) and cannot be "
                "undone — pass confirm=True to proceed")
        if not email and not linkedin_url:
            raise VxValidationError("salesshift.request_erasure",
                                    "email or linkedin_url is required")
        body: dict[str, Any] = {"reason": reason or "gdpr_erasure"}
        if email:
            body["email"] = email
        if linkedin_url:
            body["linkedin_url"] = linkedin_url
        if note:
            body["note"] = note
        return await self._leads("POST", "/leads/erasure",
                                 op="salesshift.request_erasure", json_body=body)

    async def enrich_company(self, *, company_id: str = "", domain: str = "",
                       ) -> dict[str, Any]:
        """Crawl a company's own website and fold what it finds into the pool.

        The only call here that WRITES to the shared pool, so what it refuses
        to do matters as much as what it does:

        * **Gaps only.** An existing description, keyword set or address is
          never replaced by crawl output.
        * **Erasure is checked before every insert**, so a crawl cannot bring
          back someone who asked to be forgotten.
        * **Shared mailboxes are not people.** ``sales@``, ``info@``,
          ``announce@`` and their regional variants are dropped, and a name is
          derived from an address only when the local part plausibly is one.
        * **Everything found is unverified**, and no reveal quota is spent.

        Pass ``company_id`` for a company already in the pool, or ``domain``
        for one that is not — in which case the crawl CREATES the company and
        any people it finds.

        Read ``crawled`` in the result first. When it is 0, ``note`` says why
        (blocked by a CDN, nothing readable, a server error) and nothing was
        written. Slow by nature: it fetches up to a dozen pages, and the
        server's own ceiling is 90 seconds.
        """
        domain = (domain or "").strip().lower()
        for prefix in ("https://", "http://"):
            if domain.startswith(prefix):
                domain = domain[len(prefix):]
        domain = domain.split("/")[0]
        if not company_id and not domain:
            raise VxValidationError("salesshift.enrich_company",
                                    "company_id or domain is required")
        body: dict[str, Any] = {}
        if company_id:
            body["company_id"] = company_id
        if domain:
            body["domain"] = domain
        return await self._leads("POST", "/api/v1/salesshift/leads/enrich",
                           op="salesshift.enrich_company", json_body=body)

    # ── leads: saved searches ──

    async def list_saved_searches(self) -> list[dict[str, Any]]:
        """This org's saved lead searches (``{id, name, filters, is_shared}``)."""
        out = await self._leads("GET", "/lead-searches",
                                op="salesshift.list_saved_searches")
        return list(out.get("data") or [])

    async def save_search(self, name: str, filters: dict[str, Any] | None = None,
                          *, is_shared: bool = False) -> dict[str, Any]:
        """Save a filter set by name. Stores the filters only — never a cursor,
        which is a position inside one ordering of one result set and means
        nothing the next time the search is run."""
        if not name or not name.strip():
            raise VxValidationError("salesshift.save_search", "name is required")
        out = await self._leads("POST", "/lead-searches",
                                op="salesshift.save_search",
                                json_body={"name": name.strip(),
                                           "filters": dict(filters or {}),
                                           "is_shared": bool(is_shared)})
        return out.get("data") or {}


# ── Async client ───────────────────────────────────────────────────────

class AsyncClient:
    """Async equivalent of vxsdk.Client. Use as an ``async with`` context manager.

    Constructed via ``AsyncClient(api_key=...)`` or ``await AsyncClient.load_from_vxcli()``.
    The ``__aenter__``/``__aexit__`` pair owns the underlying ``httpx.AsyncClient``,
    so connection pooling is shared across all calls within the block.
    """

    def __init__(self, *, api_key: str | None = None, username: str | None = None,
                 access_token: str = "", refresh_token: str = "",
                 infinity_url: str = DEFAULT_INFINITY_URL,
                 node_url: str = "",
                 user_agent: str = f"vxsdk-py-async/{__version__}",
                 http_client: httpx.AsyncClient | None = None):
        if not api_key and not access_token:
            raise VxError("vxsdk_async.AsyncClient",
                          "no credentials: pass api_key= or access_token=")
        if api_key:
            self._validate_api_key(api_key)

        self.api_key = api_key or ""
        self.username = username or ""
        self.access_token = access_token
        self.refresh_token = refresh_token
        self.infinity_url = infinity_url.rstrip("/")
        self.node_url = node_url.rstrip("/")
        self.user_agent = user_agent

        self._whoami = Whoami(username=username or "")
        self._lock = asyncio.Lock()
        self._owned_http = http_client is None
        self._http: httpx.AsyncClient | None = http_client

        self.cicd = _AsyncCICD(self)
        self.sessions = _AsyncSessions(self)
        self.install = _AsyncInstall(self)
        self.deploy = _AsyncDeploy(self)
        self.marketplace = _AsyncMarketplace(self)
        self.cloud = _AsyncCloud(self)
        self.nodes = _AsyncNodes(self)
        self.vxcomputer = _AsyncVxComputer(self)
        self.robotic = _AsyncRobotic(self)
        self.vxchrono = _AsyncVxChrono(self)
        self.workspace = _AsyncWorkspace(self)
        self.workflow = _AsyncWorkflow(self)
        self.salesshift = _AsyncSalesShift(self)

    @classmethod
    async def load_from_vxcli(cls) -> "AsyncClient":
        """Read ~/.vxcloud/credentials.json (the file vxcli writes)."""
        f = _load_credentials_file()
        return cls(
            api_key=f.get("api_key") or None,
            username=f.get("username"),
            access_token=f.get("access_token", ""),
            refresh_token=f.get("refresh_token", ""),
            infinity_url=f.get("base_url") or DEFAULT_INFINITY_URL,
            node_url=f.get("node_url", ""),
        )

    async def __aenter__(self) -> "AsyncClient":
        if self._http is None:
            self._http = httpx.AsyncClient(timeout=httpx.Timeout(DEFAULT_TIMEOUT))
            self._owned_http = True
        return self

    async def __aexit__(self, exc_type, exc, tb) -> None:
        if self._owned_http and self._http is not None:
            await self._http.aclose()
            self._http = None

    @property
    def whoami(self) -> Whoami:
        return self._whoami

    async def authenticate(self) -> None:
        await self._refresh()

    # ── internals ──

    def _validate_api_key(self, key: str) -> None:
        if not key.startswith("xc_"):
            raise VxAuthError("vxsdk_async.AsyncClient", "api key must start with xc_")
        parts = key.split("_", 2)
        if len(parts) != 3:
            raise VxAuthError("vxsdk_async.AsyncClient", "api key format: xc_<env>_<token>")
        if parts[1] not in ("dev", "test", "live"):
            raise VxAuthError("vxsdk_async.AsyncClient", "api key environment must be dev|test|live")
        if len(parts[2]) < 16:
            raise VxAuthError("vxsdk_async.AsyncClient", "api key token segment too short")

    def _ssh_fields(self, host, ssh_user, key_pair_name, workspace_user, organization):
        if not (host and ssh_user and key_pair_name):
            raise ValueError("host, ssh_user, and key_pair_name are required")
        user = workspace_user or self.username
        org = organization or user
        return {"hostname": host, "ssh_username": ssh_user,
                "key_pair_name": key_pair_name, "username": user, "organization": org}

    def _auth_headers(self) -> dict[str, str]:
        h: dict[str, str] = {}
        if self.access_token:
            h["Authorization"] = f"Bearer {self.access_token}"
        if self.api_key:
            h["X-API-Key"] = self.api_key
        return h

    async def _refresh(self) -> None:
        if not self.api_key:
            raise VxAuthError("vxsdk_async._refresh",
                              "no api key configured — cannot refresh JWT")
        async with self._lock:
            url = self.infinity_url + "/api/v1/auth/developer/keys/login"
            assert self._http is not None, "use AsyncClient inside `async with`"
            try:
                resp = await self._http.post(url,
                    json={"api_key": self.api_key, "username": self.username},
                    headers={"Accept": "application/json"})
            except httpx.RequestError as e:
                raise VxNetworkError("vxsdk_async._refresh", "transport", cause=e) from e
            if resp.status_code != 200:
                raise VxAuthError("vxsdk_async._refresh",
                                  "exchange api key for jwt",
                                  resp.status_code, resp.text[:200])
            data = resp.json()
            self.access_token = data.get("access", "")
            self.refresh_token = data.get("refresh", "")
            user = data.get("user") or {}
            self.username = user.get("username", self.username)
            self._whoami = Whoami(
                username=user.get("username", self.username),
                email=user.get("email", ""),
                organization=(user.get("organization") or {}).get("name", "")
                    if user.get("organization") else "",
                workspace=(user.get("workspace") or {}).get("name", "")
                    if user.get("workspace") else "",
            )

    async def _do(self, method: str, url: str, *, op: str,
                  headers: dict[str, str], content: bytes | None,
                  timeout: float) -> tuple[int, dict[str, str], bytes]:
        assert self._http is not None, "use AsyncClient inside `async with`"
        h = dict(headers)
        h.setdefault("Accept", "application/json")
        h["User-Agent"] = self.user_agent
        h["vx-request-id"] = uuid.uuid4().hex
        h.update(self._auth_headers())

        max_retries = 3
        refreshed = False
        last_err: Exception | None = None
        for attempt in range(max_retries + 1):
            try:
                resp = await self._http.request(
                    method, url, headers=h, content=content,
                    timeout=httpx.Timeout(timeout))
            except httpx.RequestError as e:
                last_err = VxNetworkError(op, "transport", cause=e)
                if attempt >= max_retries:
                    raise last_err from e
                await asyncio.sleep(min(0.2 * (2 ** attempt), 5.0))
                continue

            status = resp.status_code
            raw = resp.content
            if 200 <= status < 300:
                return status, dict(resp.headers), raw

            if status == 401 and not refreshed and self.api_key:
                refreshed = True
                try:
                    await self._refresh()
                    h.update(self._auth_headers())
                    continue
                except VxError:
                    pass

            try:
                detail = raw.decode("utf-8", "replace")[:800]
            except Exception:
                detail = ""
            retry_after = 0
            if status == 429:
                ra = resp.headers.get("Retry-After")
                if ra and ra.isdigit():
                    retry_after = int(ra)
            err = _from_http(op, status, _http_reason(status), detail,
                             retry_after=retry_after)
            if attempt < max_retries and _is_retryable(err):
                last_err = err
                await asyncio.sleep(min(0.2 * (2 ** attempt), 5.0))
                continue
            raise err

        if last_err:
            raise last_err
        raise VxError(op, "exhausted retries")

    async def _json(self, method: str, url: str, *, op: str,
                    json_body: Any | None = None,
                    timeout: float = DEFAULT_TIMEOUT) -> Any:
        headers: dict[str, str] = {}
        content: bytes | None = None
        if json_body is not None:
            headers["Content-Type"] = "application/json"
            content = json.dumps(json_body).encode("utf-8")
        _status, _hdrs, raw = await self._do(method, url, op=op,
            headers=headers, content=content, timeout=timeout)
        if not raw:
            return {}
        try:
            data = json.loads(raw.decode("utf-8"))
        except Exception as e:
            raise VxError(op, "decode response", cause=e) from e
        if isinstance(data, list):
            return {"data": data}
        return data

    async def _multipart(self, url: str, fields: dict[str, str],
                         files: list[tuple[str, str, bytes, str]],
                         *, op: str, timeout: float) -> dict[str, Any]:
        body, content_type = _multipart_body(fields, files)
        _status, _hdrs, raw = await self._do("POST", url, op=op,
            headers={"Content-Type": content_type}, content=body,
            timeout=timeout)
        if not raw:
            return {}
        try:
            return json.loads(raw.decode("utf-8"))
        except Exception as e:
            raise VxError(op, "decode response", cause=e) from e


# ── Brand aliases (mirror vxsdk.py) ─────────────────────────────────────
# All three resolve to AsyncClient — additive only:
#     vxsdk_async.AsyncClient.load_from_vxcli()   # canonical
#     vxsdk_async.VxCloud.load_from_vxcli()       # PascalCase brand
#     vxsdk_async.vxcloud.load_from_vxcli()       # lowercase brand
VxCloud = AsyncClient
vxcloud = AsyncClient
