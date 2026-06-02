// Package metaldb is the resource module for MetalDB — self-managed
// PostgreSQL provisioned over SSH onto a customer VM.
//
// It wraps the two node endpoints the web dashboard's Metal DB wizard
// calls:
//
//	POST /api/v2/tenant/provision/metaldb/test-connection   (multipart)
//	POST /api/v2/tenant/provision/metaldb                   (JSON)
//
// The SSH private key is never sent by the client — the node resolves it
// from the workspace vault by KeyPairName. The client only supplies which
// VM, how to log into it, and the Postgres user/password to create.
package metaldb

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

const (
	testPath      = "/api/v2/tenant/provision/metaldb/test-connection"
	provisionPath = "/api/v2/tenant/provision/metaldb"
)

// Result is a decoded JSON object response.
type Result = map[string]interface{}

// SSH identifies the target VM and how to log into it. The private key
// itself is resolved server-side from the workspace vault by KeyPairName.
type SSH struct {
	Host        string // target VM IP or DNS name (required)
	User        string // SSH login user, e.g. "ubuntu" or "root" (required)
	KeyPairName string // workspace vault key entry, e.g. "VPS1" (required)

	// WorkspaceUser overrides the workspace owner used for vault path
	// resolution. Defaults to the authenticated user.
	WorkspaceUser string
	// Organization overrides the org segment of the vault path.
	Organization string
}

// Client is the entry point. Construct via c.MetalDB().
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

func (s SSH) fields(authedUsername string) (map[string]string, error) {
	if s.Host == "" || s.User == "" || s.KeyPairName == "" {
		return nil, errors.New("metaldb: SSH.Host, SSH.User and SSH.KeyPairName are required")
	}
	user := s.WorkspaceUser
	if user == "" {
		user = authedUsername
	}
	org := s.Organization
	if org == "" {
		org = user
	}
	return map[string]string{
		"hostname":      s.Host,
		"ssh_username":  s.User,
		"key_pair_name": s.KeyPairName,
		"username":      user,
		"organization":  org,
	}, nil
}

// TestConnection pre-flights the SSH connection to the target VM — the
// equivalent of the wizard's "Test SSH Connection" button. HTTP is always
// 200; inspect the "success" field of the returned Result.
func (c *Client) TestConnection(ctx context.Context, ssh SSH) (Result, error) {
	fields, err := ssh.fields(c.AuthedUsername)
	if err != nil {
		return nil, err
	}
	var out Result
	u := transport.JoinURL(c.NodeURL, testPath)
	if err := c.T.Multipart(ctx, "metaldb.TestConnection", u, fields, nil, &out); err != nil {
		return nil, fmt.Errorf("metaldb.TestConnection: %w", err)
	}
	return out, nil
}

// ProvisionInput is the body for Provision. Use DefaultProvisionInput to
// start from the same defaults the web dashboard applies.
type ProvisionInput struct {
	SSH SSH

	// ResourceName labels the deployment; defaults to DatabaseName.
	ResourceName string
	// CloudProvider tag; defaults to "metaldb".
	CloudProvider string

	DatabaseName     string // default "postgres"
	DatabaseUser     string // default "postgres"
	DatabasePassword string // default "root"
	PostgresPassword string // default "root"
	Port             string // default "5432"
	PostgresVersion  string // default "16"

	EnableReplication bool
	ReplicaHostname   string
	MultiZone         bool
	BackupEnabled     bool // dashboard default: true (see DefaultProvisionInput)
	BackupRetention   int  // days; dashboard default: 7

	Tags map[string]string
}

// DefaultProvisionInput returns a ProvisionInput pre-filled with the same
// defaults the web dashboard's Metal DB wizard uses. Set SSH and any
// overrides, then pass it to Provision.
func DefaultProvisionInput() ProvisionInput {
	return ProvisionInput{
		CloudProvider:    "metaldb",
		DatabaseName:     "postgres",
		DatabaseUser:     "postgres",
		DatabasePassword: "root",
		PostgresPassword: "root",
		Port:             "5432",
		PostgresVersion:  "16",
		BackupEnabled:    true,
		BackupRetention:  7,
	}
}

// Provision installs PostgreSQL on the target VM and creates the requested
// database/user. This endpoint runs the install synchronously, so the
// returned Result already carries status / connection_string / outputs.
func (c *Client) Provision(ctx context.Context, in ProvisionInput) (Result, error) {
	ssh, err := in.SSH.fields(c.AuthedUsername)
	if err != nil {
		return nil, err
	}
	dbName := orDefault(in.DatabaseName, "postgres")
	body := map[string]interface{}{
		"resource_name":      orDefault(in.ResourceName, dbName),
		"resource_type":      "metaldb",
		"cloud_provider":     orDefault(in.CloudProvider, "metaldb"),
		"database_name":      dbName,
		"database_user":      orDefault(in.DatabaseUser, "postgres"),
		"database_password":  orDefault(in.DatabasePassword, "root"),
		"postgres_password":  orDefault(in.PostgresPassword, "root"),
		"port":               orDefault(in.Port, "5432"),
		"postgres_version":   orDefault(in.PostgresVersion, "16"),
		"enable_replication": in.EnableReplication,
		"multi_zone":         in.MultiZone,
		"backup_enabled":     in.BackupEnabled,
		"backup_retention":   in.BackupRetention,
	}
	for k, v := range ssh {
		body[k] = v
	}
	if in.ReplicaHostname != "" {
		body["replica_hostname"] = in.ReplicaHostname
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}
	var out Result
	u := transport.JoinURL(c.NodeURL, provisionPath)
	if err := c.T.JSON(ctx, "metaldb.Provision", "POST", u, body, &out); err != nil {
		return nil, fmt.Errorf("metaldb.Provision: %w", err)
	}
	return out, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
