/**
 * Shared types for the vxcloud SDK.
 *
 * These mirror the JSON shapes the Go server already accepts/returns.
 * They are intentionally permissive (most fields optional) so the SDK
 * keeps working when the server adds new fields — strict typing happens
 * post-OpenAPI (see BIG_PLAN.md M4).
 */

/** A target VM reachable over SSH. Used by deploy / install / services. */
export interface SSHTarget {
  /** Hostname or IP of the target VM. Required. */
  host: string;
  /** SSH username on the target VM. Required. */
  sshUser: string;
  /**
   * Vault key-pair name. The tenant node resolves this against your
   * workspace Vault. Either this OR `keyPairLocation` must be set.
   */
  keyPairName?: string;
  /**
   * Path to a local PEM file. Read locally and attached as the
   * `private_key_pem` multipart part. Either this OR `keyPairName`
   * must be set.
   */
  keyPairLocation?: string;
  /**
   * Override the workspace owner used for Vault lookup. Defaults to
   * the authenticated user.
   */
  workspaceUser?: string;
  /**
   * Organization for workspace isolation. Defaults to `workspaceUser`,
   * then to the authenticated user.
   */
  organization?: string;
}

/** Common deploy-session response envelope. */
export interface DeploySession {
  sessionId: string;
  resourceName?: string;
  accessUrl?: string;
  status?: string;
  raw: Record<string, unknown>;
}

/** Container summary returned by `services.list` / VM list-containers. */
export interface ContainerSummary {
  id: string;
  name: string;
  image: string;
  status: string;
  ports: string;
}

/** Container status response from `services.status(name)`. */
export interface ContainerStatus {
  success: boolean;
  hostname?: string;
  total?: number;
  containers?: ContainerSummary[];
  raw: Record<string, unknown>;
}

/** Generic action response from `services` and `services.vm.*`. */
export interface ActionResponse {
  success: boolean;
  output?: string;
  message?: string;
  raw: Record<string, unknown>;
}

/** Pipeline (CI/CD). */
export interface Pipeline {
  id: string;
  name?: string;
  repository?: string;
  branch?: string;
  status?: string;
  raw: Record<string, unknown>;
}

/** Build (CI/CD). */
export interface Build {
  id: string;
  pipelineId?: string;
  status?: string;
  startedAt?: string;
  finishedAt?: string;
  raw: Record<string, unknown>;
}

/** Tenant node. */
export interface NodeInfo {
  id: string | number;
  name: string;
  url?: string;
  isDefault?: boolean;
  raw: Record<string, unknown>;
}

/** Marketplace catalog item (agent / model / solution). */
export interface MarketplaceItem {
  id: string;
  name?: string;
  category?: string;
  description?: string;
  version?: string;
  raw: Record<string, unknown>;
}

/**
 * The 12 deploy-stack kinds. Same keys vxcli uses on `vxcli deploy <kind>`.
 */
export type StackKind =
  | 'fastapi'
  | 'react'
  | 'nextjs'
  | 'django'
  | 'nodejs'
  | 'python'
  | 'golang'
  | 'rust'
  | 'cpp'
  | 'php'
  | 'static'
  | 'staticwebsite';

/** Whitelisted host actions accepted by `/api/v2/tenant/services/action`. */
export type HostAction =
  | 'disk_cleanup'
  | 'docker_cleanup'
  | 'shutdown'
  | 'reboot'
  | 'restart_docker'
  | 'list_running_services'
  | 'list_docker_containers'
  | 'check_memory'
  | 'check_disk_detailed'
  | 'kill_port'
  | 'stop_service'
  | 'remove_file'
  | 'tail_logs';
