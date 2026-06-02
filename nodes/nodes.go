// Package nodes is the resource module for tenant-node management.
//
// Tenant nodes are per-customer provisioning environments — each customer
// has at least one, and SDK calls target one specific node at a time.
//
// All endpoints in this module live on the Infinity control plane
// (api.vxcloud.io), not on a tenant node, because the node registry is
// a control-plane concern.
package nodes

import (
	"context"
	"strconv"
	"strings"

	"github.com/prodxcloud/vxcloud/transport"
)

// Node mirrors infinity's NodeResponse pydantic model.
type Node struct {
	ID               int     `json:"id"`
	UserID           int     `json:"user_id,omitempty"`
	OrganizationID   string  `json:"organization_id,omitempty"`
	Hostname         string  `json:"hostname"`
	Status           string  `json:"status"`
	IsDefaultNode    bool    `json:"is_default_node"`
	PublicIP         string  `json:"public_ip,omitempty"`
	PrivateIP        string  `json:"private_ip,omitempty"`
	InstanceID       string  `json:"instance_id,omitempty"`
	LoadBalancer     string  `json:"load_balancer,omitempty"`
	CustomDomainName string  `json:"custom_domain_name,omitempty"`
	OSType           string  `json:"os_type,omitempty"`
	BoxType          string  `json:"box_type,omitempty"`
	StorageGB        float64 `json:"storage_gb,omitempty"`
	Category         string  `json:"category,omitempty"`
	License          string  `json:"license,omitempty"`
	SessionID        string  `json:"session_id,omitempty"`
	StatePath        string  `json:"state_path,omitempty"`
	CreatedAt        string  `json:"created_at,omitempty"`
	UpdatedAt        string  `json:"updated_at,omitempty"`
}

// BaseURL returns the resolved https URL for the node, with the same
// priority order vxcli and the frontend use:
// custom_domain_name → load_balancer → public_ip.
//
// Returns "" if the node has no addressable hostname yet.
func (n Node) BaseURL() string {
	raw := strings.TrimSpace(n.CustomDomainName)
	if raw == "" {
		raw = strings.TrimSpace(n.LoadBalancer)
	}
	if raw == "" {
		raw = strings.TrimSpace(n.PublicIP)
	}
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}

// Label is a short user-facing identifier.
func (n Node) Label() string {
	switch {
	case n.CustomDomainName != "":
		return n.CustomDomainName
	case n.Hostname != "":
		return n.Hostname
	case n.LoadBalancer != "":
		return n.LoadBalancer
	default:
		return "node-" + strconv.Itoa(n.ID)
	}
}

// Client is the nodes resource module facade. Constructed by the parent
// vxsdk.Client; callers obtain it from c.Nodes().
type Client struct {
	T           *transport.Transport
	InfinityURL string
}

// List returns all tenant nodes registered to the authenticated principal.
//
// Endpoint: GET {InfinityURL}/api/v1/auth/nodes/.
//
// Note: vxcli's `node list` command returned 401 for some tokens during
// preview testing. The SDK's auto-refresh-on-401 handles that case
// transparently when an API key is configured on the Client.
func (c *Client) List(ctx context.Context) ([]Node, error) {
	u := transport.JoinURL(c.InfinityURL, "/api/v1/auth/nodes/")
	var out []Node
	if err := c.T.JSON(ctx, "nodes.List", "GET", u, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SetDefault marks the given node ID as the user's default. Subsequent
// API key exchanges will resolve this node as the tenant target.
//
// Endpoint: POST {InfinityURL}/api/v1/auth/nodes/{id}/set-default.
func (c *Client) SetDefault(ctx context.Context, id int) error {
	u := transport.JoinURL(c.InfinityURL, "/api/v1/auth/nodes/"+strconv.Itoa(id)+"/set-default")
	return c.T.JSON(ctx, "nodes.SetDefault", "POST", u, struct{}{}, nil)
}

// Default returns the node currently marked as the workspace default,
// or nil if none exists.
func (c *Client) Default(ctx context.Context) (*Node, error) {
	all, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].IsDefaultNode {
			return &all[i], nil
		}
	}
	return nil, nil
}

// RegisterSelfHostedInput mirrors SelfHostedNodeRegisterRequest in
// app/auth/node.py:772 — the dashboard's "Self-Hosted" form payload.
type RegisterSelfHostedInput struct {
	Hostname              string `json:"hostname"`
	CustomDomainName      string `json:"custom_domain_name"`
	Port                  int    `json:"port,omitempty"`
	PublicIP              string `json:"public_ip,omitempty"`
	PrivateIP             string `json:"private_ip,omitempty"`
	TunnelProvider        string `json:"tunnel_provider,omitempty"`
	KeyPairName           string `json:"key_pair_name,omitempty"`
	IDEConnectionToken    string `json:"ide_connection_token,omitempty"`
	Agent1ConnectionToken string `json:"agent1_connection_token,omitempty"`
	StorageType           string `json:"storage_type,omitempty"`
	StorageBackupMode     string `json:"storage_backup_mode,omitempty"`
	Description           string `json:"description,omitempty"`
	SSHUsername           string `json:"ssh_username,omitempty"`
}

// RegisterSelfHosted registers a vxnode container the caller is running
// themselves (BYO hardware). No VM is provisioned on our side — the row
// is created with cloud_provider=self-hosted, status=active.
//
// Endpoint: POST {InfinityURL}/api/v1/auth/nodes/self-hosted.
func (c *Client) RegisterSelfHosted(ctx context.Context, in RegisterSelfHostedInput) (*Node, error) {
	u := transport.JoinURL(c.InfinityURL, "/api/v1/auth/nodes/self-hosted")
	var out Node
	if err := c.T.JSON(ctx, "nodes.RegisterSelfHosted", "POST", u, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a node record. Only deletes the row — the caller is
// responsible for terminating any underlying VM first.
//
// Endpoint: DELETE {InfinityURL}/api/v1/auth/nodes/{id}.
func (c *Client) Delete(ctx context.Context, id int) error {
	u := transport.JoinURL(c.InfinityURL, "/api/v1/auth/nodes/"+strconv.Itoa(id))
	return c.T.JSON(ctx, "nodes.Delete", "DELETE", u, nil, nil)
}

// UpdateInput mirrors NodeUpdateRequest in app/auth/node.py:81. All fields
// are pointer/slice/map so the zero value can be distinguished from "not
// set" — only non-nil fields are sent in the PATCH body. Backend rejects
// any field not in its EDITABLE_FIELDS whitelist (public_ip is read-only).
type UpdateInput struct {
	Hostname              *string                  `json:"hostname,omitempty"`
	CustomDomainName      *string                  `json:"custom_domain_name,omitempty"`
	LoadBalancer          *string                  `json:"load_balancer,omitempty"`
	PrivateIP             *string                  `json:"private_ip,omitempty"`
	Status                *string                  `json:"status,omitempty"`
	IsDefaultNode         *bool                    `json:"is_default_node,omitempty"`
	ProviderComputeType   *string                  `json:"provider_compute_type,omitempty"`
	StorageType           *string                  `json:"storage_type,omitempty"`
	StorageBackupMode     *string                  `json:"storage_backup_mode,omitempty"`
	StorageBackupAddress  *string                  `json:"storage_backup_address,omitempty"`
	InstallationChecklist []map[string]interface{} `json:"installation_checklist,omitempty"`
	EnabledFeatures       []interface{}            `json:"enabled_features,omitempty"`
	VPNAccessDetails      map[string]interface{}   `json:"vpn_access_details,omitempty"`
	TunnelVM              map[string]interface{}   `json:"tunnel_vm,omitempty"`
}

// Update partial-updates an existing node record. Only fields set in `in`
// are sent. Endpoint: PATCH {InfinityURL}/api/v1/auth/nodes/{id}.
func (c *Client) Update(ctx context.Context, id int, in UpdateInput) (*Node, error) {
	u := transport.JoinURL(c.InfinityURL, "/api/v1/auth/nodes/"+strconv.Itoa(id))
	var out Node
	if err := c.T.JSON(ctx, "nodes.Update", "PATCH", u, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
