/**
 * Install resource — `vxcli install <tech>`, `--script`, `--compose`.
 * Backed by the multipart endpoints under /api/v2/tenant/install/*
 * and /api/v2/tenant/provision/docker-compose/custom.
 */

import type { Transport, MultipartFile } from './transport.js';
import type { SSHTarget } from './types.js';
import { sshFields, attachKeyPair } from './internal.js';

export interface InstallScriptInput extends SSHTarget {
  /** Path to a local shell script. */
  scriptPath?: string;
  /** Or pass the script body directly. */
  scriptContent?: string;
  /** Filename the remote installer sees (optional). */
  scriptName?: string;
  /** Extra positional args appended to the script. */
  args?: string[];
  /** Extra environment variables (KEY=value). */
  env?: string[];
}

export interface InstallComposeInput extends SSHTarget {
  /** Local docker-compose.yml path. */
  composePath?: string;
  /** Or pass the compose YAML directly. */
  composeContent: string;
  /** Stack name (lowercase / digits / _ / -, max 63 chars). */
  stack: string;
  /** Optional .env file content shipped alongside. */
  envFileContent?: string;
  /** Workspace docker-registry slug for private images. */
  dockerRegistrySlug?: string;
  /** Or registry creds inline. */
  dockerUser?: string;
  dockerPass?: string;
}

export class Install {
  constructor(private t: Transport) {}

  /** Ship and run a custom shell installer. */
  async script(input: InstallScriptInput): Promise<Record<string, unknown>> {
    const scriptName = input.scriptName ?? 'install.sh';
    const fields: Record<string, string> = {
      ...sshFields(input),
      mode: 'script',
      script_name: scriptName,
    };
    if (input.args?.length) fields.script_args = input.args.join('\x00');
    if (input.env?.length) fields.script_env = input.env.join('\n');

    const files: MultipartFile[] = [];
    if (input.scriptContent) {
      files.push({
        field: 'script_file',
        filename: scriptName,
        text: input.scriptContent,
        contentType: 'application/x-sh',
      });
    } else if (input.scriptPath) {
      // Reading from disk is the caller's job; we accept content directly
      // for portability across Node + browser.
      throw new Error('install.script: pass scriptContent (read the file in your code) — direct file paths are not auto-resolved.');
    } else {
      throw new Error('install.script: scriptContent (or scriptPath that you read) is required');
    }

    attachKeyPair(input, files);
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/tenant/install/script', fields, files,
    );
    return res.body ?? {};
  }

  /** Apply a docker-compose.yml as a named stack. */
  async compose(input: InstallComposeInput): Promise<Record<string, unknown>> {
    const fields: Record<string, string> = {
      ...sshFields(input),
      stack_name: input.stack,
      compose_content: input.composeContent,
      cloud_provider: 'docker',
    };
    if (input.envFileContent) fields.env_file_content = input.envFileContent;
    if (input.dockerRegistrySlug) fields.docker_registry_slug = input.dockerRegistrySlug;
    if (input.dockerUser) fields.docker_username = input.dockerUser;
    if (input.dockerPass) fields.docker_password = input.dockerPass;

    const files: MultipartFile[] = [];
    attachKeyPair(input, files);
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/tenant/provision/docker-compose/custom', fields, files,
    );
    return res.body ?? {};
  }
}
