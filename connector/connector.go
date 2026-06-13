// Package connector is the resource module for SDK-direct cloud deploys.
//
// Unlike the cloud package — which provisions via server-side Terraform under
// /api/v2/tenant/provision/* — connector targets the tenant node's
// /api/v2/connector/* surface, which provisions by calling cloud-provider SDKs
// and REST APIs directly: no Terraform, no Bash, no state files. It covers:
//
//   - EC2 VM provision / studio deploy (SCP+SSH) / terminate
//   - S3 static-site deploy and bucket creation
//   - GCP Cloud Run container deploy (Cloud Run Admin REST v2)
//   - Route53 subdomain records
//   - ALB attach with target group
//
// Every request carries a username for the Vault-backed credential lookup the
// node performs (connectors.FetchCloudCredentials). When a request leaves
// Username empty the Client fills it from AuthedUsername.
package connector

import (
	"context"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client is the connector facade. Acquire it from the SDK with c.Connector().
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// ── Request types (JSON tags mirror services/connectors/connector_types.go) ──

// VMRequest provisions a single EC2 instance via the EC2 SDK.
type VMRequest struct {
	Username     string            `json:"username"`
	Organization string            `json:"organization,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	InstanceName string            `json:"instance_name"`
	InstanceType string            `json:"instance_type,omitempty"`
	Region       string            `json:"region,omitempty"`
	OS           string            `json:"os,omitempty"`
	VolumeSize   int32             `json:"volume_size,omitempty"`
	KeyPairName  string            `json:"key_pair_name,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// DeployToVMRequest provisions an EC2 instance then pushes a studio project to
// it over SFTP and optionally runs a deploy command.
type DeployToVMRequest struct {
	Username     string            `json:"username"`
	Organization string            `json:"organization,omitempty"`
	SessionID    string            `json:"session_id"`
	StoragePath  string            `json:"storage_path,omitempty"`
	InstanceName string            `json:"instance_name,omitempty"`
	InstanceType string            `json:"instance_type,omitempty"`
	Region       string            `json:"region,omitempty"`
	OS           string            `json:"os,omitempty"`
	VolumeSize   int32             `json:"volume_size,omitempty"`
	KeyPairName  string            `json:"key_pair_name,omitempty"`
	Subdomain    string            `json:"subdomain,omitempty"`
	DeployCmd    string            `json:"deploy_command,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
}

// StaticSiteRequest deploys a studio project to S3 as a static website.
type StaticSiteRequest struct {
	Username      string `json:"username"`
	Organization  string `json:"organization,omitempty"`
	SessionID     string `json:"session_id"`
	StoragePath   string `json:"storage_path,omitempty"`
	BucketName    string `json:"bucket_name,omitempty"`
	Region        string `json:"region,omitempty"`
	IndexDocument string `json:"index_document,omitempty"`
	ErrorDocument string `json:"error_document,omitempty"`
	Subdomain     string `json:"subdomain,omitempty"`
	CustomDomain  string `json:"custom_domain,omitempty"`
}

// BucketRequest creates a single S3 bucket.
type BucketRequest struct {
	Username     string `json:"username"`
	Organization string `json:"organization,omitempty"`
	BucketName   string `json:"bucket_name"`
	Region       string `json:"region,omitempty"`
	Public       bool   `json:"public,omitempty"`
}

// CloudRunRequest deploys a container image to Google Cloud Run.
type CloudRunRequest struct {
	Username     string            `json:"username"`
	Organization string            `json:"organization,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	ServiceName  string            `json:"service_name"`
	Region       string            `json:"region,omitempty"`
	Image        string            `json:"image"`
	Port         int32             `json:"port,omitempty"`
	EnvVars      map[string]string `json:"env_vars,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	CPU          string            `json:"cpu,omitempty"`
	MaxInstances int32             `json:"max_instances,omitempty"`
	Subdomain    string            `json:"subdomain,omitempty"`
	AllowPublic  bool              `json:"allow_public,omitempty"`
}

// SubdomainRequest creates a Route53 DNS record.
type SubdomainRequest struct {
	Username     string `json:"username"`
	Organization string `json:"organization,omitempty"`
	Subdomain    string `json:"subdomain"`
	BaseDomain   string `json:"base_domain,omitempty"`
	HostedZoneID string `json:"hosted_zone_id,omitempty"`
	TargetIP     string `json:"target_ip,omitempty"`
	TargetCNAME  string `json:"target_cname,omitempty"`
	RecordType   string `json:"record_type,omitempty"`
	TTL          int64  `json:"ttl,omitempty"`
}

// LBRequest attaches instances to an application load balancer.
type LBRequest struct {
	Username     string   `json:"username"`
	Organization string   `json:"organization,omitempty"`
	Name         string   `json:"name"`
	InstanceIDs  []string `json:"instance_ids"`
	Port         int64    `json:"port,omitempty"`
	HealthPath   string   `json:"health_path,omitempty"`
	Region       string   `json:"region,omitempty"`
	Subdomain    string   `json:"subdomain,omitempty"`
	VPCID        string   `json:"vpc_id,omitempty"`
	SubnetIDs    []string `json:"subnet_ids,omitempty"`
}

// ── Result types ────────────────────────────────────────────────────────────

// VMResult is returned after provisioning a VM.
type VMResult struct {
	InstanceID string `json:"instance_id"`
	PublicIP   string `json:"public_ip"`
	PrivateIP  string `json:"private_ip"`
	State      string `json:"state"`
	Region     string `json:"region"`
	Provider   string `json:"provider"`
}

// BucketResult is returned after creating a bucket.
type BucketResult struct {
	BucketName string `json:"bucket_name"`
	Region     string `json:"region"`
	ARN        string `json:"arn"`
	Provider   string `json:"provider"`
}

// DeployResult is returned for composite deploy operations (VM/S3).
type DeployResult struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	AccessURL  string `json:"access_url"`
	Provider   string `json:"provider"`
	Region     string `json:"region"`
	InstanceID string `json:"instance_id,omitempty"`
	PublicIP   string `json:"public_ip,omitempty"`
	BucketName string `json:"bucket_name,omitempty"`
	WebsiteURL string `json:"website_url,omitempty"`
	Subdomain  string `json:"subdomain,omitempty"`
	FQDN       string `json:"fqdn,omitempty"`
}

// SubdomainResult is returned after creating a DNS record.
type SubdomainResult struct {
	FQDN       string `json:"fqdn"`
	RecordType string `json:"record_type"`
	Target     string `json:"target"`
	TTL        int64  `json:"ttl"`
}

// LBResult is returned after creating/attaching a load balancer.
type LBResult struct {
	LoadBalancerARN string `json:"load_balancer_arn"`
	DNSName         string `json:"dns_name"`
	TargetGroupARN  string `json:"target_group_arn"`
	FQDN            string `json:"fqdn,omitempty"`
}

// CloudRunResult is returned after deploying to Cloud Run.
type CloudRunResult struct {
	ServiceName string `json:"service_name"`
	ServiceURL  string `json:"service_url"`
	Region      string `json:"region"`
	ProjectID   string `json:"project_id"`
	FQDN        string `json:"fqdn,omitempty"`
}

// ── Methods ─────────────────────────────────────────────────────────────────

// ProvisionVM launches a single EC2 instance (no file deploy).
// POST /api/v2/connector/vm/provision
func (c *Client) ProvisionVM(ctx context.Context, req VMRequest) (*VMResult, error) {
	req.Username = c.user(req.Username)
	var out VMResult
	if err := c.post(ctx, "connector.ProvisionVM", "/api/v2/connector/vm/provision", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployToVM provisions an EC2 instance and pushes a studio project to it.
// POST /api/v2/connector/vm/studio/deploy
func (c *Client) DeployToVM(ctx context.Context, req DeployToVMRequest) (*DeployResult, error) {
	req.Username = c.user(req.Username)
	var out DeployResult
	if err := c.post(ctx, "connector.DeployToVM", "/api/v2/connector/vm/studio/deploy", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TerminateVM terminates an EC2 instance.
// POST /api/v2/connector/vm/terminate
func (c *Client) TerminateVM(ctx context.Context, instanceID, organization string) error {
	body := map[string]string{
		"username":     c.user(""),
		"organization": organization,
		"instance_id":  instanceID,
	}
	var out map[string]any
	return c.post(ctx, "connector.TerminateVM", "/api/v2/connector/vm/terminate", body, &out)
}

// CreateBucket creates a single S3 bucket.
// POST /api/v2/connector/s3/bucket
func (c *Client) CreateBucket(ctx context.Context, req BucketRequest) (*BucketResult, error) {
	req.Username = c.user(req.Username)
	var out BucketResult
	if err := c.post(ctx, "connector.CreateBucket", "/api/v2/connector/s3/bucket", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployStaticSite deploys a studio project to S3 as a static website.
// POST /api/v2/connector/s3/studio/deploy
func (c *Client) DeployStaticSite(ctx context.Context, req StaticSiteRequest) (*DeployResult, error) {
	req.Username = c.user(req.Username)
	var out DeployResult
	if err := c.post(ctx, "connector.DeployStaticSite", "/api/v2/connector/s3/studio/deploy", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeployCloudRun deploys a container image to Google Cloud Run.
// POST /api/v2/connector/gcr/studio/deploy
func (c *Client) DeployCloudRun(ctx context.Context, req CloudRunRequest) (*CloudRunResult, error) {
	req.Username = c.user(req.Username)
	var out CloudRunResult
	if err := c.post(ctx, "connector.DeployCloudRun", "/api/v2/connector/gcr/studio/deploy", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSubdomain creates a Route53 DNS record.
// POST /api/v2/connector/dns/subdomain
func (c *Client) CreateSubdomain(ctx context.Context, req SubdomainRequest) (*SubdomainResult, error) {
	req.Username = c.user(req.Username)
	var out SubdomainResult
	if err := c.post(ctx, "connector.CreateSubdomain", "/api/v2/connector/dns/subdomain", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AttachLoadBalancer creates an ALB + target group and registers instances.
// POST /api/v2/connector/lb/attach
func (c *Client) AttachLoadBalancer(ctx context.Context, req LBRequest) (*LBResult, error) {
	req.Username = c.user(req.Username)
	var out LBResult
	if err := c.post(ctx, "connector.AttachLoadBalancer", "/api/v2/connector/lb/attach", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── internals ───────────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, op, path string, body, out any) error {
	u := transport.JoinURL(c.NodeURL, path)
	return c.T.JSON(ctx, op, "POST", u, body, out)
}

func (c *Client) user(requested string) string {
	if requested != "" {
		return requested
	}
	return c.AuthedUsername
}
