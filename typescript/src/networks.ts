/**
 * Networks resource — diagnostic scripts (DNS, bandwidth, port checks,
 * security audits). Mirrors the Go and Python SDKs.
 *
 * Local execution is the caller's job (the script is a shell script);
 * remote execution delegates to install.script under the hood.
 */

import type { Transport, MultipartFile } from './transport.js';
import type { SSHTarget } from './types.js';
import { sshFields, attachKeyPair } from './internal.js';

export interface ScriptCatalogEntry {
  name: string;
  fileName?: string;
  description?: string;
  aliases?: string[];
  raw: Record<string, unknown>;
}

export interface RunRemoteInput extends SSHTarget {
  /** Script bytes — caller fetches/embeds the script content. */
  script: string | Uint8Array;
  /** Filename as it appears on the remote host. */
  scriptName?: string;
  /** Args appended to the script invocation. */
  args?: string[];
}

export class Networks {
  constructor(private t: Transport) {}

  /** List the catalog of available diagnostic scripts. Soft-fails to []. */
  async list(): Promise<ScriptCatalogEntry[]> {
    try {
      const res = await this.t.get<{ scripts?: unknown[] }>('/api/v2/tenant/networks/scripts');
      const arr = (res.body as { scripts?: unknown[] })?.scripts ?? [];
      return arr.map((m) => {
        const b = (typeof m === 'object' && m !== null) ? m as Record<string, unknown> : {};
        return {
          name: (b.name as string) ?? '',
          fileName: b.file_name as string | undefined,
          description: b.description as string | undefined,
          aliases: b.aliases as string[] | undefined,
          raw: b,
        };
      });
    } catch {
      // Endpoint may not exist yet; vxcli embeds the catalog client-side.
      return [];
    }
  }

  /** Ship and run a diagnostic script on a remote VM via install.script. */
  async runRemote(input: RunRemoteInput): Promise<Record<string, unknown>> {
    if (!input.script) {
      throw new Error('networks.runRemote: script bytes are required');
    }
    const scriptName = input.scriptName ?? 'network-script.sh';
    const fields: Record<string, string> = {
      ...sshFields(input),
      mode: 'script',
      script_name: scriptName,
    };
    if (input.args?.length) fields.script_args = input.args.join('\x00');

    const scriptText = typeof input.script === 'string'
      ? input.script
      : new TextDecoder().decode(input.script);

    const files: MultipartFile[] = [{
      field: 'script_file',
      filename: scriptName,
      text: scriptText,
      contentType: 'application/x-sh',
    }];
    attachKeyPair(input, files);
    const res = await this.t.postMultipart<Record<string, unknown>>(
      '/api/v2/tenant/install/script', fields, files,
    );
    return res.body ?? {};
  }
}
