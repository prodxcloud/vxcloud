// Package cloud is the resource module for cloud-side provisioning.
//
// These endpoints provision real resources in AWS/GCP/Azure, NOT on the
// SSH-into-VM path that install/ and deploy/ use. Examples:
//
//   - S3 buckets, GCS buckets
//   - IAM roles and policies
//   - VPCs and subnets
//   - EC2 / Compute Engine instances
//   - RDS / Cloud SQL instances
//   - Lambda / Cloud Functions
//   - EKS / GKE clusters
//
// Backed by /api/v2/tenant/provision/* on the tenant node, which in turn
// runs Terraform server-side.
package cloud

import (
	"context"
	"fmt"

	vxerrors "github.com/prodxcloud/vxcloud/errors"
	"github.com/prodxcloud/vxcloud/transport"
)

// Result is what every cloud provisioning endpoint returns.
type Result struct {
	SessionID        string                 `json:"session_id"`
	Status           string                 `json:"status,omitempty"`
	ResourceName     string                 `json:"resource_name,omitempty"`
	ARN              string                 `json:"arn,omitempty"`
	BucketName       string                 `json:"bucket_name,omitempty"`
	InstanceID       string                 `json:"instance_id,omitempty"`
	ExecutionTime    string                 `json:"execution_time,omitempty"`
	StatePath        string                 `json:"state_path,omitempty"`
	TerraformOutputs map[string]interface{} `json:"terraform_outputs,omitempty"`
}

// Client is the cloud facade.
type Client struct {
	T              *transport.Transport
	NodeURL        string
	AuthedUsername string
}

// S3 returns the S3 / object-storage sub-client.
func (c *Client) S3() *S3 { return &S3{c: c} }

// IAM returns the IAM sub-client (roles + policies).
func (c *Client) IAM() *IAM { return &IAM{c: c} }

// VM returns the EC2 / Compute-Engine sub-client.
func (c *Client) VM() *VM { return &VM{c: c} }

// Network returns the VPC / network sub-client.
func (c *Client) Network() *Network { return &Network{c: c} }

// Database returns the RDS / Cloud SQL sub-client.
func (c *Client) Database() *Database { return &Database{c: c} }

// Kubernetes returns the EKS / GKE sub-client.
func (c *Client) Kubernetes() *Kubernetes { return &Kubernetes{c: c} }

// Serverless returns the Lambda / Cloud Functions sub-client.
func (c *Client) Serverless() *Serverless { return &Serverless{c: c} }

// commonProvision sends a provisioning POST to the given path with the
// canonical envelope all /api/v2/tenant/provision/* handlers expect.
func (c *Client) commonProvision(ctx context.Context, op, path, app, cloud, region, env, resourceType string, extras map[string]interface{}) (*Result, error) {
	if env == "" {
		env = "development"
	}
	if cloud == "" {
		cloud = "aws"
	}
	if region == "" {
		region = "us-east-1"
	}
	body := map[string]interface{}{
		"app_name":       app,
		"resource_name":  app,
		"instance_name":  app,
		"network_name":   app,
		"key_name":       app,
		"role_name":      app,
		"hostname":       app,
		"cloud_provider": cloud,
		"region":         region,
		"environment":    env,
		"resource_type":  resourceType,
		"username":       c.AuthedUsername,
	}
	for k, v := range extras {
		body[k] = v
	}
	u := transport.JoinURL(c.NodeURL, path)
	var out Result
	if err := c.T.JSON(ctx, op, "POST", u, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── S3 / Storage ──────────────────────────────────────────────────

type S3 struct{ c *Client }

// CreateBucket provisions a bucket. Cloud may be "aws" (S3) or "gcp" (GCS)
// or "azure" (Blob); region is the cloud-specific region string.
func (s *S3) CreateBucket(ctx context.Context, name, cloud, region string) (*Result, error) {
	return s.c.commonProvision(ctx,
		"cloud.S3.CreateBucket",
		"/api/v2/tenant/provision/storage",
		name, cloud, region, "", "s3", nil)
}

// ── IAM ────────────────────────────────────────────────────────────

type IAM struct{ c *Client }

// CreatePolicy provisions an IAM policy. policyDocument is the inline JSON
// document; the SDK passes it through to the Terraform aws_iam_policy
// resource.
func (i *IAM) CreatePolicy(ctx context.Context, name string, policyDocument string) (*Result, error) {
	return i.c.commonProvision(ctx,
		"cloud.IAM.CreatePolicy",
		"/api/v2/tenant/provision/security",
		name, "aws", "", "", "policy",
		map[string]interface{}{"policy_document": policyDocument},
	)
}

// CreateRole provisions an IAM role. assumeRolePolicy is the trust policy
// document.
func (i *IAM) CreateRole(ctx context.Context, name string, assumeRolePolicy string) (*Result, error) {
	return i.c.commonProvision(ctx,
		"cloud.IAM.CreateRole",
		"/api/v2/tenant/provision/security",
		name, "aws", "", "", "role",
		map[string]interface{}{"assume_role_policy": assumeRolePolicy},
	)
}

// CreateKeypair provisions an EC2 key pair. The private key is stored in
// the workspace vault under the given name; the public material is
// uploaded to AWS.
func (i *IAM) CreateKeypair(ctx context.Context, name, region string) (*Result, error) {
	return i.c.commonProvision(ctx,
		"cloud.IAM.CreateKeypair",
		"/api/v2/tenant/provision/security",
		name, "aws", region, "", "keypair", nil)
}

// ── VM (EC2 / Compute Engine) ─────────────────────────────────────

type VM struct{ c *Client }

// VMOpts describes a VM provision request.
type VMOpts struct {
	InstanceType string // e.g. t3.small
	VolumeSizeGB int
	Replicas     int
}

// Create provisions a VM.
func (v *VM) Create(ctx context.Context, name, cloud, region string, opts VMOpts) (*Result, error) {
	if opts.InstanceType == "" {
		opts.InstanceType = "t2.micro"
	}
	if opts.VolumeSizeGB == 0 {
		opts.VolumeSizeGB = 30
	}
	if opts.Replicas == 0 {
		opts.Replicas = 1
	}
	return v.c.commonProvision(ctx,
		"cloud.VM.Create",
		"/api/v2/tenant/provision/vm",
		name, cloud, region, "", "vm",
		map[string]interface{}{
			"instance_type": opts.InstanceType,
			"volume_size":   opts.VolumeSizeGB,
			"replicas":      opts.Replicas,
		},
	)
}

// StatusInput describes a VM status lookup.
type StatusInput struct {
	// InstanceID is the cloud provider's instance identifier (e.g. an
	// AWS EC2 instance id). Required.
	InstanceID string
	// Cloud is the provider ("aws" | "gcp" | "azure"). Defaults to "aws".
	Cloud string
}

// ActionInput describes a VM lifecycle action.
type ActionInput struct {
	// InstanceID is the cloud provider's instance identifier. Required.
	InstanceID string
	// Action is one of "start", "stop", "restart", "reboot". Required.
	Action string
	// Cloud is the provider ("aws" | "gcp" | "azure"). Defaults to "aws".
	Cloud string
}

// Status returns the current power / lifecycle state of a previously-
// provisioned VM.
//
// Maps to POST {NodeURL}/api/v2/provision/vm/status.
func (v *VM) Status(ctx context.Context, input StatusInput) (map[string]any, error) {
	if input.InstanceID == "" {
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "cloud.VM.Status", Message: "InstanceID is required",
		}}
	}
	provider := input.Cloud
	if provider == "" {
		provider = "aws"
	}
	url := transport.JoinURL(v.c.NodeURL, "/api/v2/provision/vm/status")
	body := map[string]any{
		"instance_id": input.InstanceID,
		"provider":    provider,
	}
	var out map[string]any
	if err := v.c.T.JSON(ctx, "cloud.VM.Status", "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Action issues a lifecycle action against a previously-provisioned VM.
// Valid actions are "start", "stop", "restart", and "reboot" — anything
// else returns a ValidationError before any network round-trip.
//
// Maps to POST {NodeURL}/api/v2/provision/vm/action.
func (v *VM) Action(ctx context.Context, input ActionInput) (map[string]any, error) {
	if input.InstanceID == "" {
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "cloud.VM.Action", Message: "InstanceID is required",
		}}
	}
	switch input.Action {
	case "start", "stop", "restart", "reboot":
		// ok
	default:
		return nil, &vxerrors.ValidationError{Failure: &vxerrors.Failure{
			Op: "cloud.VM.Action",
			Message: fmt.Sprintf(
				"Action must be one of start/stop/restart/reboot (got %q)",
				input.Action),
		}}
	}
	provider := input.Cloud
	if provider == "" {
		provider = "aws"
	}
	url := transport.JoinURL(v.c.NodeURL, "/api/v2/provision/vm/action")
	body := map[string]any{
		"instance_id": input.InstanceID,
		"action":      input.Action,
		"provider":    provider,
	}
	var out map[string]any
	if err := v.c.T.JSON(ctx, "cloud.VM.Action", "POST", url, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Network (VPC) ────────────────────────────────────────────────

type Network struct{ c *Client }

// CreateVPC provisions a VPC.
func (n *Network) CreateVPC(ctx context.Context, name, cloud, region string) (*Result, error) {
	return n.c.commonProvision(ctx,
		"cloud.Network.CreateVPC",
		"/api/v2/tenant/provision/networks",
		name, cloud, region, "", "vpc", nil)
}

// ── Database (RDS / Aurora / ElastiCache Redis) ───────────────────

type Database struct{ c *Client }

// DBOpts describes a managed database provision request.
type DBOpts struct {
	Engine                  string // "postgres" | "postgresql" | "mysql" | "mariadb"
	EngineVersion           string
	InstanceClass           string
	StorageGB               int
	Username                string
	Password                string
	DBName                  string
	Port                    int
	VPCID                   string
	SubnetIDs               []string
	AllowedSecurityGroupIDs []string
	VPCSecurityGroupIDs     []string
	InstanceCount           int
	NodeType                string
	NumCacheNodes           int
	PubliclyAccessible      bool
	MultiAZ                 bool
	BackupRetention         int
	Encryption              bool
}

// CreateRDS provisions an RDS instance.
func (d *Database) CreateRDS(ctx context.Context, name, region string, opts DBOpts) (*Result, error) {
	return d.c.commonProvision(ctx,
		"cloud.Database.CreateRDS",
		"/api/v2/tenant/provision/databases",
		name, "aws", region, "", "rds",
		map[string]interface{}{"configuration": databaseConfig(name, region, "rds", opts)},
	)
}

// CreateAurora provisions an Aurora cluster. Pass at least two SubnetIDs.
func (d *Database) CreateAurora(ctx context.Context, name, region string, opts DBOpts) (*Result, error) {
	return d.c.commonProvision(ctx,
		"cloud.Database.CreateAurora",
		"/api/v2/tenant/provision/databases",
		name, "aws", region, "", "aurora",
		map[string]interface{}{"configuration": databaseConfig(name, region, "aurora", opts)},
	)
}

// CreateRedis provisions an ElastiCache Redis cluster.
func (d *Database) CreateRedis(ctx context.Context, name, region string, opts DBOpts) (*Result, error) {
	return d.c.commonProvision(ctx,
		"cloud.Database.CreateRedis",
		"/api/v2/tenant/provision/databases",
		name, "aws", region, "", "redis",
		map[string]interface{}{"configuration": databaseConfig(name, region, "redis", opts)},
	)
}

func databaseConfig(name, region, resourceType string, opts DBOpts) map[string]interface{} {
	cfg := map[string]interface{}{
		"region": region,
	}
	switch resourceType {
	case "redis":
		nodeType := opts.NodeType
		if nodeType == "" {
			nodeType = "cache.t3.micro"
		}
		numCacheNodes := opts.NumCacheNodes
		if numCacheNodes == 0 {
			numCacheNodes = 1
		}
		cfg["node_type"] = nodeType
		cfg["num_cache_nodes"] = numCacheNodes
		if len(opts.SubnetIDs) > 0 {
			cfg["subnet_ids"] = opts.SubnetIDs
		}
		if len(opts.VPCSecurityGroupIDs) > 0 {
			cfg["vpc_security_group_ids"] = opts.VPCSecurityGroupIDs
		}
		return cfg
	default:
		engine := opts.Engine
		if engine == "" {
			engine = "mysql"
		}
		version := opts.EngineVersion
		if version == "" {
			version = "8.0"
		}
		instanceClass := opts.InstanceClass
		if instanceClass == "" {
			instanceClass = "db.t3.micro"
			if resourceType == "aurora" {
				instanceClass = "db.t3.medium"
			}
		}
		storageGB := opts.StorageGB
		if storageGB == 0 {
			storageGB = 20
		}
		port := opts.Port
		if port == 0 {
			port = 3306
			if engine == "postgres" || engine == "postgresql" {
				port = 5432
			}
		}
		dbName := opts.DBName
		if dbName == "" {
			dbName = name
		}
		username := opts.Username
		if username == "" {
			username = "admin"
		}
		cfg["engine"] = engine
		cfg["version"] = version
		cfg["instance_type"] = instanceClass
		cfg["storage_size"] = storageGB
		cfg["multi_az"] = opts.MultiAZ
		cfg["backup_retention"] = opts.BackupRetention
		if opts.BackupRetention == 0 {
			cfg["backup_retention"] = 7
		}
		cfg["encryption"] = true
		cfg["publicly_accessible"] = opts.PubliclyAccessible
		cfg["username"] = username
		cfg["password"] = opts.Password
		cfg["db_name"] = dbName
		cfg["port"] = port
		cfg["vpc_id"] = opts.VPCID
		if resourceType == "aurora" {
			cfg["instance_count"] = opts.InstanceCount
			if opts.InstanceCount == 0 {
				cfg["instance_count"] = 2
			}
		}
		if len(opts.SubnetIDs) > 0 {
			cfg["subnet_ids"] = opts.SubnetIDs
		}
		if len(opts.AllowedSecurityGroupIDs) > 0 {
			cfg["allowed_security_group_ids"] = opts.AllowedSecurityGroupIDs
		}
		return cfg
	}
}

// ── Kubernetes (EKS / GKE) ────────────────────────────────────────

type Kubernetes struct{ c *Client }

// CreateCluster provisions a managed Kubernetes cluster.
func (k *Kubernetes) CreateCluster(ctx context.Context, name, cloud, region string) (*Result, error) {
	return k.c.commonProvision(ctx,
		"cloud.Kubernetes.CreateCluster",
		"/api/v2/tenant/provision/kubernetes",
		name, cloud, region, "", "eks", nil)
}

// ── Serverless (Lambda / Cloud Functions) ────────────────────────

type Serverless struct{ c *Client }

// CreateFunction provisions a serverless function.
func (s *Serverless) CreateFunction(ctx context.Context, name, cloud, region, runtime string) (*Result, error) {
	return s.c.commonProvision(ctx,
		"cloud.Serverless.CreateFunction",
		"/api/v2/tenant/provision/serverless",
		name, cloud, region, "", "lambda",
		map[string]interface{}{"runtime": runtime},
	)
}
