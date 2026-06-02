// Package deploy is the resource module for deploying applications and
// containers onto VMs the tenant node can SSH into.
//
// Two surfaces:
//
//   - Container: drop a single Docker container onto a VM (POST
//     /api/v2/tenant/container/deploy). Equivalent to `vxcli deploy container`.
//   - Stack: clone a git repo (or upload a bundle), build it, and run it
//     fronted by nginx (POST /api/v2/infrastructure/services/<stack>/deploy).
//     Equivalent to `vxcli deploy <react|fastapi|golang|...>`.
//
// Cloud-side provisioning (EC2, S3, IAM) lives in package nodes/cloud
// (not in this preview).
package deploy

import (
	"context"
	"fmt"
	"strings"

	"github.com/prodxcloud/vxcloud/install"
	"github.com/prodxcloud/vxcloud/transport"
)

// SSH is re-exported from install/ since the SSH triple is shared.
type SSH = install.SSH

// Result mirrors install.Result. New fields may be added.
type Result struct {
	SessionID    string `json:"session_id"`
	Status       string `json:"status,omitempty"`
	Hostname     string `json:"hostname,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
	AccessURL    string `json:"access_url,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
}

// Stack identifies which language deployer the node should run.
type Stack string

const (
	StackReact   Stack = "react"
	StackNextJS  Stack = "nextjs"
	StackNodeJS  Stack = "nodejs"
	StackFastAPI Stack = "fastapi"
	StackPython  Stack = "python"
	StackDjango  Stack = "django"
	StackGolang  Stack = "golang"
	StackRust    Stack = "rust"
	StackCpp     Stack = "cpp"
	StackPHP     Stack = "php"
	StackStatic  Stack = "static"
)

// stackTarget describes one stack's wire contract. Each stack picked its
// own form field names for the git URL and branch — react/nextjs took
// repo_url/branch; everyone else took git_url/git_branch. The SDK
// normalizes this so callers always set RepoURL / Branch on StackOpts.
type stackTarget struct {
	Path        string
	GitField    string
	BranchField string
}

var stackTargets = map[Stack]stackTarget{
	StackReact:   {"/api/v2/infrastructure/services/reactjs/deploy", "repo_url", "branch"},
	StackNextJS:  {"/api/v2/infrastructure/services/nextjs/deploy", "repo_url", "branch"},
	StackNodeJS:  {"/api/v2/infrastructure/services/nodejs/deploy", "git_url", "git_branch"},
	StackFastAPI: {"/api/v2/infrastructure/services/fastapi/deploy", "git_url", "git_branch"},
	StackPython:  {"/api/v2/infrastructure/services/python/deploy", "git_url", "git_branch"},
	StackDjango:  {"/api/v2/infrastructure/services/django/deploy", "git_url", "git_branch"},
	StackGolang:  {"/api/v2/infrastructure/services/golang/deploy", "git_url", "git_branch"},
	StackRust:    {"/api/v2/infrastructure/services/rust/deploy", "git_url", "git_branch"},
	StackCpp:     {"/api/v2/infrastructure/services/cpp/deploy", "git_url", "git_branch"},
	StackPHP:     {"/api/v2/infrastructure/services/php/deploy", "git_url", "git_branch"},
	StackStatic:  {"/api/v2/infrastructure/services/staticwebsite/deploy", "git_url", "git_branch"},
}

// Client is the deploy resource module.
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// ContainerOpts describes a single-container deploy onto a VM.
type ContainerOpts struct {
	SSH SSH

	Name          string   // container name override (defaults to image basename)
	Image         string   // registry/image:tag (required)
	Ports         []string // "host:container" pairs, e.g. {"3000:3000"}
	Volumes       []string // "host_path:container_path" pairs
	Env           []string // KEY=VALUE pairs
	RestartPolicy string   // "unless-stopped" (default), "always", "no"
	Network       string   // optional docker network
	Command       string   // optional override of CMD
	CapAdd        []string
	Devices       []string
	Sysctls       []string

	// Private registry auth — set EITHER DockerRegistrySlug (workspace
	// vault entry name) OR DockerUsername/DockerPassword.
	DockerRegistrySlug string
	DockerUsername     string
	DockerPassword     string

	// HTTPS — when EnableSSL is set with a Domain, the deploy installs nginx +
	// a Let's Encrypt cert in front of the first published port once the
	// container is healthy. The Domain's A record must already point to the VM.
	EnableSSL bool
	Domain    string
	SSLEmail  string
}

// Container drops a single Docker container onto a VM.
//
// Endpoint: POST /api/v2/tenant/container/deploy.
func (c *Client) Container(ctx context.Context, opts ContainerOpts) (*Result, error) {
	if err := validateSSH(opts.SSH); err != nil {
		return nil, err
	}
	if opts.Image == "" {
		return nil, fmt.Errorf("deploy.Container: Image is required")
	}

	fields := sshFields(opts.SSH, c.AuthedUsername)
	fields["image"] = opts.Image
	if opts.Name != "" {
		fields["container_name"] = opts.Name
	}
	if opts.RestartPolicy == "" {
		opts.RestartPolicy = "unless-stopped"
	}
	fields["restart_policy"] = opts.RestartPolicy
	fields["cloud_provider"] = "docker"
	if len(opts.Ports) > 0 {
		fields["ports"] = strings.Join(opts.Ports, ",")
	}
	if len(opts.Volumes) > 0 {
		fields["volumes"] = strings.Join(opts.Volumes, ",")
	}
	if len(opts.Env) > 0 {
		fields["environment_vars"] = strings.Join(opts.Env, ",")
	}
	if len(opts.CapAdd) > 0 {
		fields["cap_add"] = strings.Join(opts.CapAdd, ",")
	}
	if len(opts.Devices) > 0 {
		fields["devices"] = strings.Join(opts.Devices, ",")
	}
	if len(opts.Sysctls) > 0 {
		fields["sysctls"] = strings.Join(opts.Sysctls, ",")
	}
	if opts.Network != "" {
		fields["network"] = opts.Network
	}
	if opts.Command != "" {
		fields["command"] = opts.Command
	}
	if opts.DockerRegistrySlug != "" {
		fields["docker_registry_slug"] = opts.DockerRegistrySlug
	}
	if opts.DockerUsername != "" {
		fields["docker_username"] = opts.DockerUsername
	}
	if opts.DockerPassword != "" {
		fields["docker_password"] = opts.DockerPassword
	}
	if opts.EnableSSL && opts.Domain != "" {
		fields["enable_ssl"] = "true"
		fields["domain"] = opts.Domain
		if opts.SSLEmail != "" {
			fields["ssl_email"] = opts.SSLEmail
		}
	}

	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/container/deploy")
	var out Result
	if err := c.T.Multipart(ctx, "deploy.Container", url, fields, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StackOpts describes a language-stack git-clone-and-build deploy.
type StackOpts struct {
	SSH SSH

	AppName string // app identifier (defaults to repo basename)
	RepoURL string // git URL (required for repo-based deploys)
	Branch  string // default "main"

	// Git auth for private repos. GitToken accepts the literal "vault:"
	// to resolve from workspace vault — same convention as vxcli.
	GitProvider string // "github" | "gitlab" | "bitbucket"
	GitUsername string
	GitToken    string

	// Stack-specific knobs. Empty values fall back to platform defaults.
	BuildMode    string // "production" | "development" (react/nextjs)
	Entry        string // entry script / module:app
	Requirements string // path to requirements.txt (python/fastapi)
	Framework    string // express, fastify, etc. (nodejs)
	GoVersion    string // golang
	NodeVersion  string // ignored — server rejected as of preview
	HTTPPort     string // nginx-side port (default "80")
	HTTPSPort    string // default "443"
	AppPort      string // app-side port

	EnvVars string // newline-separated KEY=value

	// HTTPS / Traefik — when EnableSSL is set with a Domain, the service is
	// fronted at https://<Domain> by the shared Traefik proxy. ExposeDirectPort
	// also keeps the raw ip:port published (https + ip:port both reachable).
	EnableSSL        bool
	Domain           string
	SSLEmail         string
	ExposeDirectPort bool
}

// Stack runs a language-stack deploy against a remote VM. The available
// stacks are enumerated by the Stack constants above. Note: as of preview,
// nodejs/python/nextjs/static stack scripts have a server-side template
// substitution bug — vxsdk faithfully forwards the call, but those four
// will fail with exit status 1 until the platform fixes it.
func (c *Client) Stack(ctx context.Context, kind Stack, opts StackOpts) (*Result, error) {
	target, ok := stackTargets[kind]
	if !ok {
		return nil, fmt.Errorf("deploy.Stack: unknown stack %q", kind)
	}
	if err := validateSSH(opts.SSH); err != nil {
		return nil, err
	}
	if opts.RepoURL == "" {
		return nil, fmt.Errorf("deploy.Stack: RepoURL is required (local-bundle mode is not yet implemented)")
	}
	if opts.Branch == "" {
		opts.Branch = "main"
	}

	fields := sshFields(opts.SSH, c.AuthedUsername)
	fields[target.GitField] = opts.RepoURL
	fields[target.BranchField] = opts.Branch
	if opts.AppName != "" {
		fields["app_name"] = opts.AppName
	}
	if opts.GitProvider != "" {
		fields["git_provider"] = opts.GitProvider
	}
	if opts.GitUsername != "" {
		fields["git_username"] = opts.GitUsername
	}
	if opts.GitToken != "" {
		fields["git_token"] = opts.GitToken
	}
	if opts.BuildMode != "" {
		fields["build_mode"] = opts.BuildMode
	}
	if opts.Entry != "" {
		fields["entry"] = opts.Entry
	}
	if opts.Requirements != "" {
		fields["requirements"] = opts.Requirements
	}
	if opts.Framework != "" {
		fields["framework"] = opts.Framework
	}
	if opts.GoVersion != "" {
		fields["go_version"] = opts.GoVersion
	}
	if opts.HTTPPort != "" {
		fields["http_port"] = opts.HTTPPort
	}
	if opts.HTTPSPort != "" {
		fields["https_port"] = opts.HTTPSPort
	}
	if opts.AppPort != "" {
		fields["app_port"] = opts.AppPort
	}
	if opts.EnvVars != "" {
		fields["env_vars"] = opts.EnvVars
	}
	if opts.EnableSSL && opts.Domain != "" {
		fields["enable_ssl"] = "true"
		fields["domain"] = opts.Domain
		if opts.SSLEmail != "" {
			fields["ssl_email"] = opts.SSLEmail
		}
		if opts.ExposeDirectPort {
			fields["expose_direct_port"] = "true"
		}
	}

	url := transport.JoinURL(c.NodeURL, target.Path)
	var out Result
	if err := c.T.Multipart(ctx, "deploy.Stack."+string(kind), url, fields, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func validateSSH(s SSH) error {
	if s.Host == "" || s.User == "" || s.KeyPairName == "" {
		return fmt.Errorf("deploy: SSH.Host, SSH.User, and SSH.KeyPairName are required")
	}
	return nil
}

func sshFields(s SSH, authed string) map[string]string {
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
