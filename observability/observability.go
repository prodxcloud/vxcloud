// Package observability is the resource module for backups, migrations,
// and resource synchronization.
//
// Backups:    POST /api/v2/tenant/backup/create
//
//	GET  /api/v2/tenant/backup/list
//	POST /api/v2/tenant/backup/restore
//
// Migrations: POST /api/v2/tenant/migrations/plan
//
//	POST /api/v2/tenant/migrations/execute
//
// Synchronize:POST /api/v2/tenant/resources/synchronize/batch
package observability

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client is the entry point. Construct via c.Observability().
type Client struct {
	T       *transport.Transport
	NodeURL string
}

// Backups returns the backups sub-module.
func (c *Client) Backups() *Backups { return &Backups{T: c.T, NodeURL: c.NodeURL} }

// Migrations returns the migrations sub-module.
func (c *Client) Migrations() *Migrations { return &Migrations{T: c.T, NodeURL: c.NodeURL} }

// Sync returns the resource-synchronization sub-module.
func (c *Client) Sync() *Sync { return &Sync{T: c.T, NodeURL: c.NodeURL} }

// ─── Backups ─────────────────────────────────────────────────────────

type Backups struct {
	T       *transport.Transport
	NodeURL string
}

type CreateBackupInput struct {
	ResourceID   string `json:"resource_id"`
	ResourceType string `json:"resource_type"` // database | vm | volume | …
	BackupName   string `json:"backup_name"`
}

type Backup struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Status    string                 `json:"status"`
	SizeGB    float64                `json:"size_gb,omitempty"`
	Source    string                 `json:"source,omitempty"`
	CreatedAt string                 `json:"created_at,omitempty"`
	Raw       map[string]interface{} `json:"-"`
}

// Create a snapshot backup of an infrastructure resource.
func (b *Backups) Create(ctx context.Context, in CreateBackupInput) (*Backup, error) {
	if in.ResourceID == "" || in.ResourceType == "" {
		return nil, errors.New("backups.Create: ResourceID and ResourceType are required")
	}
	url := transport.JoinURL(b.NodeURL, "/api/v2/tenant/backup/create")
	var raw map[string]interface{}
	if err := b.T.JSON(ctx, "backups.Create", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("backups.Create: %w", err)
	}
	return wrapBackup(raw), nil
}

// List all backups for the current tenant.
func (b *Backups) List(ctx context.Context) ([]Backup, error) {
	var resp struct {
		Backups []map[string]interface{} `json:"backups"`
	}
	url := transport.JoinURL(b.NodeURL, "/api/v2/tenant/backup/list")
	if err := b.T.JSON(ctx, "backups.List", "GET", url, nil, &resp); err != nil {
		return nil, fmt.Errorf("backups.List: %w", err)
	}
	out := make([]Backup, 0, len(resp.Backups))
	for _, m := range resp.Backups {
		out = append(out, *wrapBackup(m))
	}
	return out, nil
}

type RestoreBackupInput struct {
	BackupID     string `json:"backup_id"`
	TargetRegion string `json:"target_region,omitempty"`
}

type RestoreResult struct {
	Restored      bool                   `json:"restored"`
	NewResourceID string                 `json:"new_resource_id,omitempty"`
	Raw           map[string]interface{} `json:"-"`
}

// Restore a resource from an existing backup snapshot.
func (b *Backups) Restore(ctx context.Context, in RestoreBackupInput) (*RestoreResult, error) {
	if in.BackupID == "" {
		return nil, errors.New("backups.Restore: BackupID is required")
	}
	url := transport.JoinURL(b.NodeURL, "/api/v2/tenant/backup/restore")
	var raw map[string]interface{}
	if err := b.T.JSON(ctx, "backups.Restore", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("backups.Restore: %w", err)
	}
	r := &RestoreResult{Raw: raw}
	if v, ok := raw["restored"].(bool); ok {
		r.Restored = v
	}
	if v, ok := raw["new_resource_id"].(string); ok {
		r.NewResourceID = v
	}
	return r, nil
}

func wrapBackup(m map[string]interface{}) *Backup {
	b := &Backup{Raw: m}
	if v, ok := m["id"].(string); ok {
		b.ID = v
	}
	if v, ok := m["backup_id"].(string); ok && b.ID == "" {
		b.ID = v
	}
	if v, ok := m["name"].(string); ok {
		b.Name = v
	}
	if v, ok := m["backup_name"].(string); ok && b.Name == "" {
		b.Name = v
	}
	if v, ok := m["status"].(string); ok {
		b.Status = v
	}
	if v, ok := m["size_gb"].(float64); ok {
		b.SizeGB = v
	}
	if v, ok := m["source"].(string); ok {
		b.Source = v
	}
	if v, ok := m["created_at"].(string); ok {
		b.CreatedAt = v
	}
	return b
}

// ─── Migrations ──────────────────────────────────────────────────────

type Migrations struct {
	T       *transport.Transport
	NodeURL string
}

type PlanMigrationInput struct {
	SourceProvider string   `json:"source_provider"`
	TargetProvider string   `json:"target_provider"`
	Resources      []string `json:"resources"`
}

type MigrationPlan struct {
	SessionID                string                 `json:"session_id"`
	Steps                    int                    `json:"steps"`
	EstimatedDowntimeMinutes int                    `json:"estimated_downtime_minutes,omitempty"`
	Raw                      map[string]interface{} `json:"-"`
}

// Plan a migration of resources between providers/regions.
func (m *Migrations) Plan(ctx context.Context, in PlanMigrationInput) (*MigrationPlan, error) {
	if in.SourceProvider == "" || in.TargetProvider == "" || len(in.Resources) == 0 {
		return nil, errors.New("migrations.Plan: SourceProvider, TargetProvider and Resources are required")
	}
	url := transport.JoinURL(m.NodeURL, "/api/v2/tenant/migrations/plan")
	var raw map[string]interface{}
	if err := m.T.JSON(ctx, "migrations.Plan", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("migrations.Plan: %w", err)
	}
	p := &MigrationPlan{Raw: raw}
	if v, ok := raw["session_id"].(string); ok {
		p.SessionID = v
	}
	if v, ok := raw["steps"].(float64); ok {
		p.Steps = int(v)
	}
	if v, ok := raw["estimated_downtime_minutes"].(float64); ok {
		p.EstimatedDowntimeMinutes = int(v)
	}
	return p, nil
}

type ExecuteMigrationInput struct {
	SessionID string `json:"session_id"`
	DryRun    bool   `json:"dry_run"`
}

// Execute a previously-planned migration.
func (m *Migrations) Execute(ctx context.Context, in ExecuteMigrationInput) (map[string]interface{}, error) {
	if in.SessionID == "" {
		return nil, errors.New("migrations.Execute: SessionID is required")
	}
	url := transport.JoinURL(m.NodeURL, "/api/v2/tenant/migrations/execute")
	var raw map[string]interface{}
	if err := m.T.JSON(ctx, "migrations.Execute", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("migrations.Execute: %w", err)
	}
	return raw, nil
}

// ─── Sync (resource discovery) ───────────────────────────────────────

type Sync struct {
	T       *transport.Transport
	NodeURL string
}

type BatchSyncInput struct {
	Provider string   `json:"provider"`
	Services []string `json:"services"`
}

// Batch — discover and sync cloud resources into VxCloud state.
func (s *Sync) Batch(ctx context.Context, in BatchSyncInput) (map[string]interface{}, error) {
	url := transport.JoinURL(s.NodeURL, "/api/v2/tenant/resources/synchronize/batch")
	var raw map[string]interface{}
	if err := s.T.JSON(ctx, "sync.Batch", "POST", url, in, &raw); err != nil {
		return nil, fmt.Errorf("sync.Batch: %w", err)
	}
	return raw, nil
}
