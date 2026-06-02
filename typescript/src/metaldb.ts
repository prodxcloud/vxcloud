/**
 * MetalDB — self-managed PostgreSQL provisioned over SSH onto a customer VM.
 * Mirrors Python `vxsdk.MetalDB` and the web dashboard's MetalDB wizard.
 *
 * The SSH private key is NOT sent by the client — the node looks it up in
 * the workspace vault by `keyPairName`.
 *
 * Endpoints:
 *   POST /api/v2/tenant/provision/metaldb/test-connection (multipart)
 *   POST /api/v2/tenant/provision/metaldb                 (JSON)
 */

import type { Transport } from './transport.js';
import { sshFields } from './internal.js';
import type { SSHTarget } from './types.js';

export interface MetalDBTestInput extends SSHTarget {}

export interface MetalDBProvisionInput extends SSHTarget {
  databaseName?: string;
  databaseUser?: string;
  databasePassword?: string;
  postgresPassword?: string;
  port?: string | number;
  postgresVersion?: string;
  cloudProvider?: string;
  enableReplication?: boolean;
  replicaHostname?: string;
  multiZone?: boolean;
  backupEnabled?: boolean;
  backupRetention?: number;
  resourceName?: string;
  tags?: Record<string, string>;
}

export class MetalDB {
  constructor(private t: Transport, private getUsername: () => string) {}

  /** Pre-flight SSH check — mirrors the wizard's "Test SSH Connection".
   *  Server always returns 200; check the `success` field on the body. */
  async testConnection(input: MetalDBTestInput): Promise<Record<string, unknown>> {
    const fields = sshFields(input, this.getUsername());
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/tenant/provision/metaldb/test-connection',
      fields, [],
    );
    return res.body ?? {};
  }

  /** Install PostgreSQL on the VM and create the requested DB/user.
   *  The endpoint runs synchronously — the returned dict carries
   *  status / connection_string / outputs. */
  async provision(input: MetalDBProvisionInput): Promise<Record<string, unknown>> {
    const ssh = sshFields(input, this.getUsername());
    const databaseName = input.databaseName || 'postgres';
    const body: Record<string, unknown> = {
      ...ssh,
      resource_name: input.resourceName || databaseName,
      resource_type: 'metaldb',
      cloud_provider: input.cloudProvider || 'metaldb',
      database_name: databaseName,
      database_user: input.databaseUser || 'postgres',
      database_password: input.databasePassword || 'root',
      postgres_password: input.postgresPassword || 'root',
      port: String(input.port ?? '5432'),
      postgres_version: String(input.postgresVersion ?? '16'),
      enable_replication: input.enableReplication ?? false,
      replica_hostname: input.replicaHostname ?? '',
      multi_zone: input.multiZone ?? false,
      backup_enabled: input.backupEnabled ?? true,
      backup_retention: input.backupRetention ?? 7,
    };
    if (input.tags) body.tags = input.tags;
    // Drop empty strings so the node applies its own defaults — same
    // normalization the dashboard's useProvisionStatus hook does.
    for (const [k, v] of Object.entries(body)) {
      if (v === '' || v === null || v === undefined) delete body[k];
    }
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/metaldb', body,
    );
    return res.body ?? {};
  }
}
