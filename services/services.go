// Package services is the resource module for managing already-running
// services on a remote VM. Sibling to install (which CREATES) and
// deploy (which DEPLOYS).
//
// Wraps three distinct server endpoints:
//
//   - Container lifecycle: POST /api/v2/tenant/container/{start,stop,remove}
//     (JSON body)
//   - Container status:    POST /api/v2/tenant/docker/container/status
//     (JSON body)
//   - Host actions:        POST /api/v2/tenant/services/action
//     (multipart form, whitelisted action keys)
//
// Maps 1:1 to the `vxcli services` command surface.
package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// SSH is the common SSH target. Same shape as install.SSH / deploy.SSH —
// every method here accepts it. KeyPairName resolves against your
// workspace Vault on the tenant node.
type SSH struct {
	Host          string
	User          string
	KeyPairName   string
	WorkspaceUser string
	Organization  string
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

func (s SSH) validate() error {
	if s.Host == "" || s.User == "" || s.KeyPairName == "" {
		return errors.New("services: SSH.Host, SSH.User, and SSH.KeyPairName are required")
	}
	return nil
}

// Client is the entry point. Construct via the parent SDK client:
//
//	c.Services()
//
// Methods are concurrency-safe via the shared Transport.
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// VM exposes host-level operations under c.Services().VM().
func (c *Client) VM() *VMClient {
	return &VMClient{T: c.T, NodeURL: c.NodeURL, AuthedUsername: c.AuthedUsername}
}

// ─── Container lifecycle (JSON) ──────────────────────────────────────────

// LifecycleResult is the JSON envelope every /container/{action} endpoint
// returns. Most fields are optional; raw is preserved for debugging.
type LifecycleResult struct {
	Success     bool                   `json:"success,omitempty"`
	Message     string                 `json:"message,omitempty"`
	ContainerID string                 `json:"container_id,omitempty"`
	Removed     bool                   `json:"removed,omitempty"`
	Raw         map[string]interface{} `json:"-"`
}

// Start a stopped container by name on the target VM.
func (c *Client) Start(ctx context.Context, ssh SSH, containerName string) (*LifecycleResult, error) {
	return c.lifecycle(ctx, ssh, containerName, "start")
}

// Stop a running container by name on the target VM.
func (c *Client) Stop(ctx context.Context, ssh SSH, containerName string) (*LifecycleResult, error) {
	return c.lifecycle(ctx, ssh, containerName, "stop")
}

// Remove (stop + delete) a container by name on the target VM.
// Destructive — wrap in your own confirmation prompt.
func (c *Client) Remove(ctx context.Context, ssh SSH, containerName string) (*LifecycleResult, error) {
	return c.lifecycle(ctx, ssh, containerName, "remove")
}

// Restart is a convenience: Stop, then Start. The server has no native
// restart endpoint for containers; we chain.
func (c *Client) Restart(ctx context.Context, ssh SSH, containerName string) (*LifecycleResult, error) {
	if _, err := c.Stop(ctx, ssh, containerName); err != nil {
		return nil, fmt.Errorf("services.Restart: stop failed: %w", err)
	}
	return c.Start(ctx, ssh, containerName)
}

func (c *Client) lifecycle(ctx context.Context, ssh SSH, name, action string) (*LifecycleResult, error) {
	if err := ssh.validate(); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("services: container name is required")
	}
	if action != "start" && action != "stop" && action != "remove" {
		return nil, fmt.Errorf("services: unsupported lifecycle action %q", action)
	}

	body := ssh.toFields(c.AuthedUsername)
	body["container_name"] = name

	var out map[string]interface{}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/container/"+action)
	if err := c.T.JSON(ctx, "services."+action, "POST", url, body, &out); err != nil {
		return nil, err
	}
	r := &LifecycleResult{Raw: out}
	if v, ok := out["success"].(bool); ok {
		r.Success = v
	}
	if v, ok := out["message"].(string); ok {
		r.Message = v
	}
	if v, ok := out["container_id"].(string); ok {
		r.ContainerID = v
	}
	if v, ok := out["removed"].(bool); ok {
		r.Removed = v
	}
	return r, nil
}

// ─── Status (JSON) ───────────────────────────────────────────────────────

// ContainerSummary mirrors the docker ps summary the server returns.
type ContainerSummary struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
	Ports  string `json:"ports"`
}

// StatusResult is the response shape of /docker/container/status.
type StatusResult struct {
	Success    bool                   `json:"success"`
	Hostname   string                 `json:"hostname"`
	Total      int                    `json:"total"`
	Containers []ContainerSummary     `json:"containers"`
	Raw        map[string]interface{} `json:"-"`
}

// Status inspects a single container by name (or all containers if name == "").
// Endpoint: POST /api/v2/tenant/docker/container/status
func (c *Client) Status(ctx context.Context, ssh SSH, name string) (*StatusResult, error) {
	if err := ssh.validate(); err != nil {
		return nil, err
	}
	body := ssh.toFields(c.AuthedUsername)
	if name != "" {
		body["service_name"] = name
	}
	var raw map[string]interface{}
	url := transport.JoinURL(c.NodeURL, "/api/v2/tenant/docker/container/status")
	if err := c.T.JSON(ctx, "services.Status", "POST", url, body, &raw); err != nil {
		return nil, err
	}
	r := &StatusResult{Raw: raw}
	if v, ok := raw["success"].(bool); ok {
		r.Success = v
	}
	if v, ok := raw["hostname"].(string); ok {
		r.Hostname = v
	}
	if v, ok := raw["total"].(float64); ok {
		r.Total = int(v)
	}
	if arr, ok := raw["containers"].([]interface{}); ok {
		for _, item := range arr {
			if m, ok := item.(map[string]interface{}); ok {
				cs := ContainerSummary{}
				if v, ok := m["id"].(string); ok {
					cs.ID = v
				}
				if v, ok := m["name"].(string); ok {
					cs.Name = v
				}
				if v, ok := m["image"].(string); ok {
					cs.Image = v
				}
				if v, ok := m["status"].(string); ok {
					cs.Status = v
				}
				if v, ok := m["ports"].(string); ok {
					cs.Ports = v
				}
				r.Containers = append(r.Containers, cs)
			}
		}
	}
	return r, nil
}

// ─── List (multipart admin action) ───────────────────────────────────────

// List returns the docker ps -a output for the host. Wraps the
// list_docker_containers admin action.
func (c *Client) List(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return c.runAction(ctx, ssh, "list_docker_containers", "")
}

// Logs tails journalctl logs for a systemd unit (NOT for a docker
// container). The server template runs `journalctl -u <unit> -n 50`,
// falling back to /var/log/<unit> if the unit isn't registered.
func (c *Client) Logs(ctx context.Context, ssh SSH, unit string) (*ActionResult, error) {
	if unit == "" {
		return nil, errors.New("services.Logs: unit is required")
	}
	return c.runAction(ctx, ssh, "tail_logs", unit)
}

// ─── VM (host-level actions) ─────────────────────────────────────────────

// VMClient — host-level operations. Reachable via Client.VM().
type VMClient struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// Reboot the remote host (sudo reboot). Destructive.
func (vm *VMClient) Reboot(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "reboot", "")
}

// Shutdown the remote host (sudo shutdown -h +1). Destructive.
func (vm *VMClient) Shutdown(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "shutdown", "")
}

// DiskCleanup — apt autoremove + apt clean + journalctl vacuum.
func (vm *VMClient) DiskCleanup(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "disk_cleanup", "")
}

// DockerCleanup — docker system prune -af --volumes.
func (vm *VMClient) DockerCleanup(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "docker_cleanup", "")
}

// RestartDocker — sudo systemctl restart docker.
func (vm *VMClient) RestartDocker(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "restart_docker", "")
}

// Memory diagnostics — free -h plus /proc/meminfo head.
func (vm *VMClient) Memory(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "check_memory", "")
}

// Disk diagnostics — df -hT plus largest dirs.
func (vm *VMClient) Disk(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "check_disk_detailed", "")
}

// ListServices — running systemd services.
func (vm *VMClient) ListServices(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "list_running_services", "")
}

// ListContainers — docker ps -a (alias of Client.List for naming convenience).
func (vm *VMClient) ListContainers(ctx context.Context, ssh SSH) (*ActionResult, error) {
	return vm.runAction(ctx, ssh, "list_docker_containers", "")
}

// KillPort — kill the process bound to <port> on the remote host.
func (vm *VMClient) KillPort(ctx context.Context, ssh SSH, port string) (*ActionResult, error) {
	if port == "" {
		return nil, errors.New("services.VM.KillPort: port is required")
	}
	return vm.runAction(ctx, ssh, "kill_port", port)
}

// StopService — sudo systemctl stop <unit>.
func (vm *VMClient) StopService(ctx context.Context, ssh SSH, unit string) (*ActionResult, error) {
	if unit == "" {
		return nil, errors.New("services.VM.StopService: unit is required")
	}
	return vm.runAction(ctx, ssh, "stop_service", unit)
}

// ─── shared admin-action machinery ───────────────────────────────────────

// ActionResult is the JSON envelope every admin-action endpoint returns.
type ActionResult struct {
	Success bool                   `json:"success"`
	Action  string                 `json:"action"`
	Output  string                 `json:"output"`
	Message string                 `json:"message"`
	Error   string                 `json:"error,omitempty"`
	Raw     map[string]interface{} `json:"-"`
}

func (c *Client) runAction(ctx context.Context, ssh SSH, action, target string) (*ActionResult, error) {
	return doAction(ctx, c.T, c.NodeURL, c.AuthedUsername, ssh, action, target, "services."+action)
}
func (vm *VMClient) runAction(ctx context.Context, ssh SSH, action, target string) (*ActionResult, error) {
	return doAction(ctx, vm.T, vm.NodeURL, vm.AuthedUsername, ssh, action, target, "services.vm."+action)
}

func doAction(
	ctx context.Context,
	t *transport.Transport,
	nodeURL, authedUsername string,
	ssh SSH, action, target, op string,
) (*ActionResult, error) {
	if err := ssh.validate(); err != nil {
		return nil, err
	}
	if action == "" {
		return nil, errors.New("services: action is required")
	}
	fields := ssh.toFields(authedUsername)
	fields["action"] = action
	if target != "" {
		fields["target"] = target
	}
	url := transport.JoinURL(nodeURL, "/api/v2/tenant/services/action")
	var raw map[string]interface{}
	if err := t.Multipart(ctx, op, url, fields, nil, &raw); err != nil {
		return nil, err
	}
	r := &ActionResult{Raw: raw, Action: action}
	if v, ok := raw["success"].(bool); ok {
		r.Success = v
	}
	if v, ok := raw["output"].(string); ok {
		r.Output = v
	}
	if v, ok := raw["message"].(string); ok {
		r.Message = v
	}
	if v, ok := raw["error"].(string); ok {
		r.Error = v
	}
	return r, nil
}
