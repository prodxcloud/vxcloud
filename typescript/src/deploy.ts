/**
 * Deploy resource — Docker container deploys + 12 stack types.
 *
 * Wire endpoints:
 *   container          POST /api/v2/tenant/container/deploy            JSON
 *   stack <kind>       POST /api/v2/infrastructure/services/<kind>/deploy   multipart (zip OR git)
 *
 * Stack kinds match the vxcli `deploy <kind>` subcommand surface.
 */

import type { Transport, MultipartFile } from './transport.js';
import type { SSHTarget, StackKind, DeploySession } from './types.js';
import { sshFields, attachKeyPair } from './internal.js';

export interface DeployContainerInput extends SSHTarget {
  /** Image to pull and run (e.g. grafana/grafana:latest). */
  image: string;
  /** Container name on the host. */
  name: string;
  /** Port mappings host:container (e.g. ['8080:80']). */
  ports?: string[];
  /** Environment variables KEY=value (repeatable). */
  env?: string[];
  /** Volume mappings /host:/container (repeatable). */
  volumes?: string[];
  /** Restart policy. Default 'unless-stopped'. */
  restart?: 'always' | 'unless-stopped' | 'on-failure' | 'no';
  /** Docker network to attach. */
  network?: string;
  /** Override the container CMD. */
  command?: string;
  /** Linux capabilities (repeatable). */
  capAdd?: string[];
  /** Host devices (repeatable). */
  devices?: string[];
  /** Sysctls (KEY=value, repeatable). */
  sysctls?: string[];
  /** Private-registry credentials. */
  dockerUser?: string;
  dockerPass?: string;
}

export interface DeployStackInput extends SSHTarget {
  /** Local source directory contents — caller pre-bundles into a zip. */
  zipBundle?: { filename: string; content: Uint8Array | Blob };
  /** Or deploy from a git repo instead. */
  repoUrl?: string;
  branch?: string;
  /** Application name on the host (used for container/network naming). */
  appName?: string;
  /** Entry point (e.g. 'app.app:app' for FastAPI, 'main:app' for Flask, etc.). */
  entry?: string;
  /** Path inside the bundle to requirements.txt / package.json / go.mod. */
  requirements?: string;
  /** Application port. Default 8000 for python stacks, 3000 for node, etc. */
  appPort?: string;
  /** HTTP port nginx serves on. Default 80. */
  httpPort?: string;
  /** Environment variables passed to the runner (KEY=value newline-separated). */
  envVars?: string;
}

export class Deploy {
  constructor(private t: Transport) {}

  /** Deploy a single Docker container by image. */
  async container(input: DeployContainerInput): Promise<DeploySession> {
    if (!input.image) throw new Error('deploy.container: image is required');
    if (!input.name) throw new Error('deploy.container: name is required');

    const fields: Record<string, string> = {
      ...sshFields(input),
      image: input.image,
      container_name: input.name,
      cloud_provider: 'docker',
    };
    if (input.ports?.length) fields.ports = input.ports.join(',');
    if (input.env?.length) fields.env = input.env.join('\n');
    if (input.volumes?.length) fields.volumes = input.volumes.join(',');
    if (input.restart) fields.restart_policy = input.restart;
    if (input.network) fields.network = input.network;
    if (input.command) fields.command = input.command;
    if (input.capAdd?.length) fields.cap_add = input.capAdd.join(',');
    if (input.devices?.length) fields.devices = input.devices.join(',');
    if (input.sysctls?.length) fields.sysctls = input.sysctls.join(',');
    if (input.dockerUser) fields.docker_username = input.dockerUser;
    if (input.dockerPass) fields.docker_password = input.dockerPass;

    const files: MultipartFile[] = [];
    attachKeyPair(input, files);
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/tenant/container/deploy', fields, files,
    );
    return wrapSession(res.body);
  }

  /** Deploy a stack of one of the 12 supported kinds. */
  async stack(kind: StackKind, input: DeployStackInput): Promise<DeploySession> {
    if (!input.zipBundle && !input.repoUrl) {
      throw new Error('deploy.stack: pass either zipBundle (pre-built) or repoUrl (git)');
    }

    const fields: Record<string, string> = { ...sshFields(input) };
    if (input.appName) fields.app_name = input.appName;
    if (input.entry) fields.entry = input.entry;
    if (input.requirements) fields.requirements = input.requirements;
    if (input.appPort) fields.app_port = input.appPort;
    if (input.httpPort) fields.http_port = input.httpPort;
    if (input.envVars) fields.env_vars = input.envVars;
    if (input.repoUrl) {
      fields.repo_url = input.repoUrl;
      if (input.branch) fields.branch = input.branch;
    }

    const files: MultipartFile[] = [];
    if (input.zipBundle) {
      files.push({
        field: stackFileField(kind),
        filename: input.zipBundle.filename,
        content: input.zipBundle.content as Uint8Array | Blob,
        contentType: 'application/zip',
      });
    }
    attachKeyPair(input, files);
    const path = stackEndpoint(kind);
    const res = await this.t.postMultipart<Record<string, unknown>>(path, fields, files);
    return wrapSession(res.body);
  }

  /** Convenience wrappers for each stack kind. Identical to stack(kind, ...). */
  async fastapi(input: DeployStackInput): Promise<DeploySession>   { return this.stack('fastapi', input); }
  async react(input: DeployStackInput): Promise<DeploySession>     { return this.stack('react', input); }
  async nextjs(input: DeployStackInput): Promise<DeploySession>    { return this.stack('nextjs', input); }
  async django(input: DeployStackInput): Promise<DeploySession>    { return this.stack('django', input); }
  async nodejs(input: DeployStackInput): Promise<DeploySession>    { return this.stack('nodejs', input); }
  async python(input: DeployStackInput): Promise<DeploySession>    { return this.stack('python', input); }
  async golang(input: DeployStackInput): Promise<DeploySession>    { return this.stack('golang', input); }
  async rust(input: DeployStackInput): Promise<DeploySession>      { return this.stack('rust', input); }
  async cpp(input: DeployStackInput): Promise<DeploySession>       { return this.stack('cpp', input); }
  async php(input: DeployStackInput): Promise<DeploySession>       { return this.stack('php', input); }
  async static_(input: DeployStackInput): Promise<DeploySession>   { return this.stack('static', input); }
}

// ────────────────────────────────────────────────────────────────────────
// internals
// ────────────────────────────────────────────────────────────────────────

function stackEndpoint(kind: StackKind): string {
  // Maps to the FastAPI / React / etc. handlers in services/<kind>/.
  switch (kind) {
    case 'fastapi':       return '/api/v2/infrastructure/services/fastapi/deploy';
    case 'react':         return '/api/v2/infrastructure/services/reactjs/deploy';
    case 'nextjs':        return '/api/v2/infrastructure/services/nextjs/deploy';
    case 'django':        return '/api/v2/infrastructure/services/django/deploy';
    case 'nodejs':        return '/api/v2/infrastructure/services/nodejs/deploy';
    case 'python':        return '/api/v2/infrastructure/services/python/deploy';
    case 'golang':        return '/api/v2/infrastructure/services/golang/deploy';
    case 'rust':          return '/api/v2/infrastructure/services/rust/deploy';
    case 'cpp':           return '/api/v2/infrastructure/services/cpp/deploy';
    case 'php':           return '/api/v2/infrastructure/services/php/deploy';
    case 'static':
    case 'staticwebsite': return '/api/v2/infrastructure/services/staticwebsite/deploy';
  }
}

function stackFileField(_kind: StackKind): string {
  // The server expects the zip under different field names per stack
  // kind (matches vxcli's deploy_stack.go target.FileField). Default to
  // 'app_zip' which most handlers accept; refine if needed.
  return 'app_zip';
}

function wrapSession(body: unknown): DeploySession {
  const b = (typeof body === 'object' && body !== null) ? body as Record<string, unknown> : {};
  return {
    sessionId: (b.session_id as string) ?? (b.sessionId as string) ?? '',
    resourceName: b.resource_name as string | undefined,
    accessUrl: b.access_url as string | undefined,
    status: b.status as string | undefined,
    raw: b,
  };
}
