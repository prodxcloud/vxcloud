/**
 * Workspace setup — covers the entire /api/v2/setup/* surface (35 endpoints).
 *
 * Workspace + organization lifecycle, cloud provider creds (AWS/GCP/Azure),
 * AI provider creds (16 providers), Git/payment/SMTP/SSL/OAuth/OKTA/CyberArk
 * credential storage, and the API token lifecycle.
 *
 * The platform stores everything in HashiCorp Vault under a per-workspace
 * path; the SDK never logs request bodies.
 */

import type { Transport } from './transport.js';

export interface WorkspaceResult {
  workspaceId: string;
  status?: string;
  raw: Record<string, unknown>;
}

export interface APIToken {
  token: string;
  tokenName?: string;
  expiresAt?: string;
  raw: Record<string, unknown>;
}

export interface AWSCredentialsInput {
  accessKeyId: string;
  secretAccessKey: string;
  region?: string;
}

export interface AzureCredentialsInput {
  clientId: string;
  clientSecret: string;
  tenantId: string;
  subscriptionId: string;
}

export interface GCPCredentialsInput {
  projectId: string;
  serviceAccountKey: string; // raw JSON string
}

export interface AICredentialsInput {
  apiKey?: string;
  orgId?: string;
  endpoint?: string; // for self-hosted Ollama / Hermes
}

export type AIProvider =
  | 'anthropic' | 'openai' | 'gemini' | 'deepseek' | 'qwen'
  | 'groq' | 'mistral' | 'perplexity' | 'huggingface' | 'llama'
  | 'cohere' | 'azure-openai' | 'openclaw' | 'ollama' | 'hermes';

export interface DockerCredentialsInput {
  registryName: string;          // slug — multiple registries supported
  dockerUsername: string;
  dockerPassword: string;
  dockerEmail?: string;
  dockerServer?: string;
  registryType?: string;
}

export type DockerRegistryType =
  | 'dockerhub' | 'ecr' | 'gcr' | 'acr' | 'ghcr'
  | 'gitlab' | 'quay' | 'harbor' | 'jfrog' | 'custom';

export interface DockerRegistryInput {
  registryName: string;
  registryType: DockerRegistryType;
  registryUrl: string;
  namespace?: string;
  region?: string;
  defaultCredentialSlug?: string;
  description?: string;
  isDefault?: boolean;
}

export interface RandomCredentialInput {
  credentialName: string;
  credentialType?: string;        // free-form tag
  description?: string;
  fields?: Record<string, unknown>;
  jsonBlob?: string;              // opaque JSON document
}

export interface ServerInput {
  name: string;
  ipAddress: string;
  hostname?: string;
  port?: number;
  description?: string;
  keypairName?: string;
  keypairLocation?: string;
  tags?: string[];
}

export interface VMCredentialsInput {
  keyPairName: string;
  sshPublicKey?: string;
  sshPrivateKey?: string;
  vmPassword?: string;
}

export interface GitHubCredentialsInput {
  githubToken: string;
  githubTokenName?: string;
  githubUser?: string;
  sshPublicKey?: string;
  sshPrivateKey?: string;
}

const VALID_DOCKER_REGISTRY_TYPES: ReadonlySet<string> = new Set([
  'dockerhub', 'ecr', 'gcr', 'acr', 'ghcr',
  'gitlab', 'quay', 'harbor', 'jfrog', 'custom',
]);

export class Workspace {
  constructor(private t: Transport) {}

  // ── workspace + organization ──
  async createWorkspace(name: string, region?: string): Promise<WorkspaceResult> {
    if (!name) throw new Error('workspace.createWorkspace: name is required');
    const r = await this.post<Record<string, unknown>>('/api/v2/setup/workspace', {
      workspace_name: name, region: region ?? '',
    });
    return {
      workspaceId: (r.workspace_id as string) ?? '',
      status: r.status as string | undefined,
      raw: r,
    };
  }

  async createOrganization(orgName: string, plan?: string): Promise<Record<string, unknown>> {
    if (!orgName) throw new Error('workspace.createOrganization: orgName is required');
    return this.post('/api/v2/setup/organization', { org_name: orgName, plan: plan ?? '' });
  }

  async deleteWorkspace(): Promise<Record<string, unknown>> {
    const res = await this.t.delete<Record<string, unknown>>('/api/v2/setup/workspace');
    return res.body ?? {};
  }

  // ── cloud provider creds ──
  storeAWSCredentials(input: AWSCredentialsInput): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/aws-credentials', {
      access_key_id: input.accessKeyId,
      secret_access_key: input.secretAccessKey,
      region: input.region ?? 'us-east-1',
    });
  }

  storeAzureCredentials(input: AzureCredentialsInput): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/azure-credentials', {
      client_id: input.clientId,
      client_secret: input.clientSecret,
      tenant_id: input.tenantId,
      subscription_id: input.subscriptionId,
    });
  }

  storeGCPCredentials(input: GCPCredentialsInput): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/gcp-credentials', {
      project_id: input.projectId,
      service_account_key: input.serviceAccountKey,
    });
  }

  getAllCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/get-all-credentials', {});
  }

  // ── API tokens ──
  async createAPIToken(name: string, expiresInDays = 90): Promise<APIToken> {
    if (!name) throw new Error('workspace.createAPIToken: name is required');
    const r = await this.post<Record<string, unknown>>('/api/v2/setup/api-token', {
      token_name: name, expires_in_days: expiresInDays,
    });
    return {
      token: (r.token as string) ?? '',
      tokenName: r.token_name as string | undefined,
      expiresAt: r.expires_at as string | undefined,
      raw: r,
    };
  }

  async getAPIToken(name: string): Promise<APIToken> {
    if (!name) throw new Error('workspace.getAPIToken: name is required');
    const r = await this.post<Record<string, unknown>>('/api/v2/setup/get-api-token', {
      token_name: name,
    });
    return {
      token: (r.token as string) ?? '',
      tokenName: r.token_name as string | undefined,
      expiresAt: r.expires_at as string | undefined,
      raw: r,
    };
  }

  // ── AI provider creds (16 providers) ──
  storeAICredentials(provider: AIProvider | string, input: AICredentialsInput): Promise<Record<string, unknown>> {
    if (!provider) throw new Error('workspace.storeAICredentials: provider is required');
    const body: Record<string, unknown> = {};
    if (input.apiKey) body.api_key = input.apiKey;
    if (input.orgId) body.org_id = input.orgId;
    if (input.endpoint) body.endpoint = input.endpoint;
    return this.post(`/api/v2/setup/ai-${provider}-credentials`, body);
  }

  getAllAICredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/ai-get-all-credentials', {});
  }

  // ── Git / messaging / payment / SMTP / SSL / OAuth / OKTA / Vault ──
  storeGitCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/git-credentials', body);
  }
  storeGitlabCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/gitlab-credentials', body);
  }
  storeKubeconfig(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/kubeconfig-credentials', body);
  }
  storeOAuthCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/oauth-credentials', body);
  }
  storeOktaCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/okta-credentials', body);
  }
  storeCyberarkCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/cyberark-credentials', body);
  }
  storeExternalVaultCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/external-vault-credentials', body);
  }
  getVaultCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/get-vault-credentials', body);
  }
  storePaymentCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/payment-credentials', body);
  }
  getAllPaymentCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/payment-get-all-credentials', {});
  }
  storeSMTPCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/smtp-provider-credentials', body);
  }
  getAllSMTPCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/smtp-get-all-credentials', {});
  }
  storeMessagingBotCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/messaging-bot-credentials', body);
  }
  getAllMessagingCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/messaging-get-all-credentials', {});
  }
  storeSSLCertificateCredentials(body: Record<string, unknown>): Promise<Record<string, unknown>> {
    return this.post('/api/v2/setup/ssl-certificate-credentials', body);
  }

  async deleteCredential(name: string): Promise<Record<string, unknown>> {
    if (!name) throw new Error('workspace.deleteCredential: name is required');
    return this.post('/api/v2/setup/delete-credential', { name });
  }

  // ── docker credentials (multi-registry under docker/registries/<slug>) ──
  storeDockerCredentials(input: DockerCredentialsInput): Promise<Record<string, unknown>> {
    if (!input.registryName) throw new Error('workspace.storeDockerCredentials: registryName is required');
    const body: Record<string, unknown> = {
      DOCKER_USERNAME: input.dockerUsername,
      DOCKER_PASSWORD: input.dockerPassword,
      DOCKER_REGISTRY_NAME: input.registryName,
    };
    if (input.dockerEmail) body.DOCKER_EMAIL = input.dockerEmail;
    if (input.dockerServer) body.DOCKER_SERVER = input.dockerServer;
    if (input.registryType) body.DOCKER_REGISTRY_TYPE = input.registryType;
    return this.post('/api/v2/setup/docker-credentials', body);
  }

  getAllDockerCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/vault/get-docker-credentials', {});
  }

  getDockerCredentialsByRegistry(registrySlug: string): Promise<Record<string, unknown>> {
    if (!registrySlug) throw new Error('workspace.getDockerCredentialsByRegistry: registrySlug is required');
    return this.post('/api/v2/vault/get-single-docker-credentials', { registry_slug: registrySlug });
  }

  // ── docker REGISTRY endpoints (distinct from credentials) ──
  storeDockerRegistry(input: DockerRegistryInput): Promise<Record<string, unknown>> {
    if (!input.registryName) throw new Error('workspace.storeDockerRegistry: registryName is required');
    if (!VALID_DOCKER_REGISTRY_TYPES.has(input.registryType)) {
      throw new Error(
        `workspace.storeDockerRegistry: registryType must be one of ${[...VALID_DOCKER_REGISTRY_TYPES].sort().join(', ')}`);
    }
    if (!input.registryUrl) throw new Error('workspace.storeDockerRegistry: registryUrl is required');
    const body: Record<string, unknown> = {
      registry_name: input.registryName,
      registry_type: input.registryType,
      registry_url: input.registryUrl,
      is_default: !!input.isDefault,
    };
    if (input.namespace) body.namespace = input.namespace;
    if (input.region) body.region = input.region;
    if (input.defaultCredentialSlug) body.default_credential_slug = input.defaultCredentialSlug;
    if (input.description) body.description = input.description;
    return this.post('/api/v2/setup/docker-registry', body);
  }

  getAllDockerRegistries(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/vault/get-docker-registries', {});
  }

  getDockerRegistry(registrySlug: string): Promise<Record<string, unknown>> {
    if (!registrySlug) throw new Error('workspace.getDockerRegistry: registrySlug is required');
    return this.post('/api/v2/vault/get-single-docker-registry', { registry_slug: registrySlug });
  }

  deleteDockerRegistry(registrySlug: string): Promise<Record<string, unknown>> {
    if (!registrySlug) throw new Error('workspace.deleteDockerRegistry: registrySlug is required');
    return this.post('/api/v2/vault/delete-docker-registry', { registry_slug: registrySlug });
  }

  // ── random / generic credentials (free-form bucket) ──
  storeRandomCredential(input: RandomCredentialInput): Promise<Record<string, unknown>> {
    if (!input.credentialName) throw new Error('workspace.storeRandomCredential: credentialName is required');
    if (input.jsonBlob) {
      try { JSON.parse(input.jsonBlob); }
      catch { throw new Error('workspace.storeRandomCredential: jsonBlob is not valid JSON'); }
    }
    const body: Record<string, unknown> = { credential_name: input.credentialName };
    if (input.credentialType) body.credential_type = input.credentialType;
    if (input.description) body.description = input.description;
    if (input.fields !== undefined) body.fields = input.fields;
    if (input.jsonBlob) body.json_blob = input.jsonBlob;
    return this.post('/api/v2/setup/random-credentials', body);
  }

  getAllRandomCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/vault/get-random-credentials', {});
  }

  getRandomCredential(credentialSlug: string): Promise<Record<string, unknown>> {
    if (!credentialSlug) throw new Error('workspace.getRandomCredential: credentialSlug is required');
    return this.post('/api/v2/vault/get-single-random-credential', { credential_slug: credentialSlug });
  }

  deleteRandomCredential(credentialSlug: string): Promise<Record<string, unknown>> {
    if (!credentialSlug) throw new Error('workspace.deleteRandomCredential: credentialSlug is required');
    return this.post('/api/v2/vault/delete-random-credential', { credential_slug: credentialSlug });
  }

  // ── servers list (developer host inventory) ──
  storeServer(input: ServerInput): Promise<Record<string, unknown>> {
    if (!input.name) throw new Error('workspace.storeServer: name is required');
    if (!input.ipAddress) throw new Error('workspace.storeServer: ipAddress is required');
    const port = input.port ?? 22;
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      throw new Error('workspace.storeServer: port must be an integer between 1 and 65535');
    }
    const body: Record<string, unknown> = {
      name: input.name,
      ip_address: input.ipAddress,
      port,
    };
    if (input.hostname) body.hostname = input.hostname;
    if (input.description) body.description = input.description;
    if (input.keypairName) body.keypair_name = input.keypairName;
    if (input.keypairLocation) body.keypair_location = input.keypairLocation;
    if (input.tags && input.tags.length > 0) {
      body.tags = input.tags.map(t => String(t)).filter(t => t.trim().length > 0);
    }
    return this.post('/api/v2/setup/server', body);
  }

  getAllServers(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/vault/get-servers', {});
  }

  getServer(serverSlug: string): Promise<Record<string, unknown>> {
    if (!serverSlug) throw new Error('workspace.getServer: serverSlug is required');
    return this.post('/api/v2/vault/get-single-server', { server_slug: serverSlug });
  }

  deleteServer(serverSlug: string): Promise<Record<string, unknown>> {
    if (!serverSlug) throw new Error('workspace.deleteServer: serverSlug is required');
    return this.post('/api/v2/vault/delete-server', { server_slug: serverSlug });
  }

  // ── VM keypairs ──
  storeVMCredentials(input: VMCredentialsInput): Promise<Record<string, unknown>> {
    if (!input.keyPairName) throw new Error('workspace.storeVMCredentials: keyPairName is required');
    const body: Record<string, unknown> = { key_pair_name: input.keyPairName };
    if (input.sshPublicKey) body.SSH_PUBLIC_KEY = input.sshPublicKey;
    if (input.sshPrivateKey) body.SSH_PRIVATE_KEY = input.sshPrivateKey;
    if (input.vmPassword) body.VM_PASSWORD = input.vmPassword;
    return this.post('/api/v2/setup/vm-credentials', body);
  }

  getAllVMCredentials(): Promise<Record<string, unknown>> {
    return this.post('/api/v2/vault/get-vm-credentials', {});
  }

  getVMCredentialsByKeypair(keyPairName: string): Promise<Record<string, unknown>> {
    if (!keyPairName) throw new Error('workspace.getVMCredentialsByKeypair: keyPairName is required');
    return this.post('/api/v2/vault/get-single-vm-credentials', { key_pair_name: keyPairName });
  }

  // ── GitHub credentials ──
  storeGitHubCredentials(input: GitHubCredentialsInput): Promise<Record<string, unknown>> {
    if (!input.githubToken) throw new Error('workspace.storeGitHubCredentials: githubToken is required');
    const body: Record<string, unknown> = {
      GITHUB_TOKEN: input.githubToken,
      GITHUB_TOKEN_NAME: input.githubTokenName || 'default',
    };
    if (input.githubUser) body.GITHUB_USER = input.githubUser;
    if (input.sshPublicKey) body.SSH_PUBLIC_KEY = input.sshPublicKey;
    if (input.sshPrivateKey) body.SSH_PRIVATE_KEY = input.sshPrivateKey;
    return this.post('/api/v2/setup/github-credentials', body);
  }

  // ── internal ──
  private async post<T = Record<string, unknown>>(path: string, body: unknown): Promise<T & Record<string, unknown>> {
    const res = await this.t.postJSON<T>(path, body);
    return (res.body ?? {}) as T & Record<string, unknown>;
  }
}
