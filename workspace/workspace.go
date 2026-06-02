// Package workspace covers the entire /api/v2/setup/* surface — workspace
// + organization lifecycle, cloud provider credential storage (AWS / GCP
// / Azure), AI provider credential storage (16 providers), Git provider
// connections, payment / SMTP / SSL / OAuth / OKTA / CyberArk creds, and
// the API token lifecycle.
//
// The platform stores everything in HashiCorp Vault under a per-workspace
// path; this SDK never logs request bodies and all credential POSTs are
// over TLS.
//
// Roughly 35 endpoints are wrapped here. Most follow the same shape:
// POST /api/v2/setup/<thing>-credentials with a JSON body containing the
// secret + a name, returning { "stored": true, "vault_path": "..." }.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/prodxcloud/vxcloud/transport"
)

// Client — construct via c.Workspace().
type Client struct {
	T       *transport.Transport
	NodeURL string
	// AuthedUsername / AuthedOrganization are injected into every
	// /api/v2/setup/* body by post(); the server validates both
	// (VaultCredentialsRequest in vxnode/services/workspace/workspace.go).
	AuthedUsername     string
	AuthedOrganization string
}

// ─── Workspace + Organization lifecycle ──────────────────────────────

type CreateWorkspaceInput struct {
	WorkspaceName string `json:"workspace_name"`
	Region        string `json:"region,omitempty"`
}

type WorkspaceResult struct {
	WorkspaceID string                 `json:"workspace_id"`
	Status      string                 `json:"status,omitempty"`
	Raw         map[string]interface{} `json:"-"`
}

// Create a new tenant workspace with Vault-backed storage.
func (c *Client) CreateWorkspace(ctx context.Context, in CreateWorkspaceInput) (*WorkspaceResult, error) {
	if in.WorkspaceName == "" {
		return nil, errors.New("workspace.CreateWorkspace: WorkspaceName is required")
	}
	return wrapWorkspace(c.post(ctx, "/api/v2/setup/workspace", in, "workspace.CreateWorkspace"))
}

type CreateOrganizationInput struct {
	OrgName string `json:"org_name"`
	Plan    string `json:"plan,omitempty"`
}

// CreateOrganization under the current workspace.
func (c *Client) CreateOrganization(ctx context.Context, in CreateOrganizationInput) (map[string]interface{}, error) {
	if in.OrgName == "" {
		return nil, errors.New("workspace.CreateOrganization: OrgName is required")
	}
	return c.post(ctx, "/api/v2/setup/organization", in, "workspace.CreateOrganization")
}

// DeleteWorkspace tears down the current workspace and all resources.
func (c *Client) DeleteWorkspace(ctx context.Context) (map[string]interface{}, error) {
	url := transport.JoinURL(c.NodeURL, "/api/v2/setup/workspace")
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, "workspace.DeleteWorkspace", "DELETE", url, nil, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// ─── Cloud provider credentials ──────────────────────────────────────

// AWSCredentials — JSON tags mirror AWSVariablesRequest in
// vxnode/services/workspace/workspace.go (UPPER_SNAKE, not snake_case).
type AWSCredentials struct {
	AccessKeyID     string `json:"AWS_ACCESS_KEY_ID"`
	SecretAccessKey string `json:"AWS_SECRET_ACCESS_KEY"`
	Region          string `json:"AWS_REGION,omitempty"`
	IAMUser         string `json:"AWS_IAM_USER,omitempty"`
	AccountID       string `json:"AWS_ACCOUNT_ID,omitempty"`
}

func (c *Client) StoreAWSCredentials(ctx context.Context, in AWSCredentials) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/aws-credentials", in, "workspace.StoreAWSCredentials")
}

// AzureCredentials — JSON tags mirror AzureVariablesRequest (workspace.go).
type AzureCredentials struct {
	ClientID       string `json:"AZURE_CLIENT_ID"`
	ClientSecret   string `json:"AZURE_CLIENT_SECRET"`
	TenantID       string `json:"AZURE_TENANT_ID"`
	SubscriptionID string `json:"AZURE_SUBSCRIPTION_ID"`
}

func (c *Client) StoreAzureCredentials(ctx context.Context, in AzureCredentials) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/azure-credentials", in, "workspace.StoreAzureCredentials")
}

// GCPCredentials — JSON tags mirror GCPVariablesRequest (workspace.go).
type GCPCredentials struct {
	ProjectID         string `json:"GCP_PROJECT_ID"`
	ServiceAccountKey string `json:"GCP_SERVICE_ACCOUNT_KEY"` // raw JSON string
}

func (c *Client) StoreGCPCredentials(ctx context.Context, in GCPCredentials) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/gcp-credentials", in, "workspace.StoreGCPCredentials")
}

// GetAllCredentials lists every cloud credential stored in the current
// workspace (names + types only — never returns the secret values).
func (c *Client) GetAllCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/get-all-credentials", map[string]interface{}{}, "workspace.GetAllCredentials")
}

// ─── API token lifecycle ─────────────────────────────────────────────

type CreateAPITokenInput struct {
	TokenName     string `json:"token_name"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
}

type APIToken struct {
	Token     string                 `json:"token"`
	TokenName string                 `json:"token_name,omitempty"`
	ExpiresAt string                 `json:"expires_at,omitempty"`
	Raw       map[string]interface{} `json:"-"`
}

func (c *Client) CreateAPIToken(ctx context.Context, in CreateAPITokenInput) (*APIToken, error) {
	if in.TokenName == "" {
		return nil, errors.New("workspace.CreateAPIToken: TokenName is required")
	}
	raw, err := c.post(ctx, "/api/v2/setup/api-token", in, "workspace.CreateAPIToken")
	if err != nil {
		return nil, err
	}
	return wrapToken(raw), nil
}

func (c *Client) GetAPIToken(ctx context.Context, name string) (*APIToken, error) {
	if name == "" {
		return nil, errors.New("workspace.GetAPIToken: name is required")
	}
	raw, err := c.post(ctx, "/api/v2/setup/get-api-token",
		map[string]interface{}{"token_name": name}, "workspace.GetAPIToken")
	if err != nil {
		return nil, err
	}
	return wrapToken(raw), nil
}

// ─── AI provider credentials (16 providers) ──────────────────────────

// AICredentialsInput is the body shape every /api/v2/setup/ai-*-credentials
// endpoint accepts. Most providers want only ApiKey; some accept extras.
type AICredentialsInput struct {
	APIKey   string `json:"api_key,omitempty"`
	OrgID    string `json:"org_id,omitempty"`
	Endpoint string `json:"endpoint,omitempty"` // for self-hosted Ollama / Hermes
}

// StoreAICredentials writes credentials for one of the supported
// providers. provider is one of: anthropic, openai, gemini, deepseek,
// qwen, groq, mistral, perplexity, huggingface, llama, cohere,
// azure-openai, openclaw, ollama, hermes.
// aiKeyPrefix maps a provider slug to its Vault env-var prefix. The
// server binds <PREFIX>_API_KEY / _MODEL / _BASE_URL — NOT a generic
// "api_key" (see *CredentialsRequest structs in workspace.go).
var aiKeyPrefix = map[string]string{
	"openai": "OPENAI", "anthropic": "ANTHROPIC", "gemini": "GEMINI",
	"deepseek": "DEEPSEEK", "qwen": "QWEN", "huggingface": "HUGGINGFACE",
	"azure-openai": "AZURE_OPENAI", "llama": "LLAMA", "mistral": "MISTRAL",
	"cohere": "COHERE", "perplexity": "PERPLEXITY", "groq": "GROQ",
	"hermes": "HERMES", "openclaw": "OPENCLAW", "ollama": "OLLAMA",
	"brave": "BRAVE",
}

func (c *Client) StoreAICredentials(ctx context.Context, provider string, in AICredentialsInput) (map[string]interface{}, error) {
	if provider == "" {
		return nil, errors.New("workspace.StoreAICredentials: provider is required")
	}
	prefix, ok := aiKeyPrefix[provider]
	if !ok {
		return nil, fmt.Errorf("workspace.StoreAICredentials: unknown provider %q", provider)
	}
	body := map[string]interface{}{}
	if in.APIKey != "" {
		body[prefix+"_API_KEY"] = in.APIKey
	}
	// Endpoint doubles as base URL for self-hosted providers
	// (ollama/hermes/openclaw) and as the Azure OpenAI endpoint.
	if in.Endpoint != "" {
		if provider == "azure-openai" {
			body["AZURE_OPENAI_ENDPOINT"] = in.Endpoint
		} else {
			body[prefix+"_BASE_URL"] = in.Endpoint
		}
	}
	if in.OrgID != "" {
		body[prefix+"_ORGANIZATION"] = in.OrgID
	}
	path := fmt.Sprintf("/api/v2/setup/ai-%s-credentials", provider)
	return c.post(ctx, path, body, "workspace.StoreAICredentials."+provider)
}

// GetAllAICredentials lists every AI provider with stored credentials in
// the current workspace.
func (c *Client) GetAllAICredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/ai-get-all-credentials", map[string]interface{}{}, "workspace.GetAllAICredentials")
}

// ─── Git / messaging / payment / SMTP / SSL / OAuth / OKTA / Vault ───

func (c *Client) StoreGitCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/git-credentials", body, "workspace.StoreGitCredentials")
}

func (c *Client) StoreGitlabCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/gitlab-credentials", body, "workspace.StoreGitlabCredentials")
}

func (c *Client) StoreKubeconfigCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/kubeconfig-credentials", body, "workspace.StoreKubeconfigCredentials")
}

func (c *Client) StoreOAuthCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/oauth-credentials", body, "workspace.StoreOAuthCredentials")
}

func (c *Client) StoreOktaCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/okta-credentials", body, "workspace.StoreOktaCredentials")
}

func (c *Client) StoreCyberarkCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/cyberark-credentials", body, "workspace.StoreCyberarkCredentials")
}

func (c *Client) StoreExternalVaultCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/external-vault-credentials", body, "workspace.StoreExternalVaultCredentials")
}

func (c *Client) GetVaultCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/get-vault-credentials", body, "workspace.GetVaultCredentials")
}

func (c *Client) StorePaymentCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/payment-credentials", body, "workspace.StorePaymentCredentials")
}

func (c *Client) GetAllPaymentCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/payment-get-all-credentials", map[string]interface{}{}, "workspace.GetAllPaymentCredentials")
}

func (c *Client) StoreSMTPCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/smtp-provider-credentials", body, "workspace.StoreSMTPCredentials")
}

func (c *Client) GetAllSMTPCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/smtp-get-all-credentials", map[string]interface{}{}, "workspace.GetAllSMTPCredentials")
}

func (c *Client) StoreMessagingBotCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/messaging-bot-credentials", body, "workspace.StoreMessagingBotCredentials")
}

func (c *Client) GetAllMessagingCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/messaging-get-all-credentials", map[string]interface{}{}, "workspace.GetAllMessagingCredentials")
}

func (c *Client) StoreSSLCertificateCredentials(ctx context.Context, body map[string]interface{}) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/setup/ssl-certificate-credentials", body, "workspace.StoreSSLCertificateCredentials")
}

// DeleteCredential removes a stored credential by name.
func (c *Client) DeleteCredential(ctx context.Context, name string) (map[string]interface{}, error) {
	if name == "" {
		return nil, errors.New("workspace.DeleteCredential: name is required")
	}
	return c.post(ctx, "/api/v2/setup/delete-credential",
		map[string]interface{}{"name": name}, "workspace.DeleteCredential")
}

// ─── Docker credentials (existing — multi-registry under docker/registries/<slug>) ───

// DockerCredentials mirrors DockerVariablesRequest (workspace.go). DockerRegistryName
// is the slug — multiple registries can coexist in one workspace.
type DockerCredentials struct {
	DockerUser         string `json:"DOCKER_USERNAME"`
	DockerPass         string `json:"DOCKER_PASSWORD"`
	DockerEmail        string `json:"DOCKER_EMAIL,omitempty"`
	DockerServer       string `json:"DOCKER_SERVER,omitempty"`
	DockerRegistryName string `json:"DOCKER_REGISTRY_NAME"`
	DockerRegistryType string `json:"DOCKER_REGISTRY_TYPE,omitempty"`
}

func (c *Client) StoreDockerCredentials(ctx context.Context, in DockerCredentials) (map[string]interface{}, error) {
	if in.DockerRegistryName == "" {
		return nil, errors.New("workspace.StoreDockerCredentials: DockerRegistryName is required")
	}
	return c.post(ctx, "/api/v2/setup/docker-credentials", in, "workspace.StoreDockerCredentials")
}

// GetAllDockerCredentials lists every Docker credential entry (sensitive fields masked).
func (c *Client) GetAllDockerCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/vault/get-docker-credentials", map[string]interface{}{}, "workspace.GetAllDockerCredentials")
}

// GetDockerCredentialsByRegistry fetches one Docker credential by registry slug.
func (c *Client) GetDockerCredentialsByRegistry(ctx context.Context, registrySlug string) (map[string]interface{}, error) {
	if registrySlug == "" {
		return nil, errors.New("workspace.GetDockerCredentialsByRegistry: registrySlug is required")
	}
	return c.post(ctx, "/api/v2/vault/get-single-docker-credentials",
		map[string]interface{}{"registry_slug": registrySlug}, "workspace.GetDockerCredentialsByRegistry")
}

// ─── Docker REGISTRY endpoints (new — distinct from credentials) ───────

// DockerRegistry mirrors DockerRegistryRequest. Stored at docker/registry-endpoints/<slug>.
// May reference a saved Docker credential by slug via DefaultCredentialSlug.
type DockerRegistry struct {
	RegistryName          string `json:"registry_name"`
	RegistryType          string `json:"registry_type"` // dockerhub|ecr|gcr|acr|ghcr|gitlab|quay|harbor|jfrog|custom
	RegistryURL           string `json:"registry_url"`
	Namespace             string `json:"namespace,omitempty"`
	Region                string `json:"region,omitempty"`
	DefaultCredentialSlug string `json:"default_credential_slug,omitempty"`
	Description           string `json:"description,omitempty"`
	IsDefault             bool   `json:"is_default,omitempty"`
}

func (c *Client) StoreDockerRegistry(ctx context.Context, in DockerRegistry) (map[string]interface{}, error) {
	if in.RegistryName == "" {
		return nil, errors.New("workspace.StoreDockerRegistry: RegistryName is required")
	}
	if in.RegistryType == "" {
		return nil, errors.New("workspace.StoreDockerRegistry: RegistryType is required")
	}
	if in.RegistryURL == "" {
		return nil, errors.New("workspace.StoreDockerRegistry: RegistryURL is required")
	}
	return c.post(ctx, "/api/v2/setup/docker-registry", in, "workspace.StoreDockerRegistry")
}

func (c *Client) GetAllDockerRegistries(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/vault/get-docker-registries", map[string]interface{}{}, "workspace.GetAllDockerRegistries")
}

func (c *Client) GetDockerRegistry(ctx context.Context, registrySlug string) (map[string]interface{}, error) {
	if registrySlug == "" {
		return nil, errors.New("workspace.GetDockerRegistry: registrySlug is required")
	}
	return c.post(ctx, "/api/v2/vault/get-single-docker-registry",
		map[string]interface{}{"registry_slug": registrySlug}, "workspace.GetDockerRegistry")
}

func (c *Client) DeleteDockerRegistry(ctx context.Context, registrySlug string) (map[string]interface{}, error) {
	if registrySlug == "" {
		return nil, errors.New("workspace.DeleteDockerRegistry: registrySlug is required")
	}
	return c.post(ctx, "/api/v2/vault/delete-docker-registry",
		map[string]interface{}{"registry_slug": registrySlug}, "workspace.DeleteDockerRegistry")
}

// ─── Random / Generic credentials (new — free-form bucket) ─────────────

// RandomCredential is a free-form credential entry. Fields holds arbitrary K/V; JSONBlob
// is opaque text (e.g. a full GCP service-account JSON document).
type RandomCredential struct {
	CredentialName string                 `json:"credential_name"`
	CredentialType string                 `json:"credential_type,omitempty"` // free-form tag
	Description    string                 `json:"description,omitempty"`
	Fields         map[string]interface{} `json:"fields,omitempty"`
	JSONBlob       string                 `json:"json_blob,omitempty"`
}

func (c *Client) StoreRandomCredential(ctx context.Context, in RandomCredential) (map[string]interface{}, error) {
	if in.CredentialName == "" {
		return nil, errors.New("workspace.StoreRandomCredential: CredentialName is required")
	}
	if in.JSONBlob != "" {
		var probe interface{}
		if err := json.Unmarshal([]byte(in.JSONBlob), &probe); err != nil {
			return nil, fmt.Errorf("workspace.StoreRandomCredential: JSONBlob is not valid JSON: %w", err)
		}
	}
	return c.post(ctx, "/api/v2/setup/random-credentials", in, "workspace.StoreRandomCredential")
}

func (c *Client) GetAllRandomCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/vault/get-random-credentials", map[string]interface{}{}, "workspace.GetAllRandomCredentials")
}

func (c *Client) GetRandomCredential(ctx context.Context, credentialSlug string) (map[string]interface{}, error) {
	if credentialSlug == "" {
		return nil, errors.New("workspace.GetRandomCredential: credentialSlug is required")
	}
	return c.post(ctx, "/api/v2/vault/get-single-random-credential",
		map[string]interface{}{"credential_slug": credentialSlug}, "workspace.GetRandomCredential")
}

func (c *Client) DeleteRandomCredential(ctx context.Context, credentialSlug string) (map[string]interface{}, error) {
	if credentialSlug == "" {
		return nil, errors.New("workspace.DeleteRandomCredential: credentialSlug is required")
	}
	return c.post(ctx, "/api/v2/vault/delete-random-credential",
		map[string]interface{}{"credential_slug": credentialSlug}, "workspace.DeleteRandomCredential")
}

// ─── Servers list (new — developer host inventory) ─────────────────────

// ServerEntry is one row in the workspace server inventory.
// Name + IPAddress are required; KeypairName / KeypairLocation are optional.
type ServerEntry struct {
	Name            string   `json:"name"`
	IPAddress       string   `json:"ip_address"`
	Hostname        string   `json:"hostname,omitempty"`
	Port            int      `json:"port,omitempty"`
	Description     string   `json:"description,omitempty"`
	KeypairName     string   `json:"keypair_name,omitempty"`
	KeypairLocation string   `json:"keypair_location,omitempty"`
	Tags            []string `json:"tags,omitempty"`
}

func (c *Client) StoreServer(ctx context.Context, in ServerEntry) (map[string]interface{}, error) {
	if in.Name == "" {
		return nil, errors.New("workspace.StoreServer: Name is required")
	}
	if in.IPAddress == "" {
		return nil, errors.New("workspace.StoreServer: IPAddress is required")
	}
	return c.post(ctx, "/api/v2/setup/server", in, "workspace.StoreServer")
}

func (c *Client) GetAllServers(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/vault/get-servers", map[string]interface{}{}, "workspace.GetAllServers")
}

func (c *Client) GetServer(ctx context.Context, serverSlug string) (map[string]interface{}, error) {
	if serverSlug == "" {
		return nil, errors.New("workspace.GetServer: serverSlug is required")
	}
	return c.post(ctx, "/api/v2/vault/get-single-server",
		map[string]interface{}{"server_slug": serverSlug}, "workspace.GetServer")
}

func (c *Client) DeleteServer(ctx context.Context, serverSlug string) (map[string]interface{}, error) {
	if serverSlug == "" {
		return nil, errors.New("workspace.DeleteServer: serverSlug is required")
	}
	return c.post(ctx, "/api/v2/vault/delete-server",
		map[string]interface{}{"server_slug": serverSlug}, "workspace.DeleteServer")
}

// ─── VM keypairs (backfill — workspace.go existing surface) ────────────

type VMCredentials struct {
	KeyPairName    string `json:"key_pair_name"`
	SSHPublicKey   string `json:"SSH_PUBLIC_KEY,omitempty"`
	SSHPrivateKey  string `json:"SSH_PRIVATE_KEY,omitempty"`
	VMPassword     string `json:"VM_PASSWORD,omitempty"`
}

func (c *Client) StoreVMCredentials(ctx context.Context, in VMCredentials) (map[string]interface{}, error) {
	if in.KeyPairName == "" {
		return nil, errors.New("workspace.StoreVMCredentials: KeyPairName is required")
	}
	return c.post(ctx, "/api/v2/setup/vm-credentials", in, "workspace.StoreVMCredentials")
}

func (c *Client) GetAllVMCredentials(ctx context.Context) (map[string]interface{}, error) {
	return c.post(ctx, "/api/v2/vault/get-vm-credentials", map[string]interface{}{}, "workspace.GetAllVMCredentials")
}

func (c *Client) GetVMCredentialsByKeypair(ctx context.Context, keyPairName string) (map[string]interface{}, error) {
	if keyPairName == "" {
		return nil, errors.New("workspace.GetVMCredentialsByKeypair: keyPairName is required")
	}
	return c.post(ctx, "/api/v2/vault/get-single-vm-credentials",
		map[string]interface{}{"key_pair_name": keyPairName}, "workspace.GetVMCredentialsByKeypair")
}

// ─── GitHub credentials (backfill — own slug under github/credentials/<name>) ───

type GitHubCredentials struct {
	GitHubTokenName string `json:"GITHUB_TOKEN_NAME,omitempty"`
	GitHubToken     string `json:"GITHUB_TOKEN"`
	GitHubUser      string `json:"GITHUB_USER,omitempty"`
	SSHPublicKey    string `json:"SSH_PUBLIC_KEY,omitempty"`
	SSHPrivateKey   string `json:"SSH_PRIVATE_KEY,omitempty"`
}

func (c *Client) StoreGitHubCredentials(ctx context.Context, in GitHubCredentials) (map[string]interface{}, error) {
	if in.GitHubToken == "" {
		return nil, errors.New("workspace.StoreGitHubCredentials: GitHubToken is required")
	}
	return c.post(ctx, "/api/v2/setup/github-credentials", in, "workspace.StoreGitHubCredentials")
}

// ─── internal ────────────────────────────────────────────────────────

func (c *Client) post(ctx context.Context, path string, body interface{}, op string) (map[string]interface{}, error) {
	url := transport.JoinURL(c.NodeURL, path)
	var raw map[string]interface{}
	if err := c.T.JSON(ctx, op, "POST", url, c.withIdentity(body), &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return raw, nil
}

// withIdentity guarantees username + organization are present in the
// request body. Every /api/v2/setup/* endpoint validates them
// server-side (workspace.go); callers historically omitted them, which
// made all credential writes fail with HTTP 400. Round-tripping through
// JSON keeps this working for both struct and map bodies without each
// wrapper repeating the fields. Caller-supplied values always win.
func (c *Client) withIdentity(body interface{}) map[string]interface{} {
	m := map[string]interface{}{}
	if body != nil {
		if b, err := json.Marshal(body); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	if v, ok := m["username"]; !ok || v == "" || v == nil {
		if c.AuthedUsername != "" {
			m["username"] = c.AuthedUsername
		}
	}
	org := c.AuthedOrganization
	if org == "" {
		org = c.AuthedUsername // mirrors vxcli getUserOrg() fallback
	}
	if v, ok := m["organization"]; !ok || v == "" || v == nil {
		if org != "" {
			m["organization"] = org
		}
	}
	return m
}

func wrapWorkspace(raw map[string]interface{}, err error) (*WorkspaceResult, error) {
	if err != nil {
		return nil, err
	}
	r := &WorkspaceResult{Raw: raw}
	if v, ok := raw["workspace_id"].(string); ok {
		r.WorkspaceID = v
	}
	if v, ok := raw["status"].(string); ok {
		r.Status = v
	}
	return r, nil
}

func wrapToken(raw map[string]interface{}) *APIToken {
	t := &APIToken{Raw: raw}
	if v, ok := raw["token"].(string); ok {
		t.Token = v
	}
	if v, ok := raw["token_name"].(string); ok {
		t.TokenName = v
	}
	if v, ok := raw["expires_at"].(string); ok {
		t.ExpiresAt = v
	}
	return t
}
