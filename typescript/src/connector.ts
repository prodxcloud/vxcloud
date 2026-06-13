/**
 * Connector — SDK-direct cloud deploys (no Terraform).
 *
 * Mirrors `connector/connector.go` and Python `vxsdk.Connector`. Unlike `cloud`
 * (which provisions via server-side Terraform under /api/v2/tenant/provision/*),
 * connector targets the node's /api/v2/connector/* surface, which provisions by
 * calling cloud-provider SDKs/REST directly — no Terraform, no Bash, no state:
 *
 *   - EC2 VM provision / studio deploy (SCP+SSH) / terminate
 *   - S3 static-site deploy and bucket creation
 *   - GCP Cloud Run container deploy (Cloud Run Admin REST v2)
 *   - Route53 subdomain records
 *   - ALB attach with target group
 *
 * Every request carries `username` for the Vault-backed credential lookup the
 * node performs. When omitted it defaults to the client's authed username.
 */

import type { Transport } from './transport.js';

/** A decoded JSON object response. */
export type Result = Record<string, unknown>;

/** Provision a single EC2 instance (no file deploy). */
export interface ConnectorVMInput {
  instanceName: string;
  instanceType?: string;
  region?: string;
  os?: string;
  volumeSize?: number;
  keyPairName?: string;
  sessionId?: string;
  organization?: string;
  tags?: Record<string, string>;
  /** Workspace owner for the Vault credential lookup. Defaults to the client's authed username. */
  username?: string;
}

/** Provision an EC2 instance then push a studio project to it over SFTP. */
export interface ConnectorDeployVMInput extends ConnectorVMInput {
  sessionId: string;
  storagePath?: string;
  subdomain?: string;
  deployCommand?: string;
}

/** Create a single S3 bucket. */
export interface ConnectorBucketInput {
  bucketName: string;
  region?: string;
  public?: boolean;
  organization?: string;
  username?: string;
}

/** Deploy a studio project to S3 as a static website. */
export interface ConnectorStaticSiteInput {
  sessionId: string;
  storagePath?: string;
  bucketName?: string;
  region?: string;
  indexDocument?: string;
  errorDocument?: string;
  subdomain?: string;
  customDomain?: string;
  organization?: string;
  username?: string;
}

/** Deploy a container image to Google Cloud Run. */
export interface ConnectorCloudRunInput {
  serviceName: string;
  image: string;
  region?: string;
  port?: number;
  envVars?: Record<string, string>;
  memory?: string;
  cpu?: string;
  maxInstances?: number;
  subdomain?: string;
  allowPublic?: boolean;
  sessionId?: string;
  organization?: string;
  username?: string;
}

/** Create a Route53 DNS record. */
export interface ConnectorSubdomainInput {
  subdomain: string;
  baseDomain?: string;
  hostedZoneId?: string;
  targetIp?: string;
  targetCname?: string;
  recordType?: string;
  ttl?: number;
  organization?: string;
  username?: string;
}

/** Attach instances to an application load balancer (creates ALB + target group). */
export interface ConnectorLBInput {
  name: string;
  instanceIds: string[];
  port?: number;
  healthPath?: string;
  region?: string;
  subdomain?: string;
  vpcId?: string;
  subnetIds?: string[];
  organization?: string;
  username?: string;
}

export class Connector {
  constructor(private t: Transport, private getUsername: () => string) {}

  private u(username?: string): string {
    return (username && username.trim()) || this.getUsername();
  }

  private async post(path: string, body: Record<string, unknown>): Promise<Result> {
    return (await this.t.postJSON<Result>(path, body)).body ?? {};
  }

  /** Launch a single EC2 instance (no file deploy). POST /api/v2/connector/vm/provision */
  async provisionVm(input: ConnectorVMInput): Promise<Result> {
    if (!input.instanceName) throw new Error('connector.provisionVm: instanceName is required');
    return this.post('/api/v2/connector/vm/provision', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        session_id: input.sessionId,
        instance_name: input.instanceName,
        instance_type: input.instanceType,
        region: input.region,
        os: input.os,
        volume_size: input.volumeSize,
        key_pair_name: input.keyPairName,
        tags: input.tags,
      }),
    });
  }

  /** Provision an EC2 instance then push a studio project to it. POST /api/v2/connector/vm/studio/deploy */
  async deployToVm(input: ConnectorDeployVMInput): Promise<Result> {
    if (!input.sessionId) throw new Error('connector.deployToVm: sessionId is required');
    return this.post('/api/v2/connector/vm/studio/deploy', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        session_id: input.sessionId,
        storage_path: input.storagePath,
        instance_name: input.instanceName,
        instance_type: input.instanceType,
        region: input.region,
        os: input.os,
        volume_size: input.volumeSize,
        key_pair_name: input.keyPairName,
        subdomain: input.subdomain,
        deploy_command: input.deployCommand,
        tags: input.tags,
      }),
    });
  }

  /** Terminate an EC2 instance. POST /api/v2/connector/vm/terminate */
  async terminateVm(instanceId: string, organization?: string): Promise<Result> {
    if (!instanceId) throw new Error('connector.terminateVm: instanceId is required');
    return this.post('/api/v2/connector/vm/terminate', compact({
      username: this.u(),
      organization,
      instance_id: instanceId,
    }));
  }

  /** Create a single S3 bucket. POST /api/v2/connector/s3/bucket */
  async createBucket(input: ConnectorBucketInput): Promise<Result> {
    if (!input.bucketName) throw new Error('connector.createBucket: bucketName is required');
    return this.post('/api/v2/connector/s3/bucket', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        bucket_name: input.bucketName,
        region: input.region,
        public: input.public,
      }),
    });
  }

  /** Deploy a studio project to S3 as a static website. POST /api/v2/connector/s3/studio/deploy */
  async deployStaticSite(input: ConnectorStaticSiteInput): Promise<Result> {
    if (!input.sessionId) throw new Error('connector.deployStaticSite: sessionId is required');
    return this.post('/api/v2/connector/s3/studio/deploy', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        session_id: input.sessionId,
        storage_path: input.storagePath,
        bucket_name: input.bucketName,
        region: input.region,
        index_document: input.indexDocument,
        error_document: input.errorDocument,
        subdomain: input.subdomain,
        custom_domain: input.customDomain,
      }),
    });
  }

  /** Deploy a container image to Google Cloud Run. POST /api/v2/connector/gcr/studio/deploy */
  async deployCloudRun(input: ConnectorCloudRunInput): Promise<Result> {
    if (!input.serviceName) throw new Error('connector.deployCloudRun: serviceName is required');
    if (!input.image) throw new Error('connector.deployCloudRun: image is required');
    return this.post('/api/v2/connector/gcr/studio/deploy', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        session_id: input.sessionId,
        service_name: input.serviceName,
        region: input.region,
        image: input.image,
        port: input.port,
        env_vars: input.envVars,
        memory: input.memory,
        cpu: input.cpu,
        max_instances: input.maxInstances,
        subdomain: input.subdomain,
        allow_public: input.allowPublic,
      }),
    });
  }

  /** Create a Route53 DNS record. POST /api/v2/connector/dns/subdomain */
  async createSubdomain(input: ConnectorSubdomainInput): Promise<Result> {
    if (!input.subdomain) throw new Error('connector.createSubdomain: subdomain is required');
    return this.post('/api/v2/connector/dns/subdomain', {
      username: this.u(input.username),
      ...compact({
        organization: input.organization,
        subdomain: input.subdomain,
        base_domain: input.baseDomain,
        hosted_zone_id: input.hostedZoneId,
        target_ip: input.targetIp,
        target_cname: input.targetCname,
        record_type: input.recordType,
        ttl: input.ttl,
      }),
    });
  }

  /** Attach instances to an application load balancer. POST /api/v2/connector/lb/attach */
  async attachLoadBalancer(input: ConnectorLBInput): Promise<Result> {
    if (!input.name) throw new Error('connector.attachLoadBalancer: name is required');
    if (!input.instanceIds?.length) throw new Error('connector.attachLoadBalancer: instanceIds is required');
    return this.post('/api/v2/connector/lb/attach', {
      username: this.u(input.username),
      instance_ids: input.instanceIds,
      ...compact({
        organization: input.organization,
        name: input.name,
        port: input.port,
        health_path: input.healthPath,
        region: input.region,
        subdomain: input.subdomain,
        vpc_id: input.vpcId,
        subnet_ids: input.subnetIds,
      }),
    });
  }
}

/** Drop undefined / null / empty-string fields so the node applies its own defaults. */
function compact(o: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(o)) {
    if (v === undefined || v === null || v === '') continue;
    out[k] = v;
  }
  return out;
}
