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
from typing import Any, Iterable

import httpx

from vxsdk import (
    DEFAULT_INFINITY_URL, DEFAULT_LONG_TIMEOUT, DEFAULT_TIMEOUT,
    STACK_TARGETS, VxAuthError, VxError, VxNetworkError,
    Whoami, _from_http, _http_reason, _is_retryable, _load_credentials_file,
    _multipart_body, __version__,
)

__all__ = ["AsyncClient", "VxCloud", "vxcloud"]


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
