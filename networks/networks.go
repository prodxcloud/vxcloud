// Package networks is the resource module for vxcli's network-diagnostic
// scripts (DNS, bandwidth, port checks, security audits).
//
// Two execution modes:
//
//   - Local: caller supplies (or fetches) the script and runs it on the
//     user's host. SDK consumers do this themselves; this package only
//     surfaces the catalog.
//   - Remote: ships the script to a remote VM via the install.script
//     flow. The SDK delegates to /api/v2/tenant/install/script under
//     the hood, so all the same auth/SSH machinery applies.
//
// vxcli embeds the script catalog in its binary; the SDK does NOT.
// Callers either supply the script bytes or fetch the catalog from the
// /api/v2/tenant/networks/scripts endpoint (planned in BIG_PLAN M3).
package networks

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client is the entry point. Construct via the parent SDK client:
//
//	c.Networks()
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// ScriptCatalogEntry is one row in the embedded networks-script catalog.
// Returned by List once the server endpoint lands; today this is empty
// unless the operator has populated /api/v2/tenant/networks/scripts.
type ScriptCatalogEntry struct {
	Name        string   `json:"name"`
	FileName    string   `json:"file_name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases,omitempty"`
}

// List returns the catalog of available network-diagnostic scripts.
// Falls back to an empty list if the server doesn't yet expose the
// catalog endpoint (vxcli embeds it client-side today).
func (c *Client) List(ctx context.Context) ([]ScriptCatalogEntry, error) {
	var resp struct {
		Scripts []ScriptCatalogEntry `json:"scripts"`
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/networks/scripts")
	if err := c.T.JSON(ctx, "networks.List", "GET", url, nil, &resp); err != nil {
		// Endpoint may not exist yet — soft-fail.
		return nil, err
	}
	return resp.Scripts, nil
}

// SSH is the common SSH target shared with install/services/deploy.
type SSH struct {
	Host          string
	User          string
	KeyPairName   string
	WorkspaceUser string
	Organization  string
}

func (s SSH) toFields(authed string) map[string]string {
	user := s.WorkspaceUser
	if user == "" {
		user = authed
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
	}
}

// RunRemoteOpts ships an arbitrary network-diagnostic script to a remote
// VM via the install.script flow.
type RunRemoteOpts struct {
	SSH SSH

	// Script bytes (e.g. an embedded catalog entry from the caller).
	Script []byte
	// ScriptName as it appears on the remote host.
	ScriptName string
	// Args appended to the script invocation (NUL-separated server-side).
	Args []string
}

// RunRemote executes a network-diagnostic script on a remote VM.
//
// This is a thin wrapper over POST /api/v2/tenant/install/script — it's
// the same pipeline vxcli uses when --host is passed to `vxcli networks
// <script>`.
func (c *Client) RunRemote(ctx context.Context, opts RunRemoteOpts) (map[string]interface{}, error) {
	if opts.SSH.Host == "" || opts.SSH.User == "" || opts.SSH.KeyPairName == "" {
		return nil, errors.New("networks.RunRemote: SSH.Host, SSH.User, and SSH.KeyPairName are required")
	}
	if len(opts.Script) == 0 {
		return nil, errors.New("networks.RunRemote: Script bytes are required")
	}
	if opts.ScriptName == "" {
		opts.ScriptName = "network-script.sh"
	}

	fields := opts.SSH.toFields(c.AuthedUsername)
	fields["mode"] = "script"
	fields["script_name"] = opts.ScriptName
	if len(opts.Args) > 0 {
		// vxcli/install separator is NUL.
		args := opts.Args[0]
		for _, a := range opts.Args[1:] {
			args += "\x00" + a
		}
		fields["script_args"] = args
	}
	files := []transport.FilePart{
		{Field: "script_file", Filename: opts.ScriptName, Content: opts.Script},
	}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/install/script")
	var out map[string]interface{}
	if err := c.T.Multipart(ctx, "networks.RunRemote", url, fields, files, &out); err != nil {
		return nil, fmt.Errorf("networks.RunRemote: %w", err)
	}
	return out, nil
}
