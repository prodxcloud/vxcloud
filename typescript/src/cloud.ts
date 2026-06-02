/**
 * Cloud resource — VM / IAM / S3 / Database / Network / Kubernetes / Serverless.
 *
 * Thin in v0.1 — covers the same endpoints the Go and Python SDKs do.
 * Full coverage tracked in BIG_PLAN.md M4 (Cloud full coverage).
 */

import type { Transport } from './transport.js';

type CloudProvider = 'aws' | 'gcp' | 'azure';
type ManagedDatabaseType = 'rds' | 'aurora' | 'redis' | 'elasticache';

interface ManagedDatabaseProvisionInput {
  name?: string;
  resourceName?: string;
  cloud?: CloudProvider;
  resourceType?: ManagedDatabaseType;
  engine?: 'postgresql' | 'postgres' | 'mysql' | 'mariadb' | 'mongodb';
  instanceClass?: string;
  storageGb?: number;
  region: string;
  configuration?: Record<string, unknown>;
  tags?: Record<string, string>;
}

export class Cloud {
  readonly vm: VM;
  readonly s3: S3;
  readonly iam: IAM;
  readonly database: Database;
  readonly kubernetes: Kubernetes;
  readonly network: Network;
  readonly serverless: Serverless;

  constructor(private readonly t: Transport) {
    this.vm = new VM(t);
    this.s3 = new S3(t);
    this.iam = new IAM(t);
    this.database = new Database(t);
    this.kubernetes = new Kubernetes(t);
    this.network = new Network(t);
    this.serverless = new Serverless(t);
  }

  // Flat shortcut that mirrors the Python SDK's `c.cloud.create_vm(...)`.
  // Forwards to `this.vm.provision(...)`; identical surface, just lets users
  // who reach for the Python shape from TS land in the same place.
  async createVm(input: VMProvisionInput): Promise<Record<string, unknown>> {
    return this.vm.provision(input);
  }
}

export interface VMProvisionInput {
  name?: string;
  cloud?: 'aws' | 'gcp' | 'azure';
  instanceType: string;
  region: string;
  keyPairName?: string;
  volumeSize?: number;
  tags?: Record<string, string>;
  ami?: string;
}

export class VM {
  constructor(private t: Transport) {}

  async provision(input: VMProvisionInput): Promise<Record<string, unknown>> {
    const cloud = input.cloud ?? 'aws';
    // When the caller passes `name`, build the verbose body the Python SDK
    // uses (and that prod has been verified against). When not, keep the
    // slim body that existing TS callers already rely on — no behavior
    // change for code that doesn't opt in to the named shape.
    const body: Record<string, unknown> = input.name
      ? {
          app_name: input.name,
          resource_name: input.name,
          instance_name: input.name,
          network_name: input.name,
          key_name: input.name,
          role_name: input.name,
          hostname: input.name,
          cloud_provider: cloud,
          provider: cloud,
          region: input.region,
          environment: 'development',
          resource_type: 'vm',
          instance_type: input.instanceType,
          volume_size: input.volumeSize ?? 30,
          key_pair_name: input.keyPairName,
          tags: input.tags ?? {},
          ami: input.ami,
        }
      : {
          provider: cloud,
          instance_type: input.instanceType,
          region: input.region,
          key_pair_name: input.keyPairName,
          tags: input.tags ?? {},
          ami: input.ami,
        };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/vm',
      body,
    );
    return res.body ?? {};
  }

  async status(input: { instanceId: string; cloud?: 'aws' | 'gcp' | 'azure' }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/provision/vm/status',
      { instance_id: input.instanceId, provider: input.cloud ?? 'aws' },
    );
    return res.body ?? {};
  }

  async action(input: {
    instanceId: string;
    action: 'start' | 'stop' | 'restart' | 'reboot';
    cloud?: 'aws' | 'gcp' | 'azure';
  }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/provision/vm/action',
      { instance_id: input.instanceId, action: input.action, provider: input.cloud ?? 'aws' },
    );
    return res.body ?? {};
  }
}

export class S3 {
  constructor(private t: Transport) {}

  async createBucket(input: { name: string; region?: string; cloud?: 'aws' | 'gcp' | 'azure' }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/s3',
      { bucket_name: input.name, region: input.region ?? 'us-east-1', provider: input.cloud ?? 'aws' },
    );
    return res.body ?? {};
  }
}

export class IAM {
  constructor(private t: Transport) {}

  async createPolicy(input: { name: string; document: string | Record<string, unknown> }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/iam/policy',
      {
        name: input.name,
        policy_document: typeof input.document === 'string' ? input.document : JSON.stringify(input.document),
      },
    );
    return res.body ?? {};
  }

  async createRole(input: { name: string; assumeRolePolicy: string | Record<string, unknown> }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/iam/role',
      {
        name: input.name,
        assume_role_policy: typeof input.assumeRolePolicy === 'string'
          ? input.assumeRolePolicy
          : JSON.stringify(input.assumeRolePolicy),
      },
    );
    return res.body ?? {};
  }

  /** Provision a key pair. Private key is stored in the workspace vault
   *  under `name`; the public material is uploaded to the cloud. */
  async createKeypair(input: { name: string; region?: string; cloud?: CloudProvider }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/security',
      commonProvisionBody({
        name: input.name,
        cloud: input.cloud ?? 'aws',
        region: input.region ?? 'us-east-1',
        resourceType: 'keypair',
      }),
    );
    return res.body ?? {};
  }
}

export class Database {
  constructor(private t: Transport) {}

  async provision(input: ManagedDatabaseProvisionInput): Promise<Record<string, unknown>> {
    const resourceType = input.resourceType ?? 'rds';
    const name = input.name ?? input.resourceName ?? String(input.configuration?.db_name ?? `${resourceType}-database`);
    const configuration: Record<string, unknown> = {
      region: input.region,
      ...(input.engine ? { engine: input.engine } : {}),
      ...(input.instanceClass ? { instance_type: input.instanceClass } : {}),
      ...(input.storageGb ? { storage_size: input.storageGb } : {}),
      ...(input.configuration ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/databases',
      {
        cloud_provider: input.cloud ?? 'aws',
        resource_type: resourceType,
        resource_name: name,
        configuration,
        tags: input.tags ?? { Name: name, ManagedBy: '@vxcloud/sdk' },
      },
    );
    return res.body ?? {};
  }

  async createRDS(input: Omit<ManagedDatabaseProvisionInput, 'resourceType'>): Promise<Record<string, unknown>> {
    return this.provision({
      ...input,
      resourceType: 'rds',
      configuration: {
        engine: input.engine ?? 'mysql',
        version: input.configuration?.version ?? '8.0',
        instance_type: input.instanceClass ?? input.configuration?.instance_type ?? 'db.t3.micro',
        storage_size: input.storageGb ?? input.configuration?.storage_size ?? 20,
        username: input.configuration?.username ?? 'admin',
        db_name: input.configuration?.db_name ?? input.name ?? input.resourceName,
        port: input.configuration?.port ?? ((input.engine === 'postgres' || input.engine === 'postgresql') ? 5432 : 3306),
        ...input.configuration,
      },
    });
  }

  async createAurora(input: Omit<ManagedDatabaseProvisionInput, 'resourceType'>): Promise<Record<string, unknown>> {
    return this.provision({
      ...input,
      resourceType: 'aurora',
      configuration: {
        engine: input.engine ?? 'mysql',
        version: input.configuration?.version ?? '8.0',
        instance_type: input.instanceClass ?? input.configuration?.instance_type ?? 'db.t3.medium',
        instance_count: input.configuration?.instance_count ?? 2,
        username: input.configuration?.username ?? 'admin',
        db_name: input.configuration?.db_name ?? input.name ?? input.resourceName,
        port: input.configuration?.port ?? ((input.engine === 'postgres' || input.engine === 'postgresql') ? 5432 : 3306),
        ...input.configuration,
      },
    });
  }

  async createRedis(input: Omit<ManagedDatabaseProvisionInput, 'resourceType' | 'engine' | 'instanceClass' | 'storageGb'>): Promise<Record<string, unknown>> {
    return this.provision({
      ...input,
      resourceType: 'redis',
      configuration: {
        node_type: input.configuration?.node_type ?? 'cache.t3.micro',
        num_cache_nodes: input.configuration?.num_cache_nodes ?? 1,
        ...input.configuration,
      },
    });
  }
}

export class Kubernetes {
  constructor(private t: Transport) {}

  async provision(input: {
    cloud: 'aws' | 'gcp' | 'azure';
    clusterName: string;
    nodeCount: number;
    nodeType: string;
    region: string;
  }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/kubernetescluster/deploy',
      {
        provider: input.cloud,
        cluster_name: input.clusterName,
        node_count: input.nodeCount,
        node_type: input.nodeType,
        region: input.region,
      },
    );
    return res.body ?? {};
  }

  async listClusters(): Promise<unknown[]> {
    const res = await this.t.get<{ clusters?: unknown[] } | unknown[]>('/api/v2/tenant/kubernetes/clusters');
    if (Array.isArray(res.body)) return res.body;
    return (res.body as { clusters?: unknown[] })?.clusters ?? [];
  }

  async clusterDetails(name: string): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/kubernetes/cluster/details', { name },
    );
    return res.body ?? {};
  }
}

/** VPC / network provisioning. Mirrors Go `cloud.Network`. */
export class Network {
  constructor(private t: Transport) {}

  async createVpc(input: { name: string; cloud?: CloudProvider; region?: string }): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/networks',
      commonProvisionBody({
        name: input.name,
        cloud: input.cloud ?? 'aws',
        region: input.region ?? 'us-east-1',
        resourceType: 'vpc',
      }),
    );
    return res.body ?? {};
  }
}

/** Lambda / Cloud Functions provisioning. Mirrors Go `cloud.Serverless`. */
export class Serverless {
  constructor(private t: Transport) {}

  async createFunction(input: {
    name: string;
    cloud?: CloudProvider;
    region?: string;
    runtime: string;
  }): Promise<Record<string, unknown>> {
    if (!input.runtime) throw new Error('serverless.createFunction: runtime is required');
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/provision/serverless',
      {
        ...commonProvisionBody({
          name: input.name,
          cloud: input.cloud ?? 'aws',
          region: input.region ?? 'us-east-1',
          resourceType: 'lambda',
        }),
        runtime: input.runtime,
      },
    );
    return res.body ?? {};
  }
}

/** Build the canonical envelope `/api/v2/tenant/provision/*` handlers expect.
 *  Mirrors Go `commonProvision` and Python `Cloud._provision`. */
function commonProvisionBody(input: {
  name: string;
  cloud: CloudProvider;
  region: string;
  resourceType: string;
  env?: string;
}): Record<string, unknown> {
  return {
    app_name: input.name,
    resource_name: input.name,
    instance_name: input.name,
    network_name: input.name,
    key_name: input.name,
    role_name: input.name,
    hostname: input.name,
    cloud_provider: input.cloud,
    region: input.region,
    environment: input.env ?? 'development',
    resource_type: input.resourceType,
  };
}
