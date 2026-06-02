/**
 * Marketplace resource — agents, models, solutions.
 */

import type { Transport } from './transport.js';
import type { MarketplaceItem, SSHTarget } from './types.js';
import { sshFields } from './internal.js';

export class Marketplace {
  readonly agents: MarketplaceList;
  readonly models: MarketplaceList;
  readonly solutions: MarketplaceList;

  constructor(t: Transport) {
    this.agents = new MarketplaceList(t, 'agents');
    this.models = new MarketplaceList(t, 'models');
    this.solutions = new MarketplaceList(t, 'templates'); // Terraform templates
  }
}

export class MarketplaceList {
  constructor(private t: Transport, private kind: 'agents' | 'models' | 'templates') {}

  async list(): Promise<MarketplaceItem[]> {
    const res = await this.t.get<{ items?: MarketplaceItem[] } | MarketplaceItem[]>(
      `/api/v2/marketplace/${this.kind}`,
    );
    if (Array.isArray(res.body)) return res.body.map(wrap);
    return ((res.body as { items?: MarketplaceItem[] })?.items ?? []).map(wrap);
  }

  async show(id: string): Promise<MarketplaceItem> {
    const res = await this.t.get<Record<string, unknown>>(
      `/api/v2/marketplace/${this.kind}/${encodeURIComponent(id)}`,
    );
    return wrap(res.body);
  }

  /**
   * Deploy an agent or model onto a remote VM. The marketplace catalog
   * includes a Dockerfile per item; the server pulls/builds and runs it.
   */
  async deploy(
    id: string,
    target: SSHTarget,
    extra?: Record<string, string>,
  ): Promise<Record<string, unknown>> {
    if (this.kind === 'templates') {
      throw new Error("Use marketplace.solutions.provision() for templates, not deploy()");
    }
    const body = {
      ...sshFields(target),
      [this.kind === 'agents' ? 'agent_id' : 'model_id']: id,
      ...(extra ?? {}),
    };
    const res = await this.t.postJSON<Record<string, unknown>>(
      `/api/v2/marketplace/${this.kind}/${encodeURIComponent(id)}/deploy`, body,
    );
    return res.body ?? {};
  }

  /** Provision a Terraform-based solution (templates only). */
  async provision(id: string, opts: {
    resourceName: string;
    cloudProvider: 'aws' | 'gcp' | 'azure';
    region: string;
    inputs?: Record<string, unknown>;
  }): Promise<Record<string, unknown>> {
    if (this.kind !== 'templates') {
      throw new Error('provision() is only valid on marketplace.solutions');
    }
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/marketplace/provision',
      {
        template_id: id,
        resource_name: opts.resourceName,
        cloud_provider: opts.cloudProvider,
        region: opts.region,
        inputs: opts.inputs ?? {},
      },
    );
    return res.body ?? {};
  }
}

function wrap(input: unknown): MarketplaceItem {
  const b = (typeof input === 'object' && input !== null) ? input as Record<string, unknown> : {};
  return {
    id: (b.id as string) ?? '',
    name: b.name as string | undefined,
    category: b.category as string | undefined,
    description: b.description as string | undefined,
    version: b.version as string | undefined,
    raw: b,
  };
}
