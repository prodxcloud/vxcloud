/**
 * Tenant nodes resource — list, current, set-default.
 *
 * Backed by the Infinity control plane (the URL passed as `infinityURL`),
 * NOT the active tenant node. Each node corresponds to one regional
 * control plane (e.g. node1.prodxcloud.com).
 */

import type { Transport } from './transport.js';
import type { NodeInfo } from './types.js';

export class Nodes {
  constructor(private t: Transport) {}

  async list(): Promise<NodeInfo[]> {
    const res = await this.t.get<{ nodes?: NodeInfo[] } | NodeInfo[]>('/api/v1/auth/nodes/');
    if (Array.isArray(res.body)) return res.body.map(wrap);
    return ((res.body as { nodes?: NodeInfo[] })?.nodes ?? []).map(wrap);
  }

  async current(): Promise<NodeInfo | null> {
    const list = await this.list();
    return list.find((n) => n.isDefault) ?? list[0] ?? null;
  }

  async setDefault(idOrName: string | number): Promise<{ success: boolean; node?: NodeInfo }> {
    const res = await this.t.postJSON<{ success: boolean; node?: NodeInfo }>(
      '/api/v1/auth/nodes/default',
      { id: idOrName },
    );
    return res.body ?? { success: false };
  }

  /** Register a self-hosted vxnode container (BYO hardware). Mirrors the
   *  dashboard's SelfHostedNodeForm. POST /api/v1/auth/nodes/self-hosted. */
  async registerSelfHosted(input: {
    hostname: string;
    customDomainName: string;
    port?: number;
    publicIp?: string;
    privateIp?: string;
    tunnelProvider?: 'cloudflare' | 'tailscale' | 'caddy' | 'direct';
    keyPairName?: string;
    ideConnectionToken?: string;
    agent1ConnectionToken?: string;
    storageType?: 'ssd' | 'hdd' | 'nvme';
    storageBackupMode?: 'none' | 'daily' | 'weekly';
    description?: string;
    sshUsername?: string;
  }): Promise<NodeInfo> {
    if (!input.hostname || !input.customDomainName) {
      throw new Error('nodes.registerSelfHosted: hostname + customDomainName required');
    }
    const body: Record<string, unknown> = {
      hostname: input.hostname,
      custom_domain_name: input.customDomainName.replace(/^https?:\/\//, '').replace(/\/+$/, ''),
      port: input.port ?? 8744,
      tunnel_provider: input.tunnelProvider ?? 'cloudflare',
      storage_type: input.storageType ?? 'ssd',
      storage_backup_mode: input.storageBackupMode ?? 'none',
    };
    if (input.publicIp) body.public_ip = input.publicIp;
    if (input.privateIp) body.private_ip = input.privateIp;
    if (input.keyPairName) body.key_pair_name = input.keyPairName;
    if (input.ideConnectionToken) body.ide_connection_token = input.ideConnectionToken;
    if (input.agent1ConnectionToken) body.agent1_connection_token = input.agent1ConnectionToken;
    if (input.description) body.description = input.description;
    if (input.sshUsername) body.ssh_username = input.sshUsername;
    const res = await this.t.postJSON<NodeInfo>(
      '/api/v1/auth/nodes/self-hosted', body,
    );
    return wrap(res.body ?? {});
  }

  /** Delete a node record. DELETE /api/v1/auth/nodes/{id}. The caller is
   *  responsible for terminating any underlying VM first. */
  async delete(id: string | number): Promise<{ status: string; message?: string }> {
    if (!id && id !== 0) throw new Error('nodes.delete: id is required');
    const res = await this.t.delete<{ status: string; message?: string }>(
      `/api/v1/auth/nodes/${encodeURIComponent(String(id))}`,
    );
    return res.body ?? { status: 'ok' };
  }

  /** Partial-update an existing node. PATCH /api/v1/auth/nodes/{id}. Only
   *  fields you set are sent. Backend rejects `publicIp` / `instanceId` —
   *  those are platform-managed. */
  async update(id: string | number, patch: {
    hostname?: string;
    customDomainName?: string;
    loadBalancer?: string;
    privateIp?: string;
    status?: 'active' | 'running' | 'stopped' | 'provisioning' | 'pending' | 'failed' | 'terminated';
    isDefaultNode?: boolean;
    providerComputeType?: string;
    storageType?: 'ssd' | 'hdd' | 'nvme';
    storageBackupMode?: 'none' | 'daily' | 'weekly';
    storageBackupAddress?: string;
    installationChecklist?: Array<Record<string, unknown>>;
    enabledFeatures?: unknown[];
    vpnAccessDetails?: Record<string, unknown>;
    tunnelVm?: Record<string, unknown>;
  }): Promise<NodeInfo> {
    if (!id && id !== 0) throw new Error('nodes.update: id is required');
    const body: Record<string, unknown> = {};
    if (patch.hostname !== undefined) body.hostname = patch.hostname;
    if (patch.customDomainName !== undefined) {
      body.custom_domain_name = patch.customDomainName.replace(/^https?:\/\//, '').replace(/\/+$/, '');
    }
    if (patch.loadBalancer !== undefined) body.load_balancer = patch.loadBalancer;
    if (patch.privateIp !== undefined) body.private_ip = patch.privateIp;
    if (patch.status !== undefined) body.status = patch.status;
    if (patch.isDefaultNode !== undefined) body.is_default_node = patch.isDefaultNode;
    if (patch.providerComputeType !== undefined) body.provider_compute_type = patch.providerComputeType;
    if (patch.storageType !== undefined) body.storage_type = patch.storageType;
    if (patch.storageBackupMode !== undefined) body.storage_backup_mode = patch.storageBackupMode;
    if (patch.storageBackupAddress !== undefined) body.storage_backup_address = patch.storageBackupAddress;
    if (patch.installationChecklist !== undefined) body.installation_checklist = patch.installationChecklist;
    if (patch.enabledFeatures !== undefined) body.enabled_features = patch.enabledFeatures;
    if (patch.vpnAccessDetails !== undefined) body.vpn_access_details = patch.vpnAccessDetails;
    if (patch.tunnelVm !== undefined) body.tunnel_vm = patch.tunnelVm;
    if (Object.keys(body).length === 0) {
      throw new Error('nodes.update: at least one field must be set');
    }
    const res = await this.t.patchJSON<NodeInfo>(
      `/api/v1/auth/nodes/${encodeURIComponent(String(id))}`, body,
    );
    return wrap(res.body ?? {});
  }
}

function wrap(n: NodeInfo | Record<string, unknown>): NodeInfo {
  const b = n as Record<string, unknown>;
  return {
    id: (b.id as string | number) ?? '',
    name: (b.name as string) ?? '',
    url: b.url as string | undefined,
    isDefault: Boolean(b.is_default ?? b.default),
    raw: b,
  };
}
