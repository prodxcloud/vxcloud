"""vxsdk — Python SDK for the vxcloud platform (preview).

Single-file, stdlib-only port of the Go SDK in ../../sdk/. Same wire
contract, same auth model, same auto-refresh-on-401 behavior. Intended
as the Python equivalent of `github.com/prodxcloud/vxcloud` for scripts,
notebooks, and Python services.

Usage:

    import vxsdk

    c = vxsdk.Client.load_from_vxcli()             # uses ~/.vxcloud/credentials.json
    # OR
    c = vxsdk.Client(api_key="xc_dev_...", username="alice")

    pipelines = c.cicd.pipelines.list()
    print(pipelines)

    result = c.deploy.container(
        host="54.197.71.181", ssh_user="ubuntu", key_pair_name="AWSPRODKEY1.PEM",
        image="grafana/grafana:latest", name="grafana", ports=["3000:3000"],
    )
    print(result["session_id"])

This module pins to the live API surface verified during preview testing:
- /api/v1/auth/developer/keys/login          (auth exchange / refresh)
- /api/v1/auth/nodes/                        (tenant node listing)
- /api/v2/cicd/pipelines                     (CI/CD)
- /api/v2/marketplace/{agents,models,templates}
- /api/v2/marketplace/provision              (Terraform solutions)
- /api/v2/tenant/install/script              (custom-script install)
- /api/v2/tenant/provision/docker-compose/custom (compose install)
- /api/v2/tenant/container/deploy            (single-container deploy)
- /api/v2/infrastructure/services/<stack>/deploy (stack deploys)
- /api/v2/tenant/provision/{storage,security,vm,kubernetes,databases,...}

No third-party dependencies. Tested on Python 3.10+.
"""

from __future__ import annotations

import json
import os
import platform
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass, field
from typing import Any, Iterable

__version__ = "2026.6.10"

DEFAULT_INFINITY_URL = "https://api.vxcloud.io"
DEFAULT_TIMEOUT = 30
DEFAULT_LONG_TIMEOUT = 600  # for installs / deploys


# ── Errors ─────────────────────────────────────────────────────────────

class VxError(Exception):
    """Base SDK error. Use the subclass types to branch."""

    def __init__(self, op: str, message: str, http_status: int = 0, detail: str = "", cause: Exception | None = None):
        self.op = op
        self.http_status = http_status
        self.message = message
        self.detail = detail
        self.cause = cause
        if http_status and detail:
            super().__init__(f"{op}: {http_status} {message} — {detail}")
        elif http_status:
            super().__init__(f"{op}: {http_status} {message}")
        elif cause:
            super().__init__(f"{op}: {message}: {cause}")
        else:
            super().__init__(f"{op}: {message}")


class VxAuthError(VxError):
    """401/403 — credential rejected or invalid in shape."""


class VxValidationError(VxError):
    """400/422 — request payload is malformed."""


class VxNotFoundError(VxError):
    """404 — resource does not exist."""


class VxRateLimitError(VxError):
    """429 — quota exceeded. retry_after is seconds; 0 if not advertised."""

    def __init__(self, *args: Any, retry_after: int = 0, **kw: Any):
        super().__init__(*args, **kw)
        self.retry_after = retry_after


class VxServerError(VxError):
    """5xx — upstream failure. Safe to retry with backoff."""


class VxNetworkError(VxError):
    """Transport failure (DNS, TCP, TLS, timeout). Safe to retry."""


def _from_http(op: str, status: int, message: str, detail: str, retry_after: int = 0) -> VxError:
    if status in (401, 403):
        return VxAuthError(op, message, status, detail)
    if status in (400, 422):
        return VxValidationError(op, message, status, detail)
    if status == 404:
        return VxNotFoundError(op, message, status, detail)
    if status == 429:
        return VxRateLimitError(op, message, status, detail, retry_after=retry_after)
    if status >= 500:
        return VxServerError(op, message, status, detail)
    return VxError(op, message, status, detail)


def _is_retryable(err: BaseException) -> bool:
    return isinstance(err, (VxNetworkError, VxServerError, VxRateLimitError))


# ── Credentials file (vxcli compat) ────────────────────────────────────

def _credentials_path() -> str:
    home = os.path.expanduser("~") if platform.system() != "Windows" else os.environ.get("USERPROFILE", os.path.expanduser("~"))
    return os.path.join(home, ".vxcloud", "credentials.json")


def _load_credentials_file() -> dict[str, Any]:
    p = _credentials_path()
    if not os.path.exists(p):
        raise VxError("vxsdk.load_from_vxcli", f"credentials file not found: {p} (run `vxcli auth login` first)")
    with open(p, "r", encoding="utf-8") as f:
        return json.load(f)


# ── Multipart encoder (stdlib) ─────────────────────────────────────────

def _zip_directory(directory: str) -> bytes:
    """Bundle a local directory into an in-memory zip and return its bytes.

    Skips common build artifacts: .git, target, node_modules, __pycache__,
    .terraform, dist, build. Used by deploy.stack(path=...) for zip uploads.
    """
    import io
    import zipfile

    skip = {".git", "target", "node_modules", "__pycache__", ".terraform",
            "dist", "build", ".venv", "venv", ".idea", ".vscode"}

    abs_dir = os.path.abspath(directory)
    if not os.path.isdir(abs_dir):
        raise ValueError(f"deploy.stack: path not a directory: {directory}")

    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        for root, dirs, fnames in os.walk(abs_dir):
            dirs[:] = [d for d in dirs if d not in skip]
            for fn in fnames:
                full = os.path.join(root, fn)
                rel = os.path.relpath(full, abs_dir)
                zf.write(full, rel)
    return buf.getvalue()


def _multipart_body(fields: dict[str, str], files: list[tuple[str, str, bytes, str]]) -> tuple[bytes, str]:
    """Build a multipart/form-data body.

    files: list of (field_name, filename, content_bytes, content_type)
    Returns (body, content_type_header).
    """
    boundary = uuid.uuid4().hex
    parts: list[bytes] = []
    for name, value in fields.items():
        parts.append(f"--{boundary}\r\n".encode())
        parts.append(f'Content-Disposition: form-data; name="{name}"\r\n\r\n'.encode())
        parts.append(str(value).encode("utf-8"))
        parts.append(b"\r\n")
    for field_name, filename, content, content_type in files:
        parts.append(f"--{boundary}\r\n".encode())
        parts.append(
            f'Content-Disposition: form-data; name="{field_name}"; filename="{filename}"\r\n'.encode()
        )
        parts.append(f"Content-Type: {content_type}\r\n\r\n".encode())
        parts.append(content)
        parts.append(b"\r\n")
    parts.append(f"--{boundary}--\r\n".encode())
    return b"".join(parts), f"multipart/form-data; boundary={boundary}"


# ── Internal HTTP helpers ──────────────────────────────────────────────

def _request(method: str, url: str, headers: dict[str, str], body: bytes | None, timeout: int) -> tuple[int, dict[str, str], bytes]:
    req = urllib.request.Request(url, method=method, data=body)
    for k, v in headers.items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read() or b""
    except urllib.error.URLError as e:
        raise VxNetworkError("transport", "network", cause=e) from e
    except TimeoutError as e:
        raise VxNetworkError("transport", "timeout", cause=e) from e


# ── Resource modules ───────────────────────────────────────────────────

class _Resource:
    """Base for resource modules. Holds a back-reference to the Client."""

    def __init__(self, client: "Client"):
        self.client = client


class Pipelines(_Resource):
    def list(self) -> list[dict[str, Any]]:
        body = self.client._json("GET", self.client.node_url + "/api/v2/cicd/pipelines",
                                 op="cicd.pipelines.list")
        return body.get("data", [])

    def show(self, pipeline_id: str) -> dict[str, Any]:
        body = self.client._json("GET", f"{self.client.node_url}/api/v2/cicd/pipelines/{pipeline_id}",
                                 op="cicd.pipelines.show")
        return body.get("data", body)

    def trigger(self, pipeline_id: str, branch: str = "main") -> dict[str, Any]:
        body = self.client._json(
            "POST", f"{self.client.node_url}/api/v2/cicd/pipelines/{pipeline_id}/trigger",
            op="cicd.pipelines.trigger", json_body={"branch": branch},
        )
        return body.get("data", body)


class Builds(_Resource):
    def show(self, build_id: str) -> dict[str, Any]:
        body = self.client._json("GET", f"{self.client.node_url}/api/v2/cicd/builds/{build_id}",
                                 op="cicd.builds.show")
        return body.get("data", body)


class CICD(_Resource):
    @property
    def pipelines(self) -> Pipelines:
        return Pipelines(self.client)

    @property
    def builds(self) -> Builds:
        return Builds(self.client)


class Sessions(_Resource):
    """Session-level operations. A session is the unit of work created by
    a deploy/install — list, inspect, replay, fetch, or tear it down."""

    def list(self) -> list[dict[str, Any]]:
        query = urllib.parse.urlencode({"username": self.client.username})
        body = self.client._json(
            "GET",
            self.client.node_url + f"/api/v2/tenant/sessions?{query}",
            op="sessions.list",
        )
        if isinstance(body, list):
            return body
        return body.get("sessions") or body.get("files") or []

    def show(self, session_id: str) -> dict[str, Any]:
        """Show details and contents of a specific session."""
        if not session_id:
            raise ValueError("sessions.show: session_id is required")
        return self.client._json(
            "GET",
            self.client.node_url + f"/api/v2/tenant/sessions/{session_id}",
            op="sessions.show",
        )

    def apply(self, session_id: str) -> dict[str, Any]:
        """Re-run a previously planned deploy from a saved session."""
        if not session_id:
            raise ValueError("sessions.apply: session_id is required")
        return self.client._json(
            "POST",
            self.client.node_url + "/api/v2/tenant/sessions/apply",
            op="sessions.apply",
            json_body={"session_id": session_id},
        )

    def pull(self, session_id: str) -> dict[str, Any]:
        """Fetch terraform state and generated artifacts for a session."""
        if not session_id:
            raise ValueError("sessions.pull: session_id is required")
        return self.client._json(
            "POST",
            self.client.node_url + "/api/v2/tenant/sessions/pull",
            op="sessions.pull",
            json_body={"session_id": session_id},
        )

    def delete(self, session_id: str, force: bool = False) -> dict[str, Any]:
        """Tear down a previously-provisioned resource by session id."""
        if not session_id:
            raise ValueError("sessions.delete: session_id is required")
        return self.client._json(
            "POST",
            self.client.node_url + "/api/v2/tenant/sessions/delete",
            op="sessions.delete",
            json_body={"session_id": session_id, "force": force},
        )


# ── Services (lifecycle plane) ─────────────────────────────────────────

class _ServicesVM:
    """Host-level operations under client.services.vm.

    Each method maps 1:1 to a vxcli `services vm <verb>`. Backed by the
    multipart admin-action endpoint POST /api/v2/tenant/services/action
    with whitelisted action keys.
    """

    def __init__(self, services: "Services") -> None:
        self._svc = services

    def reboot(self, **ssh: Any) -> dict[str, Any]:
        """sudo reboot the remote host. Destructive — confirm upstream."""
        return self._svc._action("reboot", ssh=ssh, op="services.vm.reboot")

    def shutdown(self, **ssh: Any) -> dict[str, Any]:
        """sudo shutdown -h +1 the remote host. Destructive."""
        return self._svc._action("shutdown", ssh=ssh, op="services.vm.shutdown")

    def disk_cleanup(self, **ssh: Any) -> dict[str, Any]:
        """apt autoremove + apt clean + journalctl --vacuum-time=3d."""
        return self._svc._action("disk_cleanup", ssh=ssh, op="services.vm.disk_cleanup")

    def docker_cleanup(self, **ssh: Any) -> dict[str, Any]:
        """docker system prune -af --volumes."""
        return self._svc._action("docker_cleanup", ssh=ssh, op="services.vm.docker_cleanup")

    def restart_docker(self, **ssh: Any) -> dict[str, Any]:
        """sudo systemctl restart docker."""
        return self._svc._action("restart_docker", ssh=ssh, op="services.vm.restart_docker")

    def memory(self, **ssh: Any) -> dict[str, Any]:
        """free -h plus head of /proc/meminfo."""
        return self._svc._action("check_memory", ssh=ssh, op="services.vm.memory")

    def disk(self, **ssh: Any) -> dict[str, Any]:
        """df -hT plus largest dirs in /home, /opt, /var/lib."""
        return self._svc._action("check_disk_detailed", ssh=ssh, op="services.vm.disk")

    def list_services(self, **ssh: Any) -> dict[str, Any]:
        """systemctl list-units --type=service --state=running."""
        return self._svc._action("list_running_services", ssh=ssh, op="services.vm.list_services")

    def list_containers(self, **ssh: Any) -> dict[str, Any]:
        """docker ps -a (alias of services.list)."""
        return self._svc._action("list_docker_containers", ssh=ssh, op="services.vm.list_containers")

    def kill_port(self, port: int | str, **ssh: Any) -> dict[str, Any]:
        """sudo fuser -k <port>/tcp."""
        if not port:
            raise ValueError("services.vm.kill_port: port is required")
        return self._svc._action("kill_port", target=str(port), ssh=ssh, op="services.vm.kill_port")

    def stop_service(self, unit: str, **ssh: Any) -> dict[str, Any]:
        """sudo systemctl stop <unit>."""
        if not unit:
            raise ValueError("services.vm.stop_service: unit is required")
        return self._svc._action("stop_service", target=unit, ssh=ssh, op="services.vm.stop_service")


class Services(_Resource):
    """Lifecycle plane for already-running services on a remote VM.

    Mirrors `vxcli services` (services/cli/cmd/services.go). Container
    operations use JSON endpoints under /api/v2/tenant/container/* and
    /api/v2/tenant/docker/container/status. Host operations use the
    multipart admin-action endpoint /api/v2/tenant/services/action.

    SSH credentials accepted as keyword args:
        host, ssh_user, key_pair_name (or key_pair_location),
        workspace_user, organization
    """

    def __init__(self, client: "Client") -> None:
        super().__init__(client)
        self.vm = _ServicesVM(self)

    # ── container lifecycle (JSON) ─────────────────────────────────────

    def list(self, **ssh: Any) -> dict[str, Any]:
        """List Docker containers on the remote host."""
        return self._action("list_docker_containers", ssh=ssh, op="services.list")

    def status(self, name: str, **ssh: Any) -> dict[str, Any]:
        """Inspect a single container by name. Empty name lists all."""
        body = self.client._ssh_fields(**self._extract_ssh(ssh))
        if name:
            body["service_name"] = name
        return self.client._json(
            "POST",
            self.client.node_url + "/api/v2/tenant/docker/container/status",
            op="services.status",
            json_body=body,
        )

    def start(self, name: str, **ssh: Any) -> dict[str, Any]:
        """Start a stopped container by name."""
        return self._lifecycle("start", name, ssh)

    def stop(self, name: str, **ssh: Any) -> dict[str, Any]:
        """Stop a running container by name."""
        return self._lifecycle("stop", name, ssh)

    def remove(self, name: str, **ssh: Any) -> dict[str, Any]:
        """Stop and remove a container by name. Destructive — confirm upstream."""
        return self._lifecycle("remove", name, ssh)

    def restart(self, name: str, **ssh: Any) -> dict[str, Any]:
        """Stop, then start. The server has no native restart endpoint."""
        self._lifecycle("stop", name, ssh)
        return self._lifecycle("start", name, ssh)

    def logs(self, unit: str, tail: int = 50, **ssh: Any) -> dict[str, Any]:
        """Tail journalctl logs for a systemd unit (NOT docker logs)."""
        if not unit:
            raise ValueError("services.logs: unit is required")
        # tail is forward-compat — server template hard-codes -n 50 today.
        _ = tail
        return self._action("tail_logs", target=unit, ssh=ssh, op="services.logs")

    # ── internals ──────────────────────────────────────────────────────

    def _lifecycle(self, action: str, name: str, ssh: dict[str, Any]) -> dict[str, Any]:
        if action not in ("start", "stop", "remove"):
            raise ValueError(f"services: unsupported lifecycle action {action!r}")
        if not name:
            raise ValueError(f"services.{action}: container name is required")
        body = self.client._ssh_fields(**self._extract_ssh(ssh))
        body["container_name"] = name
        return self.client._json(
            "POST",
            self.client.node_url + f"/api/v2/tenant/container/{action}",
            op=f"services.{action}",
            json_body=body,
        )

    def _action(self, action: str, *, target: str = "", ssh: dict[str, Any], op: str) -> dict[str, Any]:
        fields = self.client._ssh_fields(**self._extract_ssh(ssh))
        fields["action"] = action
        if target:
            fields["target"] = target
        return self.client._multipart(
            self.client.node_url + "/api/v2/tenant/services/action",
            fields,
            [],
            op=op,
            timeout=DEFAULT_TIMEOUT,
        )

    @staticmethod
    def _extract_ssh(ssh: dict[str, Any]) -> dict[str, Any]:
        """Pluck the SSH-related keys from a kwargs bag and supply None
        for any unset optional field — the underlying _ssh_fields()
        helper takes those positions as required keyword args."""
        return {
            "host": ssh.get("host", ""),
            "ssh_user": ssh.get("ssh_user", ""),
            "key_pair_name": ssh.get("key_pair_name", ""),
            "workspace_user": ssh.get("workspace_user"),
            "organization": ssh.get("organization"),
        }


# ── Install ────────────────────────────────────────────────────────────

class Install(_Resource):
    def script(
        self,
        *,
        host: str,
        ssh_user: str,
        key_pair_name: str,
        script: bytes | str,
        script_name: str = "install.sh",
        args: Iterable[str] | None = None,
        env: Iterable[str] | None = None,
        workspace_user: str | None = None,
        organization: str | None = None,
        timeout: int = DEFAULT_LONG_TIMEOUT,
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
        return self.client._multipart(
            self.client.node_url + "/api/v2/tenant/install/script",
            fields, files, op="install.script", timeout=timeout,
        )

    def compose(
        self,
        *,
        host: str,
        ssh_user: str,
        key_pair_name: str,
        stack_name: str,
        compose: bytes | str,
        env_file: bytes | str | None = None,
        registry_slug: str | None = None,
        docker_user: str | None = None,
        docker_password: str | None = None,
        workspace_user: str | None = None,
        organization: str | None = None,
        timeout: int = DEFAULT_LONG_TIMEOUT,
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
        return self.client._multipart(
            self.client.node_url + "/api/v2/tenant/provision/docker-compose/custom",
            fields, [], op="install.compose", timeout=timeout,
        )


# ── Deploy ─────────────────────────────────────────────────────────────

# Each stack has its own form field names for the git URL and branch.
# react / nextjs took "repo_url"; everyone else took "git_url". The SDK
# normalizes the call site (callers always pass repo_url=, branch=).
@dataclass
class _StackTarget:
    path: str
    git_field: str = "git_url"
    branch_field: str = "git_branch"


STACK_TARGETS: dict[str, _StackTarget] = {
    "react":   _StackTarget("/api/v2/infrastructure/services/reactjs/deploy",      "repo_url", "branch"),
    "nextjs":  _StackTarget("/api/v2/infrastructure/services/nextjs/deploy",       "repo_url", "branch"),
    "nodejs":  _StackTarget("/api/v2/infrastructure/services/nodejs/deploy"),
    "fastapi": _StackTarget("/api/v2/infrastructure/services/fastapi/deploy"),
    "python":  _StackTarget("/api/v2/infrastructure/services/python/deploy"),
    "django":  _StackTarget("/api/v2/infrastructure/services/django/deploy"),
    "golang":  _StackTarget("/api/v2/infrastructure/services/golang/deploy"),
    "rust":    _StackTarget("/api/v2/infrastructure/services/rust/deploy"),
    "cpp":     _StackTarget("/api/v2/infrastructure/services/cpp/deploy"),
    "php":     _StackTarget("/api/v2/infrastructure/services/php/deploy"),
    "static":  _StackTarget("/api/v2/infrastructure/services/staticwebsite/deploy", "git_url", "git_branch"),
    "java":       _StackTarget("/api/v2/infrastructure/services/java/deploy"),
    "springboot": _StackTarget("/api/v2/infrastructure/services/springboot/deploy"),
}


class Deploy(_Resource):
    def container(
        self,
        *,
        host: str,
        ssh_user: str,
        key_pair_name: str,
        image: str,
        name: str | None = None,
        ports: list[str] | None = None,
        volumes: list[str] | None = None,
        env: list[str] | None = None,
        restart_policy: str = "unless-stopped",
        network: str | None = None,
        command: str | None = None,
        docker_user: str | None = None,
        docker_password: str | None = None,
        registry_slug: str | None = None,
        # HTTPS — when enable_ssl is set with a domain, the deploy installs
        # nginx + a Let's Encrypt cert in front of the first published port
        # once the container is healthy. Mirrors the Go SDK's EnableSSL /
        # Domain / SSLEmail (services/sdk/deploy/deploy.go).
        enable_ssl: bool = False,
        domain: str | None = None,
        ssl_email: str | None = None,
        workspace_user: str | None = None,
        organization: str | None = None,
        timeout: int = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        if not image:
            raise ValueError("deploy.container: image is required")
        if enable_ssl and not domain:
            raise ValueError("deploy.container: domain is required when enable_ssl is set")
        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)
        fields["image"] = image
        fields["restart_policy"] = restart_policy
        fields["cloud_provider"] = "docker"
        if name:
            fields["container_name"] = name
        if ports:
            fields["ports"] = ",".join(ports)
        if volumes:
            fields["volumes"] = ",".join(volumes)
        if env:
            fields["environment_vars"] = ",".join(env)
        if network:
            fields["network"] = network
        if command:
            fields["command"] = command
        if registry_slug:
            fields["docker_registry_slug"] = registry_slug
        if docker_user:
            fields["docker_username"] = docker_user
        if docker_password:
            fields["docker_password"] = docker_password
        if enable_ssl and domain:
            fields["enable_ssl"] = "true"
            fields["domain"] = domain
            if ssl_email:
                fields["ssl_email"] = ssl_email
        return self.client._multipart(
            self.client.node_url + "/api/v2/tenant/container/deploy",
            fields, [], op="deploy.container", timeout=timeout,
        )

    def stack(
        self,
        kind: str,
        *,
        host: str,
        ssh_user: str,
        key_pair_name: str,
        repo_url: str | None = None,
        branch: str = "main",
        path: str | None = None,
        key_pair_pem_path: str | None = None,
        app_name: str | None = None,
        git_provider: str | None = None,
        git_username: str | None = None,
        git_token: str | None = None,
        build_mode: str | None = None,
        entry: str | None = None,
        requirements: str | None = None,
        framework: str | None = None,
        go_version: str | None = None,
        http_port: str | None = None,
        https_port: str | None = None,
        app_port: str | None = None,
        env_vars: str | None = None,
        # Java / Spring Boot extras (server-side handlers ignore unknown fields)
        java_version: str | None = None,
        build_tool: str | None = None,
        jar_path: str | None = None,
        main_class: str | None = None,
        java_opts: str | None = None,
        workspace_user: str | None = None,
        organization: str | None = None,
        timeout: int = DEFAULT_LONG_TIMEOUT,
    ) -> dict[str, Any]:
        target = STACK_TARGETS.get(kind)
        if not target:
            raise ValueError(f"deploy.stack: unknown stack {kind!r}; valid: {sorted(STACK_TARGETS)}")
        if not repo_url and not path:
            raise ValueError("deploy.stack: pass either repo_url=<git-url> or path=<local-dir>")
        if repo_url and path:
            raise ValueError("deploy.stack: pass repo_url OR path, not both")

        fields = self.client._ssh_fields(host, ssh_user, key_pair_name, workspace_user, organization)

        files: list[tuple[str, str, bytes, str]] = []
        if repo_url:
            fields[target.git_field] = repo_url
            fields[target.branch_field] = branch
        else:
            blob = _zip_directory(path)
            files.append(("app_zip", f"{os.path.basename(os.path.abspath(path))}.zip", blob, "application/zip"))

        if key_pair_pem_path:
            with open(key_pair_pem_path, "rb") as fh:
                pem_bytes = fh.read()
            files.append(("private_key_pem", os.path.basename(key_pair_pem_path), pem_bytes, "application/x-pem-file"))

        for k, v in [
            ("app_name", app_name), ("git_provider", git_provider),
            ("git_username", git_username), ("git_token", git_token),
            ("build_mode", build_mode), ("entry", entry),
            ("requirements", requirements), ("framework", framework),
            ("go_version", go_version), ("http_port", http_port),
            ("https_port", https_port), ("app_port", app_port),
            ("env_vars", env_vars),
            ("java_version", java_version), ("build_tool", build_tool),
            ("jar_path", jar_path), ("main_class", main_class),
            ("java_opts", java_opts),
        ]:
            if v:
                fields[k] = v
        return self.client._multipart(
            self.client.node_url + target.path, fields, files,
            op=f"deploy.stack.{kind}", timeout=timeout,
        )


# ── Marketplace ────────────────────────────────────────────────────────

class _MarketplaceList(_Resource):
    """Generic list helper bound to a marketplace key/path."""

    PATH = ""
    KEY = ""

    def list(self) -> list[dict[str, Any]]:
        body = self.client._json("GET", self.client.node_url + self.PATH,
                                 op=f"marketplace.{self.KEY}.list")
        return body.get(self.KEY, [])

    def show(self, item_id: str) -> dict[str, Any]:
        body = self.client._json("GET", f"{self.client.node_url}{self.PATH}/{item_id}",
                                 op=f"marketplace.{self.KEY}.show")
        return body


# NOTE: must NOT be named `Agents` — a second `class Agents(_Resource)`
# (AI agents .run) is defined later in this module and would shadow this
# one at module scope, breaking `client.marketplace.agents.deploy/list`.
class MarketplaceAgents(_MarketplaceList):
    PATH = "/api/v2/marketplace/agents"
    KEY = "agents"

    def deploy(self, agent_id: str, *, host: str, ssh_user: str, key_pair_name: str,
               agent_name: str | None = None, http_port: str = "80", app_port: str | None = None,
               system_prompt: str | None = None, env_vars: str | None = None,
               version: str = "1.0.0") -> dict[str, Any]:
        body = {
            "agent_id": agent_id, "hostname": host, "ssh_username": ssh_user,
            "key_pair_name": key_pair_name, "username": self.client.username,
            "http_port": http_port, "version": version,
        }
        for k, v in [("agent_name", agent_name), ("app_port", app_port),
                     ("system_prompt", system_prompt), ("env_vars", env_vars)]:
            if v:
                body[k] = v
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/marketplace/agents/deploy",
            op="marketplace.agents.deploy", json_body=body,
            # Agent deploys provision a container on the VM — long-running,
            # same as solutions.provision. Without this the default 30s
            # timeout fires mid-deploy and raises VxNetworkError.
            timeout=DEFAULT_LONG_TIMEOUT,
        )


class Models(_MarketplaceList):
    PATH = "/api/v2/marketplace/models"
    KEY = "models"

    def deploy(self, model_id: str, *, host: str, ssh_user: str, key_pair_name: str,
               model_name: str | None = None, http_port: str = "80",
               app_port: str | None = None, env_vars: str | None = None,
               version: str = "1.0.0") -> dict[str, Any]:
        """Install a marketplace model onto a customer-owned VM. Mirrors
        Go ``marketplace.Models.Deploy`` — POST /api/v2/marketplace/models/deploy.
        Same body shape as ``MarketplaceAgents.deploy`` but keyed on
        ``model_id`` / ``model_name``."""
        body = {
            "model_id": model_id, "hostname": host, "ssh_username": ssh_user,
            "key_pair_name": key_pair_name, "username": self.client.username,
            "http_port": http_port, "version": version,
        }
        for k, v in [("model_name", model_name), ("app_port", app_port),
                     ("env_vars", env_vars)]:
            if v:
                body[k] = v
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/marketplace/models/deploy",
            op="marketplace.models.deploy", json_body=body,
            # Model deploys provision a container on the VM — long-running,
            # same as agent deploys / solutions.provision.
            timeout=DEFAULT_LONG_TIMEOUT,
        )


class Solutions(_MarketplaceList):
    PATH = "/api/v2/marketplace/templates"
    KEY = "templates"

    def provision(self, template_id: str, *, resource_name: str, cloud_provider: str,
                  region: str, environment: str = "development",
                  variables: dict[str, Any] | None = None) -> dict[str, Any]:
        body = {
            "template_name": template_id, "resource_name": resource_name,
            "cloud_provider": cloud_provider, "region": region,
            "environment": environment, "variables": variables or {},
            "username": self.client.username,
        }
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/marketplace/provision",
            op="marketplace.solutions.provision", json_body=body,
            timeout=DEFAULT_LONG_TIMEOUT,
        )


class Marketplace(_Resource):
    @property
    def agents(self) -> MarketplaceAgents:
        return MarketplaceAgents(self.client)

    @property
    def models(self) -> Models:
        return Models(self.client)

    @property
    def solutions(self) -> Solutions:
        return Solutions(self.client)


# ── Cloud (provisioning) ───────────────────────────────────────────────

class _CloudVM:
    """Namespace on `Cloud.vm` that mirrors the TypeScript SDK's `cloud.vm.*`.

    Additive: the existing `Cloud.create_vm(...)` keeps working unchanged.
    This namespace is the symmetric API so the same snippet shape works
    across both SDKs:

        c.cloud.vm.provision(name="api-vm", cloud="aws", region="us-east-1",
                             instance_type="t3.small",
                             key_pair_name="AWSPRODKEY2")
    """

    def __init__(self, cloud: "Cloud"):
        self._cloud = cloud

    def provision(self, *, name: str, cloud: str = "aws",
                  region: str = "us-east-1", instance_type: str = "t2.micro",
                  volume_size: int = 30, key_pair_name: str | None = None,
                  tags: dict[str, str] | None = None, ami: str | None = None,
                  **extras: Any) -> dict[str, Any]:
        if key_pair_name is not None:
            extras["key_pair_name"] = key_pair_name
        if tags is not None:
            extras["tags"] = tags
        if ami is not None:
            extras["ami"] = ami
        return self._cloud._provision(
            "cloud.vm.provision", "/api/v2/tenant/provision/vm",
            app=name, cloud=cloud, region=region, resource_type="vm",
            instance_type=instance_type, volume_size=volume_size, **extras,
        )

    def status(self, *, instance_id: str, cloud: str = "aws") -> dict[str, Any]:
        return self._cloud.client._json(
            "POST", self._cloud.client.node_url + "/api/v2/provision/vm/status",
            op="cloud.vm.status",
            json_body={"instance_id": instance_id, "provider": cloud},
        )

    def action(self, *, instance_id: str, action: str,
               cloud: str = "aws") -> dict[str, Any]:
        if action not in {"start", "stop", "restart", "reboot"}:
            raise VxValidationError(
                "cloud.vm.action",
                f"action must be one of start/stop/restart/reboot (got {action!r})",
            )
        return self._cloud.client._json(
            "POST", self._cloud.client.node_url + "/api/v2/provision/vm/action",
            op="cloud.vm.action",
            json_body={"instance_id": instance_id, "action": action, "provider": cloud},
        )


class Cloud(_Resource):
    @property
    def vm(self) -> "_CloudVM":
        """`c.cloud.vm.provision/status/action` — mirrors the TypeScript SDK.

        Returns a fresh namespace per access (cheap, stateless). The legacy
        flat `c.cloud.create_vm(...)` method below remains for back-compat.
        """
        return _CloudVM(self)

    def _provision(self, op: str, path: str, *, app: str, cloud: str = "aws",
                   region: str = "us-east-1", env: str = "development",
                   resource_type: str, **extras: Any) -> dict[str, Any]:
        body: dict[str, Any] = {
            "app_name": app, "resource_name": app, "instance_name": app,
            "network_name": app, "key_name": app, "role_name": app,
            "hostname": app, "cloud_provider": cloud, "region": region,
            "environment": env, "resource_type": resource_type,
            "username": self.client.username,
        }
        body.update(extras)
        return self.client._json(
            "POST", self.client.node_url + path, op=op, json_body=body,
            timeout=DEFAULT_LONG_TIMEOUT,
        )

    def create_s3_bucket(self, name: str, region: str = "us-east-1", cloud: str = "aws") -> dict[str, Any]:
        return self._provision(
            "cloud.s3.create_bucket", "/api/v2/tenant/provision/storage",
            app=name, cloud=cloud, region=region, resource_type="s3",
        )

    def create_iam_policy(self, name: str, policy_document: str | dict[str, Any]) -> dict[str, Any]:
        if isinstance(policy_document, dict):
            policy_document = json.dumps(policy_document)
        return self._provision(
            "cloud.iam.create_policy", "/api/v2/tenant/provision/security",
            app=name, resource_type="policy", policy_document=policy_document,
        )

    def create_iam_role(self, name: str, assume_role_policy: str | dict[str, Any]) -> dict[str, Any]:
        if isinstance(assume_role_policy, dict):
            assume_role_policy = json.dumps(assume_role_policy)
        return self._provision(
            "cloud.iam.create_role", "/api/v2/tenant/provision/security",
            app=name, resource_type="role", assume_role_policy=assume_role_policy,
        )

    def create_vm(self, name: str, *, cloud: str = "aws", region: str = "us-east-1",
                  instance_type: str = "t2.micro", volume_size: int = 30) -> dict[str, Any]:
        return self._provision(
            "cloud.vm.create", "/api/v2/tenant/provision/vm",
            app=name, cloud=cloud, region=region, resource_type="vm",
            instance_type=instance_type, volume_size=volume_size,
        )

    def _managed_database(self, op: str, name: str, resource_type: str, *,
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
            "tags": tags or {
                "Name": name,
                "ManagedBy": "vxsdk",
            },
        }
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/tenant/provision/databases",
            op=op, json_body=body, timeout=DEFAULT_LONG_TIMEOUT,
        )

    def create_rds(self, name: str, *, cloud: str = "aws",
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
        return self._managed_database(
            "cloud.database.create_rds", name, "rds",
            cloud=cloud, region=region, configuration=config, tags=tags,
        )

    def create_aurora(self, name: str, *, cloud: str = "aws",
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
        return self._managed_database(
            "cloud.database.create_aurora", name, "aurora",
            cloud=cloud, region=region, configuration=config, tags=tags,
        )

    def create_redis(self, name: str, *, cloud: str = "aws",
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
        return self._managed_database(
            "cloud.database.create_redis", name, "redis",
            cloud=cloud, region=region,
            configuration={
                "node_type": node_type,
                "num_cache_nodes": num_cache_nodes,
                "subnet_ids": subnet_ids,
                "vpc_security_group_ids": vpc_security_group_ids,
            },
            tags=tags,
        )

    # ── IAM keypair / VPC / Kubernetes / Serverless (Go-canonical) ────
    # Ported from cloud/cloud.go to close the Python-side gap flagged in
    # PARITY.md. All four go through the same /api/v2/tenant/provision/*
    # envelope as the existing create_vm / create_rds methods.

    def create_keypair(self, name: str, region: str = "us-east-1",
                       cloud: str = "aws") -> dict[str, Any]:
        """Provision an EC2 key pair. The private key is stored in the
        workspace vault under ``name``; the public material is uploaded
        to the cloud. Mirrors Go ``cloud.IAM.CreateKeypair``."""
        return self._provision(
            "cloud.iam.create_keypair", "/api/v2/tenant/provision/security",
            app=name, cloud=cloud, region=region, resource_type="keypair",
        )

    def create_vpc(self, name: str, cidr_block: str = "",
                   region: str = "us-east-1", cloud: str = "aws") -> dict[str, Any]:
        """Provision a VPC. Mirrors Go ``cloud.Network.CreateVPC``."""
        extras: dict[str, Any] = {}
        if cidr_block:
            extras["cidr_block"] = cidr_block
        return self._provision(
            "cloud.network.create_vpc", "/api/v2/tenant/provision/networks",
            app=name, cloud=cloud, region=region, resource_type="vpc",
            **extras,
        )

    def create_kubernetes_cluster(self, name: str, *,
                                  cloud: str = "aws",
                                  region: str = "us-east-1",
                                  node_count: int = 2,
                                  node_type: str = "t3.medium") -> dict[str, Any]:
        """Provision a managed Kubernetes cluster (EKS/GKE/AKS).
        Mirrors Go ``cloud.Kubernetes.CreateCluster``."""
        # TODO(parity): TS SDK hits /api/v2/tenant/provision/kubernetescluster/deploy.
        # Backend team to confirm canonical route.
        return self._provision(
            "cloud.kubernetes.create_cluster",
            "/api/v2/tenant/provision/kubernetes",
            app=name, cloud=cloud, region=region, resource_type="eks",
            cluster_name=name, node_count=node_count, node_type=node_type,
            provider=cloud,
        )

    def list_kubernetes_clusters(self, cloud: str = "aws",
                                 region: str = "us-east-1") -> list[dict[str, Any]]:
        """List managed Kubernetes clusters. TS-only route — Go has no
        equivalent today; using the TS canonical path until backend
        confirms."""
        # TODO(parity): Go SDK has no list_kubernetes_clusters; this route
        # only exists in the TS SDK. Backend team to confirm canonical path.
        query = urllib.parse.urlencode({"provider": cloud, "region": region})
        body = self.client._json(
            "GET",
            self.client.node_url + f"/api/v2/tenant/kubernetes/clusters?{query}",
            op="cloud.kubernetes.list_clusters",
        )
        if isinstance(body, list):
            return body
        return body.get("clusters") or body.get("data") or []

    def kubernetes_cluster_details(self, cluster_id: str, *,
                                   cloud: str = "aws",
                                   region: str = "us-east-1") -> dict[str, Any]:
        """Fetch details for a managed Kubernetes cluster. TS-only route."""
        # TODO(parity): Go SDK has no kubernetes_cluster_details; this route
        # only exists in the TS SDK. Backend team to confirm canonical path.
        if not cluster_id:
            raise ValueError("kubernetes_cluster_details: cluster_id is required")
        return self.client._json(
            "POST",
            self.client.node_url + "/api/v2/tenant/kubernetes/cluster/details",
            op="cloud.kubernetes.cluster_details",
            json_body={"name": cluster_id, "provider": cloud, "region": region},
        )

    def create_serverless_function(self, name: str, *,
                                   cloud: str = "aws",
                                   region: str = "us-east-1",
                                   runtime: str = "python3.11",
                                   handler: str = "",
                                   memory_mb: int = 0,
                                   timeout_seconds: int = 0,
                                   code: str = "",
                                   environment: dict[str, str] | None = None) -> dict[str, Any]:
        """Provision a serverless function (Lambda / Cloud Functions /
        Azure Functions). Mirrors Go ``cloud.Serverless.CreateFunction``."""
        extras: dict[str, Any] = {"runtime": runtime}
        if handler:
            extras["handler"] = handler
        if memory_mb:
            extras["memory_mb"] = memory_mb
        if timeout_seconds:
            extras["timeout_seconds"] = timeout_seconds
        if code:
            extras["code"] = code
        if environment:
            extras["environment"] = environment
        return self._provision(
            "cloud.serverless.create_function",
            "/api/v2/tenant/provision/serverless",
            app=name, cloud=cloud, region=region, resource_type="lambda",
            **extras,
        )


# ── Metal DB (PostgreSQL on a VM) ──────────────────────────────────────

class MetalDB(_Resource):
    """Self-managed PostgreSQL provisioned over SSH onto a customer VM.

    Wraps the two node endpoints the web dashboard's Metal DB wizard calls
    (vxcloud_web/app/components/databases/MetalDBComponent.tsx):

      * ``POST /api/v2/tenant/provision/metaldb/test-connection`` (multipart)
      * ``POST /api/v2/tenant/provision/metaldb``                 (JSON)

    The SSH private key is NOT sent by the client — the node looks it up in
    the workspace vault by ``key_pair_name``. So the only credentials the
    client supplies are: which VM (``host``), how to log into it
    (``ssh_user`` + vault ``key_pair_name``), and the Postgres
    user/password/admin-password to create on it.
    """

    _TEST_PATH = "/api/v2/tenant/provision/metaldb/test-connection"
    _PROVISION_PATH = "/api/v2/tenant/provision/metaldb"

    def test_connection(self, host: str, ssh_user: str, key_pair_name: str, *,
                        workspace_user: str | None = None,
                        organization: str | None = None) -> dict[str, Any]:
        """Pre-flight: SSH into ``host`` using the vault key ``key_pair_name``.

        Mirrors the wizard's "Test SSH Connection" button. Returns the node's
        JSON ``{"success": bool, "message": str, ...}`` (HTTP is always 200;
        check the ``success`` field).
        """
        self.client.ensure_node_url()
        fields = self.client._ssh_fields(
            host, ssh_user, key_pair_name, workspace_user, organization)
        return self.client._multipart(
            self.client.node_url + self._TEST_PATH, fields, [],
            op="metaldb.test_connection", timeout=DEFAULT_TIMEOUT,
        )

    def provision(self, host: str, ssh_user: str, key_pair_name: str, *,
                   database_name: str = "postgres",
                   database_user: str = "postgres",
                   database_password: str = "",
                   postgres_password: str = "",
                   port: str = "5432",
                   postgres_version: str = "16",
                   cloud_provider: str = "metaldb",
                   enable_replication: bool = False,
                   replica_hostname: str = "",
                   multi_zone: bool = False,
                   backup_enabled: bool = True,
                   backup_retention: int = 7,
                   resource_name: str | None = None,
                   workspace_user: str | None = None,
                   organization: str | None = None,
                   tags: dict[str, str] | None = None,
                   timeout: int = DEFAULT_LONG_TIMEOUT) -> dict[str, Any]:
        """Install PostgreSQL on ``host`` and create the requested DB/user.

        The payload is built to match exactly what the web wizard sends
        (MetalDBComponent.handleSubmit), so a deploy done via this SDK is
        byte-for-byte equivalent to one done from the dashboard. JSON body,
        matching the Go ``MetalDBProvisionRequest`` json tags.

        Returns the node's final provision response (this endpoint runs the
        install synchronously, so the returned dict already carries
        ``status`` / ``connection_string`` / ``outputs``).
        """
        self.client.ensure_node_url()
        ssh = self.client._ssh_fields(
            host, ssh_user, key_pair_name, workspace_user, organization)
        body: dict[str, Any] = {
            **ssh,
            "resource_name": resource_name or database_name,
            "resource_type": "metaldb",
            "cloud_provider": cloud_provider,
            "database_name": database_name,
            "database_user": database_user,
            "database_password": database_password or "root",
            "postgres_password": postgres_password or "root",
            "port": str(port),
            "postgres_version": str(postgres_version),
            "enable_replication": enable_replication,
            "replica_hostname": replica_hostname,
            "multi_zone": multi_zone,
            "backup_enabled": backup_enabled,
            "backup_retention": backup_retention,
        }
        if tags:
            body["tags"] = tags
        # Drop empty strings so the node applies its own defaults — same
        # normalization the dashboard's useProvisionStatus hook does.
        body = {k: v for k, v in body.items() if v not in ("", None)}
        return self.client._json(
            "POST", self.client.node_url + self._PROVISION_PATH,
            op="metaldb.provision", json_body=body, timeout=timeout,
        )


# ── Nodes (control plane) ──────────────────────────────────────────────

class Nodes(_Resource):
    def list(self) -> list[dict[str, Any]]:
        return self.client._json(
            "GET", self.client.infinity_url + "/api/v1/auth/nodes/", op="nodes.list",
            target="infinity",
        )

    def default(self) -> dict[str, Any] | None:
        for n in self.list():
            if n.get("is_default_node"):
                return n
        return None

    def set_default(self, node_id: int | str) -> dict[str, Any]:
        """Mark ``node_id`` as the user's default tenant node. Subsequent
        API-key exchanges will resolve this node as the target. Mirrors
        Go ``nodes.SetDefault`` — POST /api/v1/auth/nodes/{id}/set-default."""
        if not node_id:
            raise ValueError("nodes.set_default: node_id is required")
        return self.client._json(
            "POST",
            self.client.infinity_url + f"/api/v1/auth/nodes/{node_id}/set-default",
            op="nodes.set_default", json_body={}, target="infinity",
        )

    def register_self_hosted(
        self,
        *,
        hostname: str,
        custom_domain_name: str,
        port: int = 8744,
        public_ip: str = "",
        private_ip: str = "",
        tunnel_provider: str = "cloudflare",
        key_pair_name: str = "",
        ide_connection_token: str = "",
        agent1_connection_token: str = "",
        storage_type: str = "ssd",
        storage_backup_mode: str = "none",
        description: str = "",
        ssh_username: str = "",
    ) -> dict[str, Any]:
        """Register a self-hosted vxnode container (BYO hardware) — mirrors
        the dashboard's SelfHostedNodeForm. POST /api/v1/auth/nodes/self-hosted."""
        if not hostname or not custom_domain_name:
            raise ValueError("nodes.register_self_hosted: hostname + custom_domain_name required")
        body: dict[str, Any] = {
            "hostname": hostname,
            "custom_domain_name": custom_domain_name.replace("https://", "").replace("http://", "").rstrip("/"),
            "port": int(port),
            "tunnel_provider": tunnel_provider,
            "storage_type": storage_type,
            "storage_backup_mode": storage_backup_mode,
        }
        for k, v in (
            ("public_ip", public_ip),
            ("private_ip", private_ip),
            ("key_pair_name", key_pair_name),
            ("ide_connection_token", ide_connection_token),
            ("agent1_connection_token", agent1_connection_token),
            ("description", description),
            ("ssh_username", ssh_username),
        ):
            if v:
                body[k] = v
        return self.client._json(
            "POST", self.client.infinity_url + "/api/v1/auth/nodes/self-hosted",
            op="nodes.register_self_hosted", json_body=body, target="infinity",
        )

    def delete(self, node_id: int | str) -> dict[str, Any]:
        """Delete a node record. DELETE /api/v1/auth/nodes/{id}. The caller
        must terminate any underlying VM separately."""
        if not node_id:
            raise ValueError("nodes.delete: node_id is required")
        return self.client._json(
            "DELETE", self.client.infinity_url + f"/api/v1/auth/nodes/{node_id}",
            op="nodes.delete", target="infinity",
        )

    def update(self, node_id: int | str, **fields: Any) -> dict[str, Any]:
        """Partial-update an existing node. PATCH /api/v1/auth/nodes/{id}.

        Editable fields (matches backend NodeUpdateRequest):
            hostname, custom_domain_name, load_balancer, private_ip, status,
            is_default_node, provider_compute_type, storage_type,
            storage_backup_mode, storage_backup_address, installation_checklist,
            enabled_features, vpn_access_details, tunnel_vm

        Read-only (managed by the platform): public_ip, instance_id.

        Example::

            c.nodes.update(71,
                hostname="prod-east-1",
                custom_domain_name="prod.example.com",
                status="stopped",
                storage_type="nvme",
            )
        """
        if not node_id:
            raise ValueError("nodes.update: node_id is required")
        # Strip None values so callers can pass kwargs naturally; backend
        # uses ``exclude_none=True`` on the Pydantic dict too.
        body = {k: v for k, v in fields.items() if v is not None}
        if not body:
            raise ValueError("nodes.update: at least one field is required")
        # Normalize the domain field to a bare hostname (no scheme/trailing /).
        if "custom_domain_name" in body and isinstance(body["custom_domain_name"], str):
            d = body["custom_domain_name"]
            for p in ("https://", "http://"):
                if d.lower().startswith(p):
                    d = d[len(p):]
            body["custom_domain_name"] = d.rstrip("/")
        return self.client._json(
            "PATCH", self.client.infinity_url + f"/api/v1/auth/nodes/{node_id}",
            op="nodes.update", json_body=body, target="infinity",
        )


# ── Networks (script catalog + remote run) ─────────────────────────────

class Networks(_Resource):
    """Network-diagnostic scripts — DNS, bandwidth, ports, security audits.

    Local execution is the caller's job (the script is a shell script).
    Remote execution delegates to install.script under the hood.
    """

    def list(self) -> list[dict[str, Any]]:
        """List the catalog of available diagnostic scripts.

        Soft-fails to an empty list if the server doesn't yet expose
        /api/v2/tenant/networks/scripts."""
        try:
            body = self.client._json(
                "GET", self.client.node_url + "/api/v2/tenant/networks/scripts",
                op="networks.list",
            )
            return body.get("scripts", [])
        except VxError:
            return []

    def run_remote(
        self,
        script: str | bytes,
        *,
        host: str,
        ssh_user: str,
        key_pair_name: str,
        script_name: str = "network-script.sh",
        args: list[str] | None = None,
        workspace_user: str | None = None,
        organization: str | None = None,
    ) -> dict[str, Any]:
        """Ship and run a network-diagnostic script on a remote VM.

        Thin wrapper over POST /api/v2/tenant/install/script — the same
        path vxcli uses when --host is passed to `vxcli networks <name>`.
        """
        if not script:
            raise ValueError("networks.run_remote: script bytes are required")
        script_bytes = script.encode("utf-8") if isinstance(script, str) else script
        fields = self.client._ssh_fields(
            host=host, ssh_user=ssh_user, key_pair_name=key_pair_name,
            workspace_user=workspace_user, organization=organization,
        )
        fields["mode"] = "script"
        fields["script_name"] = script_name
        if args:
            fields["script_args"] = "\x00".join(args)
        files = [("script_file", script_name, script_bytes, "application/x-sh")]
        return self.client._multipart(
            self.client.node_url + "/api/v2/tenant/install/script",
            fields, files,
            op="networks.run_remote",
            timeout=DEFAULT_TIMEOUT,
        )


# ── Agents (AI orchestration) ──────────────────────────────────────────

class Agents(_Resource):
    """AI agents — coding, devops, git, parallel orchestration.

    Mirrors `vxcli agent {coding,devops,git,parallel,presets,tool,tools}`.
    The platform reads provider credentials from /api/v2/setup/ai-* and
    routes to the appropriate AI backend.
    """

    def run(
        self,
        kind: str,
        task: str,
        *,
        lang: str = "",
        provider: str = "",
        model: str = "",
        context: dict[str, str] | None = None,
    ) -> dict[str, Any]:
        if not task:
            raise ValueError("agents.run: task is required")
        body = {
            "kind": kind or "coding",
            "task": task,
            "lang": lang,
            "provider": provider,
            "model": model,
            "context": context or {},
        }
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/agents/run",
            op=f"agents.run.{kind or 'coding'}", json_body=body,
        )

    def coding(self, task: str, lang: str = "python") -> dict[str, Any]:
        return self.run("coding", task, lang=lang)

    def devops(self, task: str) -> dict[str, Any]:
        return self.run("devops", task)

    def git(self, task: str) -> dict[str, Any]:
        return self.run("git", task)

    def parallel(self, preset: str, task: str) -> dict[str, Any]:
        return self.run("parallel", task, context={"preset": preset})

    def presets(self) -> list[dict[str, Any]]:
        body = self.client._json(
            "GET", self.client.node_url + "/api/v2/agents/presets",
            op="agents.presets",
        )
        return body.get("presets", [])

    def tools(self, kind: str = "") -> list[dict[str, Any]]:
        url = self.client.node_url + "/api/v2/agents/tools"
        if kind:
            url += f"?kind={kind}"
        body = self.client._json("GET", url, op="agents.tools")
        return body.get("tools", [])

    def tool(self, name: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        if not name:
            raise ValueError("agents.tool: name is required")
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/agents/tool",
            op=f"agents.tool.{name}",
            json_body={"tool": name, "args": args or {}},
        )


# ── Chat (multi-provider) ──────────────────────────────────────────────

class Chat(_Resource):
    """Multi-provider AI chat. Provider envelope normalizes Anthropic /
    OpenAI / Google / OpenClaw / Deepseek / Qwen / etc.

    POST /api/v2/chat/send accepts a provider key and forwards the
    request using credentials stored under /api/v2/setup/ai-*.
    """

    def send(
        self,
        *,
        provider: str,
        model: str,
        messages: list[dict[str, str]],
        system_prompt: str = "",
        temperature: float = 0.0,
        max_tokens: int = 0,
    ) -> dict[str, Any]:
        if not messages and not system_prompt:
            raise ValueError("chat.send: messages or system_prompt is required")
        msgs = list(messages)
        if system_prompt:
            msgs.insert(0, {"role": "system", "content": system_prompt})
        body: dict[str, Any] = {
            "provider": provider, "model": model, "messages": msgs,
        }
        if temperature > 0:
            body["temperature"] = temperature
        if max_tokens > 0:
            body["max_tokens"] = max_tokens
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/chat/send",
            op=f"chat.send.{provider}", json_body=body,
        )

    def quick(self, provider: str, model: str, question: str) -> str:
        """One-shot helper — ask a single question, get a string back."""
        out = self.send(
            provider=provider, model=model,
            messages=[{"role": "user", "content": question}],
        )
        return out.get("completion", "")


# ── Observability (backups, migrations, sync) ──────────────────────────

class _Backups:
    def __init__(self, parent: "Observability") -> None:
        self._p = parent

    def create(self, *, resource_id: str, resource_type: str, backup_name: str) -> dict[str, Any]:
        return self._p.client._json(
            "POST", self._p.client.node_url + "/api/v2/tenant/backup/create",
            op="backups.create",
            json_body={
                "resource_id": resource_id,
                "resource_type": resource_type,
                "backup_name": backup_name,
            },
        )

    def list(self) -> list[dict[str, Any]]:
        body = self._p.client._json(
            "GET", self._p.client.node_url + "/api/v2/tenant/backup/list",
            op="backups.list",
        )
        return body.get("backups", [])

    def restore(self, *, backup_id: str, target_region: str = "") -> dict[str, Any]:
        return self._p.client._json(
            "POST", self._p.client.node_url + "/api/v2/tenant/backup/restore",
            op="backups.restore",
            json_body={"backup_id": backup_id, "target_region": target_region},
        )


class _Migrations:
    def __init__(self, parent: "Observability") -> None:
        self._p = parent

    def plan(self, *, source_provider: str, target_provider: str, resources: list[str]) -> dict[str, Any]:
        return self._p.client._json(
            "POST", self._p.client.node_url + "/api/v2/tenant/migrations/plan",
            op="migrations.plan",
            json_body={
                "source_provider": source_provider,
                "target_provider": target_provider,
                "resources": resources,
            },
        )

    def execute(self, *, session_id: str, dry_run: bool = False) -> dict[str, Any]:
        return self._p.client._json(
            "POST", self._p.client.node_url + "/api/v2/tenant/migrations/execute",
            op="migrations.execute",
            json_body={"session_id": session_id, "dry_run": dry_run},
        )


class _SyncSub:
    def __init__(self, parent: "Observability") -> None:
        self._p = parent

    def batch(self, *, provider: str, services: list[str]) -> dict[str, Any]:
        return self._p.client._json(
            "POST", self._p.client.node_url + "/api/v2/tenant/resources/synchronize/batch",
            op="sync.batch",
            json_body={"provider": provider, "services": services},
        )


class Observability(_Resource):
    """Backups, migrations, and resource synchronization."""

    def __init__(self, client: "Client") -> None:
        super().__init__(client)
        self.backups = _Backups(self)
        self.migrations = _Migrations(self)
        self.sync = _SyncSub(self)


# ── Billing ────────────────────────────────────────────────────────────

class Billing(_Resource):
    """Multicloud cost reporting + AI-driven optimization recommendations."""

    def multicloud(self, *, start_date: str, end_date: str) -> dict[str, Any]:
        if not start_date or not end_date:
            raise ValueError("billing.multicloud: start_date and end_date are required")
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/tenant/billing/multicloud",
            op="billing.multicloud",
            json_body={"start_date": start_date, "end_date": end_date},
        )

    def optimization(self, provider: str = "") -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/tenant/billing/optimization",
            op="billing.optimization",
            json_body={"provider": provider},
        )


# ── Workspace setup (35 endpoints under /api/v2/setup/*) ───────────────

class Workspace(_Resource):
    """Workspace + organization lifecycle, cloud and AI provider
    credential storage, API tokens, Git/payment/SMTP/SSL/OAuth/OKTA
    credentials, and external Vault wiring.

    All endpoints live under /api/v2/setup/*. Credential POST bodies are
    sent over TLS and never logged by the SDK.
    """

    # ── workspace + organization ──
    def create_workspace(self, name: str, region: str = "") -> dict[str, Any]:
        return self._post("/api/v2/setup/workspace",
                          {"workspace_name": name, "region": region},
                          op="workspace.create_workspace")

    def create_organization(self, name: str, plan: str = "") -> dict[str, Any]:
        return self._post("/api/v2/setup/organization",
                          {"org_name": name, "plan": plan},
                          op="workspace.create_organization")

    def delete_workspace(self, workspace_id: str = "") -> dict[str, Any]:
        """Tear down the current workspace and all resources. Mirrors Go
        ``workspace.DeleteWorkspace`` — DELETE /api/v2/setup/workspace.

        ``workspace_id`` is accepted for forward-compat but the server
        resolves the workspace from the authenticated principal."""
        url = self.client.node_url + "/api/v2/setup/workspace"
        if workspace_id:
            url += f"?workspace_id={urllib.parse.quote(workspace_id)}"
        return self.client._json(
            "DELETE", url, op="workspace.delete_workspace",
        )

    # ── cloud provider creds ──
    def store_aws_credentials(self, *, access_key_id: str, secret_access_key: str,
                              region: str = "us-east-1", iam_user: str = "",
                              account_id: str = "") -> dict[str, Any]:
        # Server contract: AWSVariablesRequest (workspace.go) — UPPER_SNAKE keys.
        body: dict[str, Any] = {
            "AWS_ACCESS_KEY_ID": access_key_id,
            "AWS_SECRET_ACCESS_KEY": secret_access_key,
            "AWS_REGION": region,
        }
        if iam_user:
            body["AWS_IAM_USER"] = iam_user
        if account_id:
            body["AWS_ACCOUNT_ID"] = account_id
        return self._post("/api/v2/setup/aws-credentials", body,
                          op="workspace.store_aws_credentials")

    def store_azure_credentials(self, *, client_id: str, client_secret: str,
                                tenant_id: str, subscription_id: str) -> dict[str, Any]:
        # Server contract: AzureVariablesRequest (workspace.go).
        return self._post("/api/v2/setup/azure-credentials", {
            "AZURE_CLIENT_ID": client_id,
            "AZURE_CLIENT_SECRET": client_secret,
            "AZURE_TENANT_ID": tenant_id,
            "AZURE_SUBSCRIPTION_ID": subscription_id,
        }, op="workspace.store_azure_credentials")

    def store_gcp_credentials(self, *, project_id: str, service_account_key: str) -> dict[str, Any]:
        # Server contract: GCPVariablesRequest (workspace.go).
        return self._post("/api/v2/setup/gcp-credentials", {
            "GCP_PROJECT_ID": project_id,
            "GCP_SERVICE_ACCOUNT_KEY": service_account_key,
        }, op="workspace.store_gcp_credentials")

    def get_all_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/setup/get-all-credentials", {},
                          op="workspace.get_all_credentials")

    # ── API tokens ──
    def create_api_token(self, name: str, expires_in_days: int = 90) -> dict[str, Any]:
        return self._post("/api/v2/setup/api-token",
                          {"token_name": name, "expires_in_days": expires_in_days},
                          op="workspace.create_api_token")

    def get_api_token(self, name: str) -> dict[str, Any]:
        return self._post("/api/v2/setup/get-api-token", {"token_name": name},
                          op="workspace.get_api_token")

    # ── AI provider credentials ──
    # Server binds <PREFIX>_API_KEY / _MODEL / _BASE_URL — NOT a generic
    # "api_key" (see *CredentialsRequest structs in workspace.go). Mirrors
    # the Go SDK's aiKeyPrefix map.
    _AI_KEY_PREFIX = {
        "openai": "OPENAI", "anthropic": "ANTHROPIC", "gemini": "GEMINI",
        "deepseek": "DEEPSEEK", "qwen": "QWEN", "huggingface": "HUGGINGFACE",
        "azure-openai": "AZURE_OPENAI", "llama": "LLAMA", "mistral": "MISTRAL",
        "cohere": "COHERE", "perplexity": "PERPLEXITY", "groq": "GROQ",
        "hermes": "HERMES", "openclaw": "OPENCLAW", "ollama": "OLLAMA",
        "brave": "BRAVE",
    }

    def store_ai_credentials(self, provider: str, *,
                             api_key: str = "", org_id: str = "",
                             endpoint: str = "", model: str = "") -> dict[str, Any]:
        """Store AI provider credentials. provider ∈ {anthropic, openai,
        gemini, deepseek, qwen, groq, mistral, perplexity, huggingface,
        llama, cohere, azure-openai, openclaw, ollama, hermes, brave}.

        endpoint doubles as the base URL for self-hosted providers
        (ollama/hermes/openclaw) and as the Azure OpenAI endpoint.
        """
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
        return self._post(f"/api/v2/setup/ai-{provider}-credentials", body,
                          op=f"workspace.store_ai_credentials.{provider}")

    def get_all_ai_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/setup/ai-get-all-credentials", {},
                          op="workspace.get_all_ai_credentials")

    # ── git / messaging / payment / smtp / ssl / oauth / okta / vault ──
    def store_git_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/git-credentials", body, op="workspace.store_git_credentials")

    def store_gitlab_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/gitlab-credentials", body, op="workspace.store_gitlab_credentials")

    def store_kubeconfig(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/kubeconfig-credentials", body, op="workspace.store_kubeconfig")

    def store_oauth_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/oauth-credentials", body, op="workspace.store_oauth_credentials")

    def store_okta_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/okta-credentials", body, op="workspace.store_okta_credentials")

    def store_cyberark_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/cyberark-credentials", body, op="workspace.store_cyberark_credentials")

    def store_external_vault_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/external-vault-credentials", body, op="workspace.store_external_vault_credentials")

    def get_vault_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/get-vault-credentials", body, op="workspace.get_vault_credentials")

    def store_payment_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/payment-credentials", body, op="workspace.store_payment_credentials")

    def get_all_payment_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/setup/payment-get-all-credentials", {}, op="workspace.get_all_payment_credentials")

    def store_smtp_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/smtp-provider-credentials", body, op="workspace.store_smtp_credentials")

    def get_all_smtp_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/setup/smtp-get-all-credentials", {}, op="workspace.get_all_smtp_credentials")

    def store_messaging_bot_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/messaging-bot-credentials", body, op="workspace.store_messaging_bot_credentials")

    def get_all_messaging_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/setup/messaging-get-all-credentials", {}, op="workspace.get_all_messaging_credentials")

    def store_ssl_certificate_credentials(self, body: dict[str, Any]) -> dict[str, Any]:
        return self._post("/api/v2/setup/ssl-certificate-credentials", body, op="workspace.store_ssl_certificate_credentials")

    def delete_credential(self, name: str) -> dict[str, Any]:
        if not name:
            raise ValueError("workspace.delete_credential: name is required")
        return self._post("/api/v2/setup/delete-credential", {"name": name},
                          op="workspace.delete_credential")

    # ── docker credentials (existing — multi-registry under docker/registries/<slug>) ──
    def store_docker_credentials(self, *, registry_name: str, docker_username: str,
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
        return self._post("/api/v2/setup/docker-credentials", body,
                          op="workspace.store_docker_credentials")

    def get_all_docker_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/vault/get-docker-credentials", {},
                          op="workspace.get_all_docker_credentials")

    def get_docker_credentials_by_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.get_docker_credentials_by_registry: registry_slug is required")
        return self._post("/api/v2/vault/get-single-docker-credentials",
                          {"registry_slug": registry_slug},
                          op="workspace.get_docker_credentials_by_registry")

    # ── docker REGISTRY endpoints (new — distinct from credentials) ──
    _VALID_DOCKER_REGISTRY_TYPES = {
        "dockerhub", "ecr", "gcr", "acr", "ghcr", "gitlab", "quay", "harbor", "jfrog", "custom",
    }

    def store_docker_registry(self, *, registry_name: str, registry_type: str,
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
        return self._post("/api/v2/setup/docker-registry", body,
                          op="workspace.store_docker_registry")

    def get_all_docker_registries(self) -> dict[str, Any]:
        return self._post("/api/v2/vault/get-docker-registries", {},
                          op="workspace.get_all_docker_registries")

    def get_docker_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.get_docker_registry: registry_slug is required")
        return self._post("/api/v2/vault/get-single-docker-registry",
                          {"registry_slug": registry_slug},
                          op="workspace.get_docker_registry")

    def delete_docker_registry(self, registry_slug: str) -> dict[str, Any]:
        if not registry_slug:
            raise ValueError("workspace.delete_docker_registry: registry_slug is required")
        return self._post("/api/v2/vault/delete-docker-registry",
                          {"registry_slug": registry_slug},
                          op="workspace.delete_docker_registry")

    # ── random / generic credentials (new — free-form bucket) ──
    def store_random_credential(self, *, credential_name: str,
                                credential_type: str = "", description: str = "",
                                fields: dict[str, Any] | None = None,
                                json_blob: str = "") -> dict[str, Any]:
        if not credential_name:
            raise ValueError("workspace.store_random_credential: credential_name is required")
        if json_blob:
            try:
                import json as _json
                _json.loads(json_blob)
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
        return self._post("/api/v2/setup/random-credentials", body,
                          op="workspace.store_random_credential")

    def get_all_random_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/vault/get-random-credentials", {},
                          op="workspace.get_all_random_credentials")

    def get_random_credential(self, credential_slug: str) -> dict[str, Any]:
        if not credential_slug:
            raise ValueError("workspace.get_random_credential: credential_slug is required")
        return self._post("/api/v2/vault/get-single-random-credential",
                          {"credential_slug": credential_slug},
                          op="workspace.get_random_credential")

    def delete_random_credential(self, credential_slug: str) -> dict[str, Any]:
        if not credential_slug:
            raise ValueError("workspace.delete_random_credential: credential_slug is required")
        return self._post("/api/v2/vault/delete-random-credential",
                          {"credential_slug": credential_slug},
                          op="workspace.delete_random_credential")

    # ── servers list (new — developer host inventory) ──
    def store_server(self, *, name: str, ip_address: str, hostname: str = "",
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
        return self._post("/api/v2/setup/server", body, op="workspace.store_server")

    def get_all_servers(self) -> dict[str, Any]:
        return self._post("/api/v2/vault/get-servers", {},
                          op="workspace.get_all_servers")

    def get_server(self, server_slug: str) -> dict[str, Any]:
        if not server_slug:
            raise ValueError("workspace.get_server: server_slug is required")
        return self._post("/api/v2/vault/get-single-server",
                          {"server_slug": server_slug}, op="workspace.get_server")

    def delete_server(self, server_slug: str) -> dict[str, Any]:
        if not server_slug:
            raise ValueError("workspace.delete_server: server_slug is required")
        return self._post("/api/v2/vault/delete-server",
                          {"server_slug": server_slug}, op="workspace.delete_server")

    # ── VM keypairs (backfill — workspace.go existing surface) ──
    def store_vm_credentials(self, *, key_pair_name: str, ssh_public_key: str = "",
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
        return self._post("/api/v2/setup/vm-credentials", body,
                          op="workspace.store_vm_credentials")

    def get_all_vm_credentials(self) -> dict[str, Any]:
        return self._post("/api/v2/vault/get-vm-credentials", {},
                          op="workspace.get_all_vm_credentials")

    def get_vm_credentials_by_keypair(self, key_pair_name: str) -> dict[str, Any]:
        if not key_pair_name:
            raise ValueError("workspace.get_vm_credentials_by_keypair: key_pair_name is required")
        return self._post("/api/v2/vault/get-single-vm-credentials",
                          {"key_pair_name": key_pair_name},
                          op="workspace.get_vm_credentials_by_keypair")

    # ── GitHub credentials (backfill — github/credentials/<name>) ──
    def store_github_credentials(self, *, github_token: str, github_token_name: str = "default",
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
        return self._post("/api/v2/setup/github-credentials", body,
                          op="workspace.store_github_credentials")

    # ── internal ──
    def _post(self, path: str, body: dict[str, Any], *, op: str) -> dict[str, Any]:
        # All /api/v2/setup/* endpoints validate VaultCredentialsRequest,
        # which requires username + organization in the body (vxcli sends
        # them via getUserOrg()). Inject them unless the caller overrode.
        merged = dict(body)
        merged.setdefault("username", self.client.username)
        merged.setdefault("organization", self.client.organization)
        return self.client._json(
            "POST", self.client.node_url + path,
            op=op, json_body=merged,
        )


# ── AgentControl (fine-tuning, training, KB, datasets, agents) ────────
#
# Maps to the Go shim at /api/v2/agentcontrol/* on the tenant node, which
# proxies to FastAPI /api/v3/agentcontrol/*. Every request needs
# X-Tenant-ID; we read it from client.tenant_id and let callers override
# per call. Long-running jobs (fine-tune, training, KB indexing) return
# a job object whose wait_for_completion() polls until terminal.

class _AgentControlBase(_Resource):
    _path = ""  # overridden per subclass, e.g. "fine-tuning"

    def _ac_url(self, suffix: str = "") -> str:
        if not self.client.node_url:
            raise VxError("agentcontrol", "node_url not set on Client")
        base = f"{self.client.node_url}/api/v2/agentcontrol/{self._path.rstrip('/')}"
        if suffix:
            return f"{base}/{suffix.lstrip('/')}"
        return base + "/"  # list endpoints want the trailing slash

    def _ac_headers(self, tenant_id: str = "") -> dict[str, str]:
        tid = tenant_id or self.client.tenant_id
        if not tid:
            raise VxError("agentcontrol", "tenant_id required (set on Client or pass per call)")
        return {"X-Tenant-ID": tid}


class _LongRunningJob:
    """Common poller for fine-tune / training / knowledge-base rows.

    The job object is a thin shell over the JSON dict returned by
    POST/GET. Mutates in-place on each poll. Terminal statuses are
    {succeeded, failed, cancelled, ready, error} — the union covers
    fine_tuning_jobs, training_jobs, and knowledge_bases.
    """
    TERMINAL = frozenset({"succeeded", "failed", "cancelled", "ready", "error"})

    def __init__(self, resource: "_AgentControlBase", data: dict[str, Any], tenant_id: str = ""):
        self._resource = resource
        self._tenant_id = tenant_id
        self.data: dict[str, Any] = dict(data)

    @property
    def id(self) -> str:
        return str(self.data.get("id", ""))

    @property
    def status(self) -> str:
        return str(self.data.get("status", ""))

    @property
    def progress(self) -> float:
        try:
            return float(self.data.get("progress") or 0.0)
        except (TypeError, ValueError):
            return 0.0

    def __getitem__(self, k: str) -> Any:
        return self.data.get(k)

    def __repr__(self) -> str:
        return f"<{type(self).__name__} id={self.id!r} status={self.status!r}>"

    def refresh(self) -> "_LongRunningJob":
        if not self.id:
            return self
        body = self._resource.client._json(
            "GET", self._resource._ac_url(self.id),
            op=f"agentcontrol.{self._resource._path}.get",
            headers=self._resource._ac_headers(self._tenant_id),
        )
        self.data.update(body)
        return self

    def wait_for_completion(self, timeout: float = 1800.0, interval: float = 5.0,
                            on_tick: Any = None) -> "_LongRunningJob":
        """Poll until status is terminal or timeout elapses. on_tick is
        an optional callable receiving the job after each refresh."""
        deadline = time.time() + max(0.0, float(timeout))
        while True:
            self.refresh()
            if on_tick is not None:
                try: on_tick(self)
                except Exception: pass
            if self.status.lower() in self.TERMINAL:
                return self
            if time.time() >= deadline:
                raise VxError(f"agentcontrol.{self._resource._path}.wait",
                              f"timed out after {timeout:.0f}s; last status={self.status!r}")
            time.sleep(max(0.5, float(interval)))


class FineTuning(_AgentControlBase):
    _path = "fine-tuning/"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.fine-tuning.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def get(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not job_id:
            raise ValueError("fine_tuning.get: job_id is required")
        return self.client._json("GET", self._ac_url(job_id),
                                 op="agentcontrol.fine-tuning.get",
                                 headers=self._ac_headers(tenant_id))

    def create(self, *, name: str, base_model: str, training_file: str,
               validation_file: str = "", epochs: int = 1, batch_size: int = 4,
               learning_rate: float = 5e-5, tenant_id: str = "",
               **extra: Any) -> _LongRunningJob:
        if not (name and base_model and training_file):
            raise ValueError("fine_tuning.create: name, base_model, training_file required")
        body = {
            "name": name, "base_model": base_model, "training_file": training_file,
            "epochs": int(epochs), "batch_size": int(batch_size),
            "learning_rate": float(learning_rate),
        }
        if validation_file:
            body["validation_file"] = validation_file
        body.update(extra)
        data = self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.fine-tuning.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)
        return _LongRunningJob(self, data, tenant_id=tenant_id)

    def delete(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not job_id:
            raise ValueError("fine_tuning.delete: job_id is required")
        return self.client._json("DELETE", self._ac_url(job_id),
                                 op="agentcontrol.fine-tuning.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/fine-tuning?confirm=true"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.fine-tuning.delete_all",
                                 headers=self._ac_headers(tenant_id))


class Training(_AgentControlBase):
    _path = "training/"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.training.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def get(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not job_id:
            raise ValueError("training.get: job_id is required")
        return self.client._json("GET", self._ac_url(job_id),
                                 op="agentcontrol.training.get",
                                 headers=self._ac_headers(tenant_id))

    def create(self, *, name: str, base_model: str, dataset_id: str,
               type: str = "pre-training", total_epochs: int = 1,
               gpu_type: str = "", tenant_id: str = "",
               **extra: Any) -> _LongRunningJob:
        if not (name and base_model and dataset_id):
            raise ValueError("training.create: name, base_model, dataset_id required")
        body = {"name": name, "base_model": base_model, "dataset_id": dataset_id,
                "type": type, "total_epochs": int(total_epochs)}
        if gpu_type:
            body["gpu_type"] = gpu_type
        body.update(extra)
        data = self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.training.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)
        return _LongRunningJob(self, data, tenant_id=tenant_id)

    def update(self, job_id: str, patch: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not job_id:
            raise ValueError("training.update: job_id is required")
        return self.client._json("PUT", self._ac_url(job_id),
                                 op="agentcontrol.training.update",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=patch)

    def delete(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not job_id:
            raise ValueError("training.delete: job_id is required")
        return self.client._json("DELETE", self._ac_url(job_id),
                                 op="agentcontrol.training.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, type_filter: str = "", tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/training?confirm=true"
        if type_filter:
            from urllib.parse import quote
            url += f"&type={quote(type_filter)}"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.training.delete_all",
                                 headers=self._ac_headers(tenant_id))

    def clone(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("POST", self._ac_url(f"{job_id}/clone"),
                                 op="agentcontrol.training.clone",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def restart(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("POST", self._ac_url(f"{job_id}/restart"),
                                 op="agentcontrol.training.restart",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def run_tests(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("POST", self._ac_url(f"{job_id}/tests"),
                                 op="agentcontrol.training.run_tests",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def run_qa(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("POST", self._ac_url(f"{job_id}/qa"),
                                 op="agentcontrol.training.run_qa",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def export(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("POST", self._ac_url(f"{job_id}/export"),
                                 op="agentcontrol.training.export",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def logs(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(f"{job_id}/logs"),
                                 op="agentcontrol.training.logs",
                                 headers=self._ac_headers(tenant_id))

    def metrics(self, job_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(f"{job_id}/metrics"),
                                 op="agentcontrol.training.metrics",
                                 headers=self._ac_headers(tenant_id))

    def chat(self, job_id: str, *, message: str, session_id: str = "",
             model_id: str = "", tenant_id: str = "") -> dict[str, Any]:
        if not message:
            raise ValueError("training.chat: message is required")
        body: dict[str, Any] = {"message": message}
        if session_id:
            body["session_id"] = session_id
        if model_id:
            body["model_id"] = model_id
        return self.client._json("POST", self._ac_url(f"{job_id}/chat"),
                                 op="agentcontrol.training.chat",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)


class Knowledge(_AgentControlBase):
    _path = "knowledge/"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.knowledge.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def get(self, kb_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(kb_id),
                                 op="agentcontrol.knowledge.get",
                                 headers=self._ac_headers(tenant_id))

    def create(self, *, name: str, type: str = "documents",
               tenant_id: str = "", **extra: Any) -> _LongRunningJob:
        body = {"name": name, "type": type}
        body.update(extra)
        data = self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.knowledge.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)
        return _LongRunningJob(self, data, tenant_id=tenant_id)

    def delete(self, kb_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not kb_id:
            raise ValueError("knowledge.delete: kb_id is required")
        return self.client._json("DELETE", self._ac_url(kb_id),
                                 op="agentcontrol.knowledge.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/knowledge?confirm=true"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.knowledge.delete_all",
                                 headers=self._ac_headers(tenant_id))


class Datasets(_AgentControlBase):
    _path = "datasets/"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.datasets.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def get(self, ds_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(ds_id),
                                 op="agentcontrol.datasets.get",
                                 headers=self._ac_headers(tenant_id))

    def preview(self, ds_id: str, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(f"{ds_id}/preview"),
                                 op="agentcontrol.datasets.preview",
                                 headers=self._ac_headers(tenant_id))

    def upload(self, file_path: str, *, name: str, type: str = "training",
               format: str = "csv", tenant_id: str = "") -> dict[str, Any]:
        with open(file_path, "rb") as f:
            blob = f.read()
        fname = file_path.rsplit("/", 1)[-1].rsplit("\\", 1)[-1]
        body, ctype = _multipart_body(
            fields={"name": name, "type": type, "format": format},
            files=[("file", fname, blob, "text/csv")],
        )
        headers = self._ac_headers(tenant_id)
        headers["Content-Type"] = ctype
        return self.client._json(
            "POST", self._ac_url("upload"),
            op="agentcontrol.datasets.upload",
            headers=headers, raw_body=body,
        )

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name"):
            raise ValueError("datasets.create: spec['name'] is required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.datasets.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def download(self, ds_id: str, tenant_id: str = "") -> bytes:
        """Fetch the raw dataset bytes. Returns the body (zip/csv/parquet —
        caller inspects Content-Disposition for the original filename if
        needed). Uses _do directly because _json would try to JSON-decode."""
        if not ds_id:
            raise ValueError("datasets.download: ds_id is required")
        _status, _hdrs, raw = self.client._do(
            "GET", self._ac_url(f"{ds_id}/download"),
            op="agentcontrol.datasets.download",
            headers=self._ac_headers(tenant_id),
        )
        return raw or b""

    def delete(self, ds_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not ds_id:
            raise ValueError("datasets.delete: ds_id is required")
        return self.client._json("DELETE", self._ac_url(ds_id),
                                 op="agentcontrol.datasets.delete",
                                 headers=self._ac_headers(tenant_id))


class AgentControlAgents(_AgentControlBase):
    """Server-side agents living in the agentcontrol DB. Distinct from
    Client.agents (AI orchestration) — those are client-side prompt
    agents. These persist through /agents/{id} CRUD."""
    _path = "agents/"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.agents.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def execute(self, agent_id: str, *, task: str = "", tenant_id: str = "",
                **extra: Any) -> dict[str, Any]:
        body = {"task": task}
        body.update(extra)
        return self.client._json("POST", self._ac_url(f"{agent_id}/execute"),
                                 op="agentcontrol.agents.execute",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def get(self, agent_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not agent_id:
            raise ValueError("agents.get: agent_id is required")
        return self.client._json("GET", self._ac_url(agent_id),
                                 op="agentcontrol.agents.get",
                                 headers=self._ac_headers(tenant_id))

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name") or not spec.get("model_id"):
            raise ValueError("agents.create: spec['name'] and spec['model_id'] are required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.agents.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def update(self, agent_id: str, patch: dict[str, Any],
               tenant_id: str = "") -> dict[str, Any]:
        if not agent_id:
            raise ValueError("agents.update: agent_id is required")
        return self.client._json("PUT", self._ac_url(agent_id),
                                 op="agentcontrol.agents.update",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=patch)

    def delete(self, agent_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not agent_id:
            raise ValueError("agents.delete: agent_id is required")
        return self.client._json("DELETE", self._ac_url(agent_id),
                                 op="agentcontrol.agents.delete",
                                 headers=self._ac_headers(tenant_id))

    def proxy_execute(self, *, endpoint: str, message: str, session_id: str = "",
                      path: str = "", payload_mode: str = "",
                      tenant_id: str = "") -> dict[str, Any]:
        """Node-mediated execute for marketplace agents (mixed-content workaround)."""
        if not endpoint:
            raise ValueError("agents.proxy_execute: endpoint is required")
        if not message:
            raise ValueError("agents.proxy_execute: message is required")
        body: dict[str, Any] = {"endpoint": endpoint, "message": message}
        if session_id:
            body["session_id"] = session_id
        if path:
            body["path"] = path
        if payload_mode:
            body["payload_mode"] = payload_mode
        return self.client._json("POST", self._ac_url("proxy-execute"),
                                 op="agentcontrol.agents.proxy_execute",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)


class _AgentControlGitHub(_AgentControlBase):
    _path = "github/"

    def list_repos(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("repos"),
                                 op="agentcontrol.github.list_repos",
                                 headers=self._ac_headers(tenant_id))

    def import_dataset(self, *, repo: str, branch: str = "main",
                       path: str = "", name: str = "",
                       tenant_id: str = "") -> dict[str, Any]:
        body = {"repo": repo, "branch": branch, "path": path,
                "name": name or repo.split("/")[-1]}
        return self.client._json("POST", self._ac_url("import-dataset"),
                                 op="agentcontrol.github.import_dataset",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def repo_contents(self, owner: str, repo: str, *, path: str = "",
                      ref: str = "", tenant_id: str = "") -> dict[str, Any]:
        """Browse repo contents (Programming-tab "Import from GitHub")."""
        if not owner or not repo:
            raise ValueError("github.repo_contents: owner + repo are required")
        from urllib.parse import quote, urlencode
        suffix = f"repos/{quote(owner, safe='')}/{quote(repo, safe='')}/contents"
        q: dict[str, str] = {}
        if path:
            q["path"] = path
        if ref:
            q["ref"] = ref
        if q:
            suffix += f"?{urlencode(q)}"
        return self.client._json("GET", self._ac_url(suffix),
                                 op="agentcontrol.github.repo_contents",
                                 headers=self._ac_headers(tenant_id))


# ── New AgentControl sub-resources (UI parity) ──────────────────────────

class Embeddings(_AgentControlBase):
    """Vector artifacts produced by the source pipeline."""
    _path = "embeddings"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        # Note: /embeddings has no trailing slash on this route.
        url = f"{self.client.node_url}/api/v2/agentcontrol/embeddings"
        body = self.client._json("GET", url,
                                 op="agentcontrol.embeddings.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def query(self, art_id: str, question: str, top_k: int = 5,
              tenant_id: str = "") -> dict[str, Any]:
        if not art_id:
            raise ValueError("embeddings.query: art_id is required")
        if not question:
            raise ValueError("embeddings.query: question is required")
        return self.client._json("POST", self._ac_url(f"{art_id}/query"),
                                 op="agentcontrol.embeddings.query",
                                 headers=self._ac_headers(tenant_id),
                                 json_body={"question": question, "top_k": int(top_k)})

    def download(self, art_id: str, part: str, tenant_id: str = "") -> bytes:
        """Returns the raw zip bytes for a part of the bundle ('faiss' | 'chromadb')."""
        if not art_id or not part:
            raise ValueError("embeddings.download: art_id and part are required")
        from urllib.parse import quote
        url = (f"{self.client.node_url}/api/v2/agentcontrol/embeddings/"
               f"{quote(art_id, safe='')}/download?part={quote(part, safe='')}")
        _status, _hdrs, raw = self.client._do(
            "GET", url, op="agentcontrol.embeddings.download",
            headers=self._ac_headers(tenant_id),
        )
        return raw or b""

    def visualize(self, art_id: str, max_points: int = 400,
                  tenant_id: str = "") -> dict[str, Any]:
        if not art_id:
            raise ValueError("embeddings.visualize: art_id is required")
        return self.client._json(
            "GET", f"{self._ac_url(f'{art_id}/visualize')}?max_points={int(max_points)}",
            op="agentcontrol.embeddings.visualize",
            headers=self._ac_headers(tenant_id))

    def promote(self, art_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not art_id:
            raise ValueError("embeddings.promote: art_id is required")
        return self.client._json("POST", self._ac_url(f"{art_id}/promote"),
                                 op="agentcontrol.embeddings.promote",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def upload(self, filename: str, content_base64: str,
               tenant_id: str = "") -> dict[str, Any]:
        if not filename or not content_base64:
            raise ValueError("embeddings.upload: filename + content_base64 required")
        return self.client._json("POST", self._ac_url("upload"),
                                 op="agentcontrol.embeddings.upload",
                                 headers=self._ac_headers(tenant_id),
                                 json_body={"filename": filename,
                                            "content_base64": content_base64})

    def delete(self, art_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not art_id:
            raise ValueError("embeddings.delete: art_id is required")
        return self.client._json("DELETE", self._ac_url(art_id),
                                 op="agentcontrol.embeddings.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/embeddings?confirm=true"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.embeddings.delete_all",
                                 headers=self._ac_headers(tenant_id))


class Tools(_AgentControlBase):
    """Tools & actions registered against the tenant."""
    _path = "tools"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.tools.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name"):
            raise ValueError("tools.create: spec['name'] is required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.tools.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def update(self, tool_id: str, patch: dict[str, Any],
               tenant_id: str = "") -> dict[str, Any]:
        if not tool_id:
            raise ValueError("tools.update: tool_id is required")
        return self.client._json("PATCH", self._ac_url(tool_id),
                                 op="agentcontrol.tools.update",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=patch)

    def delete(self, tool_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not tool_id:
            raise ValueError("tools.delete: tool_id is required")
        return self.client._json("DELETE", self._ac_url(tool_id),
                                 op="agentcontrol.tools.delete",
                                 headers=self._ac_headers(tenant_id))


class MCP(_AgentControlBase):
    """MCP servers (Cloudflare may block non-browser callers — see memory)."""
    _path = "mcp-servers"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.mcp.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name") or not spec.get("url"):
            raise ValueError("mcp.create: spec['name'] and spec['url'] are required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.mcp.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def refresh(self, server_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not server_id:
            raise ValueError("mcp.refresh: server_id is required")
        return self.client._json("POST", self._ac_url(f"{server_id}/refresh"),
                                 op="agentcontrol.mcp.refresh",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def delete(self, server_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not server_id:
            raise ValueError("mcp.delete: server_id is required")
        return self.client._json("DELETE", self._ac_url(server_id),
                                 op="agentcontrol.mcp.delete",
                                 headers=self._ac_headers(tenant_id))


class Evals(_AgentControlBase):
    """Evaluation runs + human feedback (Benchmarks tab)."""
    _path = "evals"

    def list_runs(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url("runs/"),
                                 op="agentcontrol.evals.list_runs",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def create_run(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name"):
            raise ValueError("evals.create_run: spec['name'] is required")
        tid = tenant_id or self.client.tenant_id
        body: dict[str, Any] = {"tenant_id": tid}
        body.update(spec)
        return self.client._json("POST", self._ac_url("runs/"),
                                 op="agentcontrol.evals.create_run",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def delete_run(self, run_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not run_id:
            raise ValueError("evals.delete_run: run_id is required")
        return self.client._json("DELETE", self._ac_url(f"runs/{run_id}"),
                                 op="agentcontrol.evals.delete_run",
                                 headers=self._ac_headers(tenant_id))

    def submit_feedback(self, *, request_id: str, feedback: str,
                        comment: str = "", tenant_id: str = "") -> dict[str, Any]:
        if not request_id or not feedback:
            raise ValueError("evals.submit_feedback: request_id + feedback required")
        body: dict[str, Any] = {"request_id": request_id, "feedback": feedback}
        if comment:
            body["comment"] = comment
        return self.client._json("POST", self._ac_url("feedback"),
                                 op="agentcontrol.evals.submit_feedback",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def feedback_stats(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("stats"),
                                 op="agentcontrol.evals.feedback_stats",
                                 headers=self._ac_headers(tenant_id))


class Code(_AgentControlBase):
    """Programming-tab Code runner — runs + persists editor content."""
    _path = "code"

    def run(self, *, language: str, content: str, filename: str = "",
            env: dict[str, str] | None = None, timeout_secs: int = 0,
            args: list[str] | None = None, tenant_id: str = "") -> dict[str, Any]:
        if not language:
            raise ValueError("code.run: language is required")
        if not content:
            raise ValueError("code.run: content is required")
        body: dict[str, Any] = {"language": language, "content": content}
        if filename:
            body["filename"] = filename
        if env:
            body["env"] = env
        if timeout_secs:
            body["timeout_secs"] = int(timeout_secs)
        if args:
            body["args"] = args
        return self.client._json("POST", self._ac_url("run"),
                                 op="agentcontrol.code.run",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def save(self, *, language: str, content: str, filename: str = "",
             tenant_id: str = "") -> dict[str, Any]:
        if not language or not content:
            raise ValueError("code.save: language + content are required")
        body: dict[str, Any] = {"language": language, "content": content}
        if filename:
            body["filename"] = filename
        return self.client._json("POST", self._ac_url("save"),
                                 op="agentcontrol.code.save",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def list_saved(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("saved"),
                                 op="agentcontrol.code.list_saved",
                                 headers=self._ac_headers(tenant_id))

    def get_saved(self, filename: str, tenant_id: str = "") -> dict[str, Any]:
        if not filename:
            raise ValueError("code.get_saved: filename is required")
        from urllib.parse import quote
        return self.client._json("GET", self._ac_url(f"saved/{quote(filename, safe='')}"),
                                 op="agentcontrol.code.get_saved",
                                 headers=self._ac_headers(tenant_id))

    def delete_saved(self, filename: str, tenant_id: str = "") -> dict[str, Any]:
        if not filename:
            raise ValueError("code.delete_saved: filename is required")
        from urllib.parse import quote
        return self.client._json("DELETE", self._ac_url(f"saved/{quote(filename, safe='')}"),
                                 op="agentcontrol.code.delete_saved",
                                 headers=self._ac_headers(tenant_id))


class Models(_AgentControlBase):
    """AgentControl-side Models (upload-custom + soft-delete + state)."""
    _path = "models"

    def list(self, state: str = "", tenant_id: str = "") -> list[dict[str, Any]]:
        url = self._ac_url()
        if state:
            from urllib.parse import quote
            url += f"?state={quote(state)}"
        body = self.client._json("GET", url,
                                 op="agentcontrol.models.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def get(self, model_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not model_id:
            raise ValueError("models.get: model_id is required")
        return self.client._json("GET", self._ac_url(model_id),
                                 op="agentcontrol.models.get",
                                 headers=self._ac_headers(tenant_id))

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name"):
            raise ValueError("models.create: spec['name'] is required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.models.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def delete(self, model_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not model_id:
            raise ValueError("models.delete: model_id is required")
        return self.client._json("DELETE", self._ac_url(model_id),
                                 op="agentcontrol.models.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/models?confirm=true"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.models.delete_all",
                                 headers=self._ac_headers(tenant_id))

    def set_state(self, model_id: str, state: str,
                  tenant_id: str = "") -> dict[str, Any]:
        if not model_id or not state:
            raise ValueError("models.set_state: model_id + state are required")
        return self.client._json("PATCH", self._ac_url(f"{model_id}/state"),
                                 op="agentcontrol.models.set_state",
                                 headers=self._ac_headers(tenant_id),
                                 json_body={"state": state})

    def export_training_data(self, model_id: str,
                             tenant_id: str = "") -> dict[str, Any]:
        if not model_id:
            raise ValueError("models.export_training_data: model_id is required")
        return self.client._json("GET", self._ac_url(f"{model_id}/export"),
                                 op="agentcontrol.models.export_training_data",
                                 headers=self._ac_headers(tenant_id))


class Deployments(_AgentControlBase):
    """Model endpoints — My Deployments tab."""
    _path = "deployments"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.deployments.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def summary(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("summary"),
                                 op="agentcontrol.deployments.summary",
                                 headers=self._ac_headers(tenant_id))

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name") or not spec.get("model_id"):
            raise ValueError("deployments.create: spec['name'] + spec['model_id'] required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.deployments.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def sync(self, dep_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not dep_id:
            raise ValueError("deployments.sync: dep_id is required")
        return self.client._json("POST", self._ac_url(f"{dep_id}/sync"),
                                 op="agentcontrol.deployments.sync",
                                 headers=self._ac_headers(tenant_id), json_body={})

    def set_status(self, dep_id: str, status: str,
                   tenant_id: str = "") -> dict[str, Any]:
        if not dep_id or not status:
            raise ValueError("deployments.set_status: dep_id + status required")
        return self.client._json("PATCH", self._ac_url(f"{dep_id}/status"),
                                 op="agentcontrol.deployments.set_status",
                                 headers=self._ac_headers(tenant_id),
                                 json_body={"status": status})

    def delete(self, dep_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not dep_id:
            raise ValueError("deployments.delete: dep_id is required")
        return self.client._json("DELETE", self._ac_url(dep_id),
                                 op="agentcontrol.deployments.delete",
                                 headers=self._ac_headers(tenant_id))

    def delete_all(self, tenant_id: str = "") -> dict[str, Any]:
        url = f"{self.client.node_url}/api/v2/agentcontrol/deployments?confirm=true"
        return self.client._json("DELETE", url,
                                 op="agentcontrol.deployments.delete_all",
                                 headers=self._ac_headers(tenant_id))


class WebAssets(_AgentControlBase):
    _path = "web-assets"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.web_assets.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if not spec or not spec.get("name"):
            raise ValueError("web_assets.create: spec['name'] is required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.web_assets.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)

    def delete(self, asset_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not asset_id:
            raise ValueError("web_assets.delete: asset_id is required")
        return self.client._json("DELETE", self._ac_url(asset_id),
                                 op="agentcontrol.web_assets.delete",
                                 headers=self._ac_headers(tenant_id))


class Benchmarks(_AgentControlBase):
    """Legacy /benchmarks/ surface (distinct from Evals which is /evals/runs/)."""
    _path = "benchmarks"

    def list(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.benchmarks.list",
                                 headers=self._ac_headers(tenant_id))

    def create(self, spec: dict[str, Any], tenant_id: str = "") -> dict[str, Any]:
        if spec is None:
            raise ValueError("benchmarks.create: spec is required")
        return self.client._json("POST", self._ac_url(),
                                 op="agentcontrol.benchmarks.create",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=spec)


class Catalog(_AgentControlBase):
    _path = "catalog"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        body = self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.catalog.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def summary(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("summary"),
                                 op="agentcontrol.catalog.summary",
                                 headers=self._ac_headers(tenant_id))


class Health(_AgentControlBase):
    _path = "health"

    def all_models(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("models/status"),
                                 op="agentcontrol.health.all_models",
                                 headers=self._ac_headers(tenant_id))

    def model(self, model_id: str, tenant_id: str = "") -> dict[str, Any]:
        if not model_id:
            raise ValueError("health.model: model_id is required")
        return self.client._json("GET", self._ac_url(f"models/{model_id}/status"),
                                 op="agentcontrol.health.model",
                                 headers=self._ac_headers(tenant_id))


class Events(_AgentControlBase):
    _path = "events"

    def status(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("status"),
                                 op="agentcontrol.events.status",
                                 headers=self._ac_headers(tenant_id))

    def publish(self, topic: str, event_type: str, payload: Any,
                tenant_id: str = "") -> dict[str, Any]:
        if not topic or not event_type:
            raise ValueError("events.publish: topic + event_type required")
        return self.client._json("POST", self._ac_url("publish"),
                                 op="agentcontrol.events.publish",
                                 headers=self._ac_headers(tenant_id),
                                 json_body={"topic": topic,
                                            "event_type": event_type,
                                            "payload": payload})


class LLM(_AgentControlBase):
    """In-node LLM chat (distinct from the top-level Chat client)."""
    _path = "llm"

    def chat(self, *, provider: str, model: str, message: str,
             agent_type: str = "", session_id: str = "",
             tenant_id: str = "") -> dict[str, Any]:
        if not (provider and model and message):
            raise ValueError("llm.chat: provider, model, message all required")
        body: dict[str, Any] = {"provider": provider, "model": model, "message": message}
        if agent_type:
            body["agent_type"] = agent_type
        if session_id:
            body["session_id"] = session_id
        return self.client._json("POST", self._ac_url("chat"),
                                 op="agentcontrol.llm.chat",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def providers(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("providers"),
                                 op="agentcontrol.llm.providers",
                                 headers=self._ac_headers(tenant_id))


class DeployTargets(_AgentControlBase):
    _path = "deploy-targets"

    def list(self, tenant_id: str = "") -> list[dict[str, Any]]:
        # /deploy-targets has no trailing slash on this route.
        url = f"{self.client.node_url}/api/v2/agentcontrol/deploy-targets"
        body = self.client._json("GET", url,
                                 op="agentcontrol.deploy_targets.list",
                                 headers=self._ac_headers(tenant_id))
        return body.get("items", [])

    def provision(self, *, cloud_provider: str = "", region: str = "",
                  instance_type: str = "", os: str = "", instance_name: str = "",
                  tenant_id: str = "") -> dict[str, Any]:
        body: dict[str, Any] = {}
        if cloud_provider:
            body["cloud_provider"] = cloud_provider
        if region:
            body["region"] = region
        if instance_type:
            body["instance_type"] = instance_type
        if os:
            body["os"] = os
        if instance_name:
            body["instance_name"] = instance_name
        return self.client._json("POST", self._ac_url("provision"),
                                 op="agentcontrol.deploy_targets.provision",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)

    def provision_status(self, session_id: str, username: str,
                         tenant_id: str = "") -> dict[str, Any]:
        if not session_id or not username:
            raise ValueError("deploy_targets.provision_status: session_id + username required")
        from urllib.parse import quote
        return self.client._json(
            "GET", self._ac_url(f"provision/{quote(session_id, safe='')}?username={quote(username)}"),
            op="agentcontrol.deploy_targets.provision_status",
            headers=self._ac_headers(tenant_id))


class Workflows(_AgentControlBase):
    """AgentControl workflow shim (list + trigger). The full workflow CRUD
    lives on the standalone workflow service — use vxsdk Workflow for that."""
    _path = "workflows"

    def list(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url(),
                                 op="agentcontrol.workflows.list",
                                 headers=self._ac_headers(tenant_id))

    def trigger(self, workflow_id: str, input_data: Any = None,
                tenant_id: str = "") -> dict[str, Any]:
        if not workflow_id:
            raise ValueError("workflows.trigger: workflow_id is required")
        body: dict[str, Any] = {"workflow_id": workflow_id}
        if input_data is not None:
            body["input"] = input_data
        return self.client._json("POST", self._ac_url("trigger"),
                                 op="agentcontrol.workflows.trigger",
                                 headers=self._ac_headers(tenant_id),
                                 json_body=body)


class Infra(_AgentControlBase):
    _path = "infra"

    def endpoints(self, tenant_id: str = "") -> dict[str, Any]:
        return self.client._json("GET", self._ac_url("endpoints"),
                                 op="agentcontrol.infra.endpoints",
                                 headers=self._ac_headers(tenant_id))


class AgentControl(_Resource):
    """Facade for the AgentControl surface. All sub-resources share the
    same X-Tenant-ID header rule — set client.tenant_id once."""

    def __init__(self, client: "Client") -> None:
        super().__init__(client)
        # Original sub-resources
        self.fine_tuning = FineTuning(client)
        self.training = Training(client)
        self.knowledge = Knowledge(client)
        self.datasets = Datasets(client)
        self.ac_agents = AgentControlAgents(client)
        self.github = _AgentControlGitHub(client)
        # Sub-resources added for UI parity
        self.embeddings = Embeddings(client)
        self.tools = Tools(client)
        self.mcp = MCP(client)
        self.evals = Evals(client)
        self.code = Code(client)
        self.models = Models(client)
        self.deployments = Deployments(client)
        self.web_assets = WebAssets(client)
        self.benchmarks = Benchmarks(client)
        self.catalog = Catalog(client)
        self.health = Health(client)
        self.events = Events(client)
        self.llm = LLM(client)
        self.deploy_targets = DeployTargets(client)
        self.workflows = Workflows(client)
        self.infra = Infra(client)

    def summary(self, tenant_id: str = "") -> dict[str, Any]:
        if not self.client.node_url:
            raise VxError("agentcontrol", "node_url not set on Client")
        return self.client._json(
            "GET", f"{self.client.node_url}/api/v2/agentcontrol/summary",
            op="agentcontrol.summary",
            headers=self.fine_tuning._ac_headers(tenant_id),
        )

    def runtime_metrics(self, endpoint: str, tenant_id: str = "") -> dict[str, Any]:
        """Proxy a Prometheus-style /metrics scrape through the node so the
        SDK can read marketplace agent metrics without mixed-content/CORS issues."""
        if not endpoint:
            raise ValueError("agentcontrol.runtime_metrics: endpoint is required")
        if not self.client.node_url:
            raise VxError("agentcontrol", "node_url not set on Client")
        from urllib.parse import quote
        url = (f"{self.client.node_url}/api/v2/agentcontrol/runtime/metrics"
               f"?endpoint={quote(endpoint, safe='')}")
        return self.client._json("GET", url,
                                 op="agentcontrol.runtime_metrics",
                                 headers=self.fine_tuning._ac_headers(tenant_id))


# ── VXCOMPUTER / Robotic / VxChrono (local control-plane services) ──────

class VxComputer(_Resource):
    """VXCOMPUTER — the node-local policy-governed agent runtime.

    Mirrors /api/v2/vxcomputer/*. The agent loop, policy gate, signed
    approvals, and tamper-evident audit ledger all live on the node."""

    def info(self) -> dict[str, Any]:
        return self.client._json("GET", self.client.node_url + "/api/v2/vxcomputer/info",
                                  op="vxcomputer.info")

    def health(self) -> dict[str, Any]:
        return self.client._json("GET", self.client.node_url + "/api/v2/vxcomputer/health",
                                  op="vxcomputer.health")

    def classify(self, command: str) -> dict[str, Any]:
        """Policy-classify a shell command (low|medium|high|hard-blocked)."""
        if not command:
            raise ValueError("vxcomputer.classify: command is required")
        q = urllib.parse.urlencode({"command": command})
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/vxcomputer/policy/classify?{q}",
            op="vxcomputer.classify")

    def run(self, objective: str, *, channel: str = "chat",
            session_id: str = "") -> dict[str, Any]:
        """Drive the Plan→Act→Reflect loop for an objective. Returns the
        full timeline; status may be awaiting_approval."""
        if not objective:
            raise ValueError("vxcomputer.run: objective is required")
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/vxcomputer/run",
            op="vxcomputer.run",
            json_body={"objective": objective, "channel": channel,
                       "session_id": session_id})

    def resolve_approval(self, run_id: str, step_id: str, command: str,
                         *, decision: str = "approve", ttl_seconds: int = 900,
                         approver: str = "") -> dict[str, Any]:
        """Approve or deny a pending medium/high-risk command. On approve,
        returns a signed, single-use, command-bound approval token."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/vxcomputer/approval/resolve",
            op="vxcomputer.resolve_approval",
            json_body={"run_id": run_id, "step_id": step_id, "command": command,
                       "decision": decision, "ttl_seconds": ttl_seconds,
                       "approver": approver or self.client.username})

    def audit_verify(self) -> dict[str, Any]:
        """Replay the local hash-chained audit ledger; reports tampering."""
        return self.client._json(
            "GET", self.client.node_url + "/api/v2/vxcomputer/audit/verify",
            op="vxcomputer.audit_verify")


class Robotic(_Resource):
    """Robotic control cloud — mirrors /api/v2/robotic/*."""

    def info(self) -> dict[str, Any]:
        return self.client._json("GET", self.client.node_url + "/api/v2/robotic/info",
                                  op="robotic.info")

    def list(self) -> dict[str, Any]:
        return self.client._json("GET", self.client.node_url + "/api/v2/robotic/robots",
                                  op="robotic.list")

    def register(self, spec: dict[str, Any]) -> dict[str, Any]:
        return self.client._json("POST", self.client.node_url + "/api/v2/robotic/robots",
                                  op="robotic.register", json_body=spec)

    def get(self, robot_id: str) -> dict[str, Any]:
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}",
            op="robotic.get")

    def delete(self, robot_id: str) -> dict[str, Any]:
        return self.client._json(
            "DELETE", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}",
            op="robotic.delete")

    def command(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/command",
            op="robotic.command", json_body=payload)

    def command_status(self, command_id: str) -> dict[str, Any]:
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/robotic/commands/{command_id}",
            op="robotic.command_status")

    def emergency_stop(self, robot_id: str) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/emergency-stop",
            op="robotic.emergency_stop", json_body={})

    def telemetry(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/telemetry",
            op="robotic.telemetry", json_body=payload)

    def resolve_approval(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/approval/resolve",
            op="robotic.resolve_approval", json_body=payload)

    def plan(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Autonomous LLM mission plan (payload: objective, execute, provider, model)."""
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/plan",
            op="robotic.plan", json_body=payload)

    def schedule(self, robot_id: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Schedule a recurring mission via vxchrono (payload: objective, schedule_type, cadence_minutes|cron_expr)."""
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/robotic/robots/{robot_id}/schedule",
            op="robotic.schedule", json_body=payload)

    def fleet_command(self, payload: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/robotic/fleet/command",
            op="robotic.fleet_command", json_body=payload)

    def fleet_mission(self, payload: dict[str, Any]) -> dict[str, Any]:
        """Multi-robot mission via the workflow engine + per-robot LLM plan
        (payload: objective, robot_ids|robot_type|tags)."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/robotic/fleet/mission",
            op="robotic.fleet_mission", json_body=payload)


class VxChrono(_Resource):
    """VxChrono — autonomous goal executor / scheduler.
    Mirrors /api/v2/vxchrono/*."""

    def init(self) -> dict[str, Any]:
        return self.client._json("POST", self.client.node_url + "/api/v2/vxchrono/init",
                                  op="vxchrono.init", json_body={})

    def create_goal(self, goal: dict[str, Any]) -> dict[str, Any]:
        return self.client._json("POST", self.client.node_url + "/api/v2/vxchrono/goals",
                                  op="vxchrono.create_goal", json_body=goal)

    def list_goals(self) -> dict[str, Any]:
        return self.client._json("GET", self.client.node_url + "/api/v2/vxchrono/goals",
                                  op="vxchrono.list_goals")

    def get_goal(self, goal_id: str) -> dict[str, Any]:
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.get_goal")

    def update_goal(self, goal_id: str, patch: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "PATCH", self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.update_goal", json_body=patch)

    def delete_goal(self, goal_id: str) -> dict[str, Any]:
        return self.client._json(
            "DELETE", self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}",
            op="vxchrono.delete_goal")

    def schedule(self, goal_id: str, schedule: dict[str, Any]) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}/schedule",
            op="vxchrono.schedule", json_body=schedule)

    def launch_run(self, goal_id: str, payload: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/vxchrono/goals/{goal_id}/run",
            op="vxchrono.launch_run", json_body=payload or {})

    def get_run(self, run_id: str) -> dict[str, Any]:
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}",
            op="vxchrono.get_run")

    def pause_run(self, run_id: str) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/pause",
            op="vxchrono.pause_run", json_body={})

    def resume_run(self, run_id: str) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/resume",
            op="vxchrono.resume_run", json_body={})

    def stop_run(self, run_id: str) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/vxchrono/runs/{run_id}/stop",
            op="vxchrono.stop_run", json_body={})

    def dispatch_scheduler(self) -> dict[str, Any]:
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/vxchrono/scheduler/dispatch",
            op="vxchrono.dispatch_scheduler", json_body={})


class Workflow(_Resource):
    """Workflow orchestration — the node-local n8n-style visual workflow
    engine. Executes ReactFlow DAGs in parallel waves with infrastructure,
    integration, logic, deployment, and script nodes.

    A *workflow* is a node graph (definition); an *execution* is one run of
    it. Mirrors /api/v2/workflow/*."""

    # ── workflow definitions (CRUD) ──

    def list(self) -> dict[str, Any]:
        """List saved workflows."""
        return self.client._json(
            "GET", self.client.node_url + "/api/v2/workflow/workflows",
            op="workflow.list")

    def get(self, workflow_id: str) -> dict[str, Any]:
        """Fetch a single workflow definition."""
        if not workflow_id:
            raise ValueError("workflow.get: workflow_id is required")
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/workflow/workflows/{workflow_id}",
            op="workflow.get")

    def create(self, definition: dict[str, Any]) -> dict[str, Any]:
        """Create a new workflow from a node-graph definition."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/workflows",
            op="workflow.create", json_body=definition)

    def delete(self, workflow_id: str) -> dict[str, Any]:
        """Delete a workflow definition."""
        if not workflow_id:
            raise ValueError("workflow.delete: workflow_id is required")
        return self.client._json(
            "DELETE", self.client.node_url + f"/api/v2/workflow/workflows/{workflow_id}",
            op="workflow.delete")

    def save(self, definition: dict[str, Any]) -> dict[str, Any]:
        """Save (upsert) a workflow definition."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/save",
            op="workflow.save", json_body=definition)

    def publish(self, definition: dict[str, Any]) -> dict[str, Any]:
        """Publish a workflow."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/publish",
            op="workflow.publish", json_body=definition)

    # ── validation / execution ──

    def validate(self, definition: dict[str, Any]) -> dict[str, Any]:
        """Validate a workflow graph without running it."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/validate",
            op="workflow.validate", json_body=definition)

    def execute(self, payload: dict[str, Any]) -> dict[str, Any]:
        """Execute a workflow. ``payload`` is either a full definition or
        ``{"workflow_id": "…"}`` to run a saved workflow."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/execute",
            op="workflow.execute", json_body=payload)

    def test_node(self, payload: dict[str, Any]) -> dict[str, Any]:
        """Run a single node in isolation."""
        return self.client._json(
            "POST", self.client.node_url + "/api/v2/workflow/test-node",
            op="workflow.test_node", json_body=payload)

    # ── executions ──

    def list_executions(self) -> dict[str, Any]:
        """List workflow executions."""
        return self.client._json(
            "GET", self.client.node_url + "/api/v2/workflow/executions",
            op="workflow.list_executions")

    def get_execution(self, execution_id: str) -> dict[str, Any]:
        """Fetch a single execution record."""
        if not execution_id:
            raise ValueError("workflow.get_execution: execution_id is required")
        return self.client._json(
            "GET", self.client.node_url + f"/api/v2/workflow/executions/{execution_id}",
            op="workflow.get_execution")

    def cancel_execution(self, execution_id: str) -> dict[str, Any]:
        """Cancel a running execution."""
        if not execution_id:
            raise ValueError("workflow.cancel_execution: execution_id is required")
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/workflow/executions/{execution_id}/cancel",
            op="workflow.cancel_execution", json_body={})

    def delete_execution(self, execution_id: str) -> dict[str, Any]:
        """Delete an execution record."""
        if not execution_id:
            raise ValueError("workflow.delete_execution: execution_id is required")
        return self.client._json(
            "DELETE", self.client.node_url + f"/api/v2/workflow/executions/{execution_id}",
            op="workflow.delete_execution")

    # ── export / health ──

    def export(self, definition: dict[str, Any], fmt: str = "json") -> dict[str, Any]:
        """Export a workflow as ``json`` or ``yaml``."""
        if fmt not in ("json", "yaml"):
            raise ValueError("workflow.export: fmt must be 'json' or 'yaml'")
        return self.client._json(
            "POST", self.client.node_url + f"/api/v2/workflow/export/{fmt}",
            op="workflow.export", json_body=definition)

    def health(self) -> dict[str, Any]:
        """Workflow service liveness."""
        return self.client._json(
            "GET", self.client.node_url + "/api/v2/workflow/health",
            op="workflow.health")


# ── Client ─────────────────────────────────────────────────────────────

@dataclass
class Whoami:
    username: str = ""
    email: str = ""
    organization: str = ""
    workspace: str = ""


class Client:
    """Entry point. Construct with explicit credentials or load from vxcli."""

    def __init__(
        self,
        *,
        api_key: str | None = None,
        username: str | None = None,
        access_token: str = "",
        refresh_token: str = "",
        infinity_url: str = DEFAULT_INFINITY_URL,
        node_url: str = "",
        tenant_id: str = "",
        organization: str = "",
        user_agent: str = f"vxsdk-py/{__version__}",
    ):
        if not api_key and not access_token:
            raise VxError("vxsdk.Client", "no credentials: pass api_key= or access_token=")
        if api_key:
            self._validate_api_key(api_key)

        self.api_key = api_key or ""
        self.username = username or ""
        # Vault setup endpoints (/api/v2/setup/*) require both username and
        # organization in the body. vxcli mirrors getUserOrg(): org falls
        # back to username when no distinct org is configured.
        self.organization = organization or self.username
        self.access_token = access_token
        self.refresh_token = refresh_token
        self.infinity_url = infinity_url.rstrip("/")
        self.node_url = node_url.rstrip("/")
        self.tenant_id = tenant_id
        self.user_agent = user_agent

        self._lock = threading.RLock()
        self._whoami = Whoami(username=username or "")

        # Resource modules
        self.cicd = CICD(self)
        self.sessions = Sessions(self)
        self.install = Install(self)
        self.deploy = Deploy(self)
        self.marketplace = Marketplace(self)
        self.cloud = Cloud(self)
        self.metaldb = MetalDB(self)
        self.nodes = Nodes(self)
        self.services = Services(self)
        self.networks = Networks(self)
        self.agents = Agents(self)
        self.chat = Chat(self)
        self.observability = Observability(self)
        self.billing = Billing(self)
        self.workspace = Workspace(self)
        self.agentcontrol = AgentControl(self)
        self.vxcomputer = VxComputer(self)
        self.robotic = Robotic(self)
        self.vxchrono = VxChrono(self)
        self.workflow = Workflow(self)

    # ── alternate constructor ──

    @classmethod
    def load_from_vxcli(cls) -> "Client":
        """Read ~/.vxcloud/credentials.json (the file `vxcli auth login` writes)."""
        f = _load_credentials_file()
        return cls(
            api_key=f.get("api_key") or None,
            username=f.get("username"),
            access_token=f.get("access_token", ""),
            refresh_token=f.get("refresh_token", ""),
            infinity_url=f.get("base_url") or DEFAULT_INFINITY_URL,
            node_url=f.get("node_url", ""),
            tenant_id=f.get("tenant_id", "") or f.get("organization_id", ""),
            organization=f.get("organization", "") or f.get("organization_name", ""),
        )

    # ── public helpers ──

    @property
    def whoami(self) -> Whoami:
        return self._whoami

    def authenticate(self) -> None:
        """Eagerly exchange the API key for a fresh JWT pair. Optional —
        the client refreshes lazily on the first 401."""
        self._refresh()

    def ensure_node_url(self) -> str:
        """Resolve the tenant node base URL the same way the web dashboard does.

        Node-scoped endpoints (provisioning, sessions, services, metaldb, …)
        live on the user's per-tenant node, not on the infinity control plane.
        The web app derives that URL from the user's default node record at
        ``/api/v1/auth/nodes/`` (env.ts:resolveNodeUrl). The SDK mirrors that
        exactly so a client built with only ``api_key=`` + ``username=`` can
        reach node endpoints without the caller hard-coding ``node_url=``.

        Idempotent: returns the cached value once resolved. Raises VxError if
        no node record can be found.
        """
        with self._lock:
            if self.node_url:
                return self.node_url
        data = self._json(
            "GET", self.infinity_url + "/api/v1/auth/nodes/",
            op="client.ensure_node_url", target="infinity",
        )
        # _json wraps a bare JSON array as {"data": [...]}; the endpoint may
        # also return {"results": [...]} or a bare object — handle all shapes.
        if isinstance(data, dict):
            nodes = data.get("data") or data.get("results") or (
                [data] if data.get("id") or data.get("public_ip") else []
            )
        else:  # pragma: no cover - _json always returns a dict
            nodes = data
        if not nodes:
            raise VxError("client.ensure_node_url", "no node records returned for this account")
        node = next((n for n in nodes if n.get("is_default_node")), nodes[0])
        raw = (node.get("custom_domain_name") or node.get("load_balancer")
               or node.get("public_ip") or "")
        if not raw:
            raise VxError("client.ensure_node_url", "default node record has no resolvable address")
        url = raw if raw.startswith("http") else f"https://{raw}"
        with self._lock:
            self.node_url = url.rstrip("/")
        return self.node_url

    # ── internal: HTTP machinery ──

    def _validate_api_key(self, key: str) -> None:
        if not key.startswith("xc_"):
            raise VxAuthError("vxsdk.Client", "api key must start with xc_")
        parts = key.split("_", 2)
        if len(parts) != 3:
            raise VxAuthError("vxsdk.Client", "api key format: xc_<env>_<token>")
        if parts[1] not in ("dev", "test", "live"):
            raise VxAuthError("vxsdk.Client", "api key environment must be dev|test|live")
        if len(parts[2]) < 16:
            raise VxAuthError("vxsdk.Client", "api key token segment too short")

    def _ssh_fields(self, host: str, ssh_user: str, key_pair_name: str,
                    workspace_user: str | None, organization: str | None) -> dict[str, str]:
        if not (host and ssh_user and key_pair_name):
            raise ValueError("host, ssh_user, and key_pair_name are required")
        user = workspace_user or self.username
        org = organization or user
        return {
            "hostname": host, "ssh_username": ssh_user, "key_pair_name": key_pair_name,
            "username": user, "organization": org,
        }

    def _auth_headers(self, url: str = "") -> dict[str, str]:
        h: dict[str, str] = {}
        with self._lock:
            access_token = self.access_token
            api_key = self.api_key
        if access_token:
            h["Authorization"] = f"Bearer {access_token}"
        # Tenant-node routes authenticate via the developer-key middleware,
        # which accepts a lone Bearer JWT. Sending a stale or cross-workspace
        # X-API-Key alongside the JWT makes that middleware strict-compare
        # the key and return 403 ("not valid for this workspace") even though
        # the JWT is valid. For node-targeted requests, drop X-API-Key when a
        # Bearer token is present so JWT auth is used unambiguously. Control-
        # plane (infinity_url) requests keep X-API-Key untouched. Mirrors the
        # CLI-side fix in services/cli/cmd/auth.go NewAuthenticatedRequest.
        targets_node = bool(self.node_url) and url.startswith(self.node_url)
        if api_key and not (access_token and targets_node):
            h["X-API-Key"] = api_key
        return h

    def _refresh(self) -> None:
        if not self.api_key:
            raise VxAuthError("vxsdk._refresh", "no api key configured — cannot refresh JWT")
        url = self.infinity_url + "/api/v1/auth/developer/keys/login"
        body = json.dumps({"api_key": self.api_key, "username": self.username}).encode("utf-8")
        # NOTE: this is the one request that does NOT go through _do(), so it
        # must set User-Agent itself. Without it urllib sends "Python-urllib/X"
        # which Cloudflare blocks with Error 1010 — that 403 looked like a bad
        # API key but was actually the auth exchange never reaching the API.
        headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": self.user_agent,
        }
        status, _hdrs, raw = _request("POST", url, headers, body, timeout=15)
        if status != 200:
            try:
                detail = raw.decode("utf-8", "replace")[:200]
            except Exception:
                detail = ""
            raise VxAuthError("vxsdk._refresh", "exchange api key for jwt", status, detail)
        try:
            data = json.loads(raw.decode("utf-8"))
        except Exception as e:
            raise VxAuthError("vxsdk._refresh", "decode response", cause=e) from e
        with self._lock:
            self.access_token = data.get("access", "")
            self.refresh_token = data.get("refresh", "")
            user = data.get("user") or {}
            self.username = user.get("username", self.username)
            self._whoami = Whoami(
                username=user.get("username", self.username),
                email=user.get("email", ""),
                organization=(user.get("organization") or {}).get("name", "") if user.get("organization") else "",
                workspace=(user.get("workspace") or {}).get("name", "") if user.get("workspace") else "",
            )

    def _do(self, method: str, url: str, *, op: str, headers: dict[str, str], body: bytes | None,
            timeout: int, target: str = "node") -> tuple[int, dict[str, str], bytes]:
        h = dict(headers)
        h.setdefault("Accept", "application/json")
        h["User-Agent"] = self.user_agent
        h["vx-request-id"] = uuid.uuid4().hex
        h.update(self._auth_headers(url))
        max_retries = 3
        last_err: Exception | None = None
        refreshed = False
        for attempt in range(max_retries + 1):
            try:
                status, hdrs, raw = _request(method, url, h, body, timeout)
            except VxNetworkError as e:
                last_err = e
                if attempt >= max_retries:
                    raise
                time.sleep(min(0.2 * (2 ** attempt), 5.0))
                continue

            if 200 <= status < 300:
                return status, hdrs, raw

            # one-shot refresh on 401
            if status == 401 and not refreshed and self.api_key:
                refreshed = True
                try:
                    self._refresh()
                    h.update(self._auth_headers(url))
                    continue
                except VxError:
                    # fall through and surface original 401
                    pass

            try:
                detail = raw.decode("utf-8", "replace")[:800]
            except Exception:
                detail = ""
            retry_after = 0
            if status == 429:
                ra = hdrs.get("Retry-After") or hdrs.get("retry-after")
                if ra and ra.isdigit():
                    retry_after = int(ra)
            err = _from_http(op, status, _http_reason(status), detail, retry_after=retry_after)
            if attempt < max_retries and _is_retryable(err):
                last_err = err
                time.sleep(min(0.2 * (2 ** attempt), 5.0))
                continue
            raise err
        if last_err:
            raise last_err
        raise VxError(op, "exhausted retries")

    def _json(self, method: str, url: str, *, op: str, json_body: Any | None = None,
              timeout: int = DEFAULT_TIMEOUT, target: str = "node",
              headers: dict[str, str] | None = None,
              raw_body: bytes | None = None) -> dict[str, Any]:
        out_headers: dict[str, str] = dict(headers or {})
        body: bytes | None = raw_body
        if json_body is not None and raw_body is None:
            out_headers.setdefault("Content-Type", "application/json")
            body = json.dumps(json_body).encode("utf-8")
        _status, _hdrs, raw = self._do(method, url, op=op, headers=out_headers, body=body,
                                       timeout=timeout, target=target)
        if not raw:
            return {}
        try:
            data = json.loads(raw.decode("utf-8"))
        except Exception as e:
            raise VxError(op, "decode response", cause=e) from e
        if isinstance(data, list):
            return {"data": data}
        return data

    def _multipart(self, url: str, fields: dict[str, str],
                   files: list[tuple[str, str, bytes, str]], *, op: str,
                   timeout: int) -> dict[str, Any]:
        body, content_type = _multipart_body(fields, files)
        headers = {"Content-Type": content_type}
        _status, _hdrs, raw = self._do("POST", url, op=op, headers=headers, body=body,
                                       timeout=timeout)
        if not raw:
            return {}
        try:
            return json.loads(raw.decode("utf-8"))
        except Exception as e:
            raise VxError(op, "decode response", cause=e) from e


def _http_reason(status: int) -> str:
    return {
        400: "Bad Request", 401: "Unauthorized", 403: "Forbidden",
        404: "Not Found", 409: "Conflict", 422: "Unprocessable Entity",
        429: "Too Many Requests", 500: "Internal Server Error",
        502: "Bad Gateway", 503: "Service Unavailable", 504: "Gateway Timeout",
    }.get(status, "")


# ── Brand aliases ───────────────────────────────────────────────────────
# `Client` is the canonical entry-point class; these are additive aliases
# so all three of the following resolve to the same class object:
#     vxsdk.Client.load_from_vxcli()    # canonical
#     vxsdk.VxCloud.load_from_vxcli()   # mirrors the TypeScript SDK class name
#     vxsdk.vxcloud.load_from_vxcli()   # lowercase brand
VxCloud = Client
vxcloud = Client
