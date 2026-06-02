/**
 * Observability — backups, migrations, resource synchronization.
 *
 * Endpoints:
 *   POST /api/v2/tenant/backup/create
 *   GET  /api/v2/tenant/backup/list
 *   POST /api/v2/tenant/backup/restore
 *   POST /api/v2/tenant/migrations/plan
 *   POST /api/v2/tenant/migrations/execute
 *   POST /api/v2/tenant/resources/synchronize/batch
 */

import type { Transport } from './transport.js';

export interface Backup {
  id: string;
  name?: string;
  status?: string;
  sizeGb?: number;
  source?: string;
  createdAt?: string;
  raw: Record<string, unknown>;
}

export interface CreateBackupInput {
  resourceId: string;
  resourceType: 'database' | 'vm' | 'volume' | string;
  backupName: string;
}

export interface RestoreBackupInput {
  backupId: string;
  targetRegion?: string;
}

export class Backups {
  constructor(private t: Transport) {}

  async create(input: CreateBackupInput): Promise<Backup> {
    if (!input.resourceId || !input.resourceType) {
      throw new Error('backups.create: resourceId and resourceType are required');
    }
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/backup/create', {
      resource_id: input.resourceId,
      resource_type: input.resourceType,
      backup_name: input.backupName,
    });
    return wrapBackup(res.body);
  }

  async list(): Promise<Backup[]> {
    const res = await this.t.get<{ backups?: unknown[] }>('/api/v2/tenant/backup/list');
    const arr = (res.body as { backups?: unknown[] })?.backups ?? [];
    return arr.map(wrapBackup);
  }

  async restore(input: RestoreBackupInput): Promise<Record<string, unknown>> {
    if (!input.backupId) throw new Error('backups.restore: backupId is required');
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/backup/restore', {
      backup_id: input.backupId,
      target_region: input.targetRegion ?? '',
    });
    return res.body ?? {};
  }
}

export interface MigrationPlanInput {
  sourceProvider: string;
  targetProvider: string;
  resources: string[];
}

export interface MigrationPlan {
  sessionId: string;
  steps?: number;
  estimatedDowntimeMinutes?: number;
  raw: Record<string, unknown>;
}

export class Migrations {
  constructor(private t: Transport) {}

  async plan(input: MigrationPlanInput): Promise<MigrationPlan> {
    if (!input.sourceProvider || !input.targetProvider || !input.resources?.length) {
      throw new Error('migrations.plan: sourceProvider, targetProvider, and resources are required');
    }
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/migrations/plan', {
      source_provider: input.sourceProvider,
      target_provider: input.targetProvider,
      resources: input.resources,
    });
    const r = res.body ?? {};
    return {
      sessionId: (r.session_id as string) ?? '',
      steps: typeof r.steps === 'number' ? r.steps : undefined,
      estimatedDowntimeMinutes: typeof r.estimated_downtime_minutes === 'number' ? r.estimated_downtime_minutes : undefined,
      raw: r,
    };
  }

  async execute(sessionId: string, dryRun = false): Promise<Record<string, unknown>> {
    if (!sessionId) throw new Error('migrations.execute: sessionId is required');
    const res = await this.t.postJSON<Record<string, unknown>>('/api/v2/tenant/migrations/execute', {
      session_id: sessionId, dry_run: dryRun,
    });
    return res.body ?? {};
  }
}

export class SyncSub {
  constructor(private t: Transport) {}

  /** Discover and sync cloud resources into VxCloud state. */
  async batch(provider: string, services: string[]): Promise<Record<string, unknown>> {
    const res = await this.t.postJSON<Record<string, unknown>>(
      '/api/v2/tenant/resources/synchronize/batch',
      { provider, services },
    );
    return res.body ?? {};
  }
}

export class Observability {
  readonly backups: Backups;
  readonly migrations: Migrations;
  readonly sync: SyncSub;

  constructor(t: Transport) {
    this.backups = new Backups(t);
    this.migrations = new Migrations(t);
    this.sync = new SyncSub(t);
  }
}

function wrapBackup(input: unknown): Backup {
  const b = (typeof input === 'object' && input !== null) ? input as Record<string, unknown> : {};
  return {
    id: ((b.id ?? b.backup_id) as string) ?? '',
    name: ((b.name ?? b.backup_name) as string) ?? undefined,
    status: b.status as string | undefined,
    sizeGb: typeof b.size_gb === 'number' ? b.size_gb : undefined,
    source: b.source as string | undefined,
    createdAt: b.created_at as string | undefined,
    raw: b,
  };
}
