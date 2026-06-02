/**
 * Internal helpers for resource modules. Not exported from the package
 * top-level; only the modules in this directory consume them.
 */

import { readFileSync } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import type { SSHTarget } from './types.js';
import type { MultipartFile } from './transport.js';

/**
 * Build the SSH multipart-form fields the server expects on every
 * deploy/install/services endpoint:
 *   hostname, ssh_username, key_pair_name, username, organization
 * (the "username" field is the workspace owner used for Vault lookup,
 * NOT the SSH username.)
 */
export function sshFields(t: SSHTarget, authedUser?: string): Record<string, string> {
  if (!t.host) throw new Error('sshFields: host is required');
  if (!t.sshUser) throw new Error('sshFields: sshUser is required');
  if (!t.keyPairName && !t.keyPairLocation) {
    throw new Error('sshFields: keyPairName or keyPairLocation is required');
  }
  const user = t.workspaceUser?.trim() || authedUser || '';
  const org = t.organization?.trim() || user;
  return {
    hostname: t.host.trim(),
    ssh_username: t.sshUser.trim(),
    key_pair_name: (t.keyPairName ?? '').trim(),
    username: user,
    organization: org,
  };
}

/**
 * If `keyPairLocation` is set on the SSH target, read the PEM file from
 * disk and append it as a `private_key_pem` multipart part. Mirrors the
 * `--key-pair-location` flag in vxcli.
 *
 * Server-side support is in flight; sending the part is a no-op against
 * older servers, so this is forward-compatible.
 */
export function attachKeyPair(t: SSHTarget, files: MultipartFile[]): void {
  const loc = t.keyPairLocation?.trim();
  if (!loc) return;
  const expanded = expandHome(loc);
  const data = readFileSync(expanded);
  if (!data.includes(Buffer.from('-----BEGIN'))) {
    throw new Error(`keyPairLocation: ${expanded} does not look like a PEM file (no -----BEGIN marker)`);
  }
  files.push({
    field: 'private_key_pem',
    filename: path.basename(expanded),
    content: new Uint8Array(data),
    contentType: 'application/x-pem-file',
  });
}

function expandHome(p: string): string {
  if (p === '~' || p.startsWith('~/')) {
    return path.join(os.homedir(), p.slice(1));
  }
  return path.resolve(p);
}
