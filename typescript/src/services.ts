/**
 * Services resource — lifecycle plane for containers + host operations.
 *
 * Mirrors `vxcli services` (services/cli/cmd/services.go).
 *
 * Container ops use JSON endpoints:
 *   POST /api/v2/tenant/container/{start,stop,remove}    JSON
 *   POST /api/v2/tenant/docker/container/status          JSON
 *
 * Host ops use the multipart admin-action endpoint:
 *   POST /api/v2/tenant/services/action                   multipart
 *
 * The whitelist of host actions is documented in services/dockerservices/
 * dockerservices.go (RunServiceAction). This SDK only exposes safe ones.
 */

import type { Transport, MultipartFile } from './transport.js';
import type { SSHTarget, ContainerStatus, ContainerSummary, ActionResponse, HostAction } from './types.js';
import { sshFields, attachKeyPair } from './internal.js';

export class Services {
  readonly vm: ServicesVM;

  constructor(private t: Transport) {
    this.vm = new ServicesVM(t);
  }

  /** List all containers on the remote host (id, name, status, ports). */
  async list(target: SSHTarget): Promise<ContainerSummary[]> {
    const res = await runSvcAction(this.t, target, 'list_docker_containers');
    return parseContainerList(res.output ?? '');
  }

  /** Inspect a single container by name. */
  async status(name: string, target: SSHTarget): Promise<ContainerStatus> {
    const body = {
      ...sshFields(target),
      service_name: name,
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/docker/container/status', body,
    );
    const b = res.body ?? {};
    return {
      success: Boolean((b as { success?: boolean }).success),
      hostname: (b as { hostname?: string }).hostname,
      total: (b as { total?: number }).total,
      containers: ((b as { containers?: ContainerSummary[] }).containers) ?? [],
      raw: b,
    };
  }

  /** Start a stopped container. */
  async start(name: string, target: SSHTarget): Promise<Record<string, unknown>> {
    return containerLifecycle(this.t, 'start', name, target);
  }

  /** Stop a running container. */
  async stop(name: string, target: SSHTarget): Promise<Record<string, unknown>> {
    return containerLifecycle(this.t, 'stop', name, target);
  }

  /** Stop then start (no native restart endpoint on the server). */
  async restart(name: string, target: SSHTarget): Promise<{ stop: unknown; start: unknown }> {
    const stop = await containerLifecycle(this.t, 'stop', name, target);
    const start = await containerLifecycle(this.t, 'start', name, target);
    return { stop, start };
  }

  /** Stop and remove a container. Caller is expected to confirm `--yes` upstream. */
  async remove(name: string, target: SSHTarget): Promise<Record<string, unknown>> {
    return containerLifecycle(this.t, 'remove', name, target);
  }

  /**
   * Tail journalctl logs for a systemd unit (NOT docker container logs).
   * The server template hard-codes -n 50 today; the `tail` parameter is
   * accepted for forward-compat.
   */
  async logs(unit: string, target: SSHTarget, _opts?: { tail?: number }): Promise<string> {
    if (!unit) throw new Error('services.logs: unit name is required');
    const res = await runSvcAction(this.t, target, 'tail_logs', unit);
    return res.output ?? '';
  }
}

/**
 * `services.vm.*` — host-level operations gated by the server-side
 * whitelist.  Each method maps 1:1 to a vxcli `services vm <verb>`.
 */
export class ServicesVM {
  constructor(private t: Transport) {}

  reboot(target: SSHTarget):           Promise<ActionResponse> { return runSvcAction(this.t, target, 'reboot'); }
  shutdown(target: SSHTarget):         Promise<ActionResponse> { return runSvcAction(this.t, target, 'shutdown'); }
  diskCleanup(target: SSHTarget):      Promise<ActionResponse> { return runSvcAction(this.t, target, 'disk_cleanup'); }
  dockerCleanup(target: SSHTarget):    Promise<ActionResponse> { return runSvcAction(this.t, target, 'docker_cleanup'); }
  restartDocker(target: SSHTarget):    Promise<ActionResponse> { return runSvcAction(this.t, target, 'restart_docker'); }
  memory(target: SSHTarget):           Promise<ActionResponse> { return runSvcAction(this.t, target, 'check_memory'); }
  disk(target: SSHTarget):             Promise<ActionResponse> { return runSvcAction(this.t, target, 'check_disk_detailed'); }
  listServices(target: SSHTarget):     Promise<ActionResponse> { return runSvcAction(this.t, target, 'list_running_services'); }
  listContainers(target: SSHTarget):   Promise<ActionResponse> { return runSvcAction(this.t, target, 'list_docker_containers'); }
  killPort(port: number | string, target: SSHTarget): Promise<ActionResponse> {
    return runSvcAction(this.t, target, 'kill_port', String(port));
  }
  stopService(unit: string, target: SSHTarget): Promise<ActionResponse> {
    return runSvcAction(this.t, target, 'stop_service', unit);
  }
}

// ────────────────────────────────────────────────────────────────────────
// shared helpers
// ────────────────────────────────────────────────────────────────────────

async function containerLifecycle(
  t: Transport, action: 'start' | 'stop' | 'remove',
  name: string, target: SSHTarget,
): Promise<Record<string, unknown>> {
  if (!name) throw new Error('container name is required');
  const body = {
    ...sshFields(target),
    container_name: name,
  };
  const res = await t.postJSON<Record<string, unknown>>(
    `/api/v2/tenant/container/${action}`, body,
  );
  return res.body ?? {};
}

async function runSvcAction(
  t: Transport, target: SSHTarget, action: HostAction, targetArg = '',
): Promise<ActionResponse> {
  const fields: Record<string, string> = {
    ...sshFields(target),
    action,
  };
  if (targetArg) fields.target = targetArg;
  const files: MultipartFile[] = [];
  attachKeyPair(target, files);
  const res = await t.postMultipart<Record<string, unknown>>(
    '/api/v2/tenant/services/action', fields, files,
  );
  const b = res.body ?? {};
  return {
    success: Boolean((b as { success?: boolean }).success),
    output: (b as { output?: string }).output,
    message: (b as { message?: string }).message,
    raw: b,
  };
}

/**
 * Parse the `docker ps -a --format "table ..."` output the server returns
 * for `list_docker_containers`. Tab-separated columns:
 *   CONTAINER ID  NAMES  STATUS  PORTS
 * The first line is the header; we skip it.
 */
function parseContainerList(out: string): ContainerSummary[] {
  const lines = out.split('\n').filter((l) => l.trim().length > 0);
  if (lines.length === 0) return [];
  // Drop header
  const rows = lines[0]?.startsWith('CONTAINER') ? lines.slice(1) : lines;
  return rows.map((row) => {
    const cols = row.split(/\s{2,}|\t+/);
    return {
      id: cols[0] ?? '',
      name: cols[1] ?? '',
      image: '',
      status: cols[2] ?? '',
      ports: cols[3] ?? '',
    };
  });
}
