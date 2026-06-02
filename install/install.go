// Package install is the resource module for running install scripts and
// docker-compose stacks against a remote VM via the tenant node.
//
// The SDK does not bundle service install scripts (the way vxcli does).
// Two flows are supported:
//
//   - Script: caller supplies a shell script as bytes; the node SCPs and
//     executes it on the target VM. Suitable for any custom installer.
//   - Compose: caller supplies a docker-compose.yml; the node materializes
//     it on the target VM and runs `docker compose up -d`.
//
// Service-by-name (e.g. `vxcli install grafana`) is intentionally not
// exposed — that path requires the install scripts that ship inside the
// vxcli binary. Customers should either (a) supply the script themselves
// or (b) use docker-compose.
package install

import (
	"context"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// SSH is the common SSH target shared by every install flow.
type SSH struct {
	Host        string // target VM IP or DNS name (required)
	User        string // SSH login user (typically "ubuntu") (required)
	KeyPairName string // workspace vault key entry, e.g. "AWSPRODKEY1.PEM" (required)

	// WorkspaceUser, if set, overrides the workspace owner used for vault
	// path resolution (secret/workspaces/<org>/<user>/...). Useful when a
	// keypair lives under a different user's workspace.
	WorkspaceUser string
	// Organization, if set, overrides the org segment of the vault path.
	Organization string
}

func (s SSH) toFields(authedUsername string) map[string]string {
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
	}
}

// Result is the response shape every install / deploy endpoint returns.
type Result struct {
	SessionID     string `json:"session_id"`
	Status        string `json:"status,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	ExitCode      int    `json:"exit_code,omitempty"`
	ExecutionTime string `json:"execution_time,omitempty"`
	Script        string `json:"script,omitempty"`
	StackName     string `json:"stack_name,omitempty"`
	Stdout        string `json:"stdout,omitempty"`
	Stderr        string `json:"stderr,omitempty"`
}

// Client is the install resource module facade.
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string // user from auth state (used as vault default)
}

// ScriptOpts describes a custom-script install.
type ScriptOpts struct {
	SSH        SSH
	ScriptName string   // basename uploaded to remote /tmp; e.g. "bootstrap.sh"
	Script     []byte   // raw bytes of the shell script
	Args       []string // appended to the script invocation
	Env        []string // KEY=value pairs exported before the script runs
}

func (c *Client) validate(s SSH) error {
	if s.Host == "" || s.User == "" || s.KeyPairName == "" {
		return fmt.Errorf("install: SSH.Host, SSH.User, and SSH.KeyPairName are required")
	}
	return nil
}

// Script uploads a shell script and executes it on the target VM.
//
// The remote endpoint is POST /api/v2/tenant/install/script.
func (c *Client) Script(ctx context.Context, opts ScriptOpts) (*Result, error) {
	if err := c.validate(opts.SSH); err != nil {
		return nil, err
	}
	if len(opts.Script) == 0 {
		return nil, fmt.Errorf("install.Script: Script bytes are empty")
	}
	if opts.ScriptName == "" {
		opts.ScriptName = "install.sh"
	}

	fields := opts.SSH.toFields(c.AuthedUsername)
	fields["mode"] = "script"
	fields["script_name"] = opts.ScriptName
	if len(opts.Args) > 0 {
		// vxcli uses NUL as separator. Mirror that.
		fields["script_args"] = joinNUL(opts.Args)
	}
	if len(opts.Env) > 0 {
		fields["script_env"] = joinLines(opts.Env)
	}

	files := []transport.FilePart{
		{Field: "script_file", Filename: opts.ScriptName, Content: opts.Script, ContentType: "application/x-shellscript"},
	}

	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/install/script")
	var out Result
	if err := c.T.Multipart(ctx, "install.Script", url, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ComposeOpts describes a docker-compose-based install.
type ComposeOpts struct {
	SSH       SSH
	StackName string // unique workload name (lowercase, ^[a-z0-9][a-z0-9_-]{0,62}$)
	Compose   []byte // raw bytes of docker-compose.yml
	EnvFile   []byte // optional .env contents
	// RegistrySlug references a workspace docker-registry credential
	// stored in vault. Use this OR DockerUsername/DockerPassword.
	RegistrySlug   string
	DockerUsername string
	DockerPassword string
}

// Compose uploads a docker-compose.yml and brings it up on the target VM.
//
// The remote endpoint is POST /api/v2/tenant/provision/docker-compose/custom.
func (c *Client) Compose(ctx context.Context, opts ComposeOpts) (*Result, error) {
	if err := c.validate(opts.SSH); err != nil {
		return nil, err
	}
	if opts.StackName == "" {
		return nil, fmt.Errorf("install.Compose: StackName is required")
	}
	if len(opts.Compose) == 0 {
		return nil, fmt.Errorf("install.Compose: Compose bytes are empty")
	}

	fields := opts.SSH.toFields(c.AuthedUsername)
	fields["stack_name"] = opts.StackName
	fields["compose_content"] = string(opts.Compose)
	fields["cloud_provider"] = "docker"
	if len(opts.EnvFile) > 0 {
		fields["env_file_content"] = string(opts.EnvFile)
	}
	if opts.RegistrySlug != "" {
		fields["docker_registry_slug"] = opts.RegistrySlug
	}
	if opts.DockerUsername != "" {
		fields["docker_username"] = opts.DockerUsername
	}
	if opts.DockerPassword != "" {
		fields["docker_password"] = opts.DockerPassword
	}

	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/provision/docker-compose/custom")
	var out Result
	if err := c.T.Multipart(ctx, "install.Compose", url, fields, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// joinNUL joins strings with "\x00", matching vxcli's wire format.
func joinNUL(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "\x00"
		}
		out += v
	}
	return out
}

func joinLines(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "\n"
		}
		out += v
	}
	return out
}
