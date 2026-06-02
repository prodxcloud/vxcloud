// Package cred is an internal helper that loads ~/.vxcloud/credentials.json
// — the same file vxcli writes during `vxcli auth login`. Read-only.
//
// Importing this package is optional: the SDK can be initialized purely
// from explicit options. cred is provided so that an existing vxcli user
// can construct an SDK Client without re-supplying their key.
package cred

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// File describes the on-disk credentials file produced by vxcli.
// Field names match the JSON keys vxcli writes today.
type File struct {
	APIKey       string `json:"api_key"`
	Username     string `json:"username"`
	Organization string `json:"organization,omitempty"`
	Workspace    string `json:"workspace,omitempty"`
	Environment  string `json:"environment,omitempty"`
	BaseURL      string `json:"base_url,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	NodeURL      string `json:"node_url,omitempty"`
	// TenantID / OrganizationID identify the tenant for the agentcontrol
	// surface (X-Tenant-ID header). vxcli may write either key.
	TenantID       string `json:"tenant_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`
	IsValid        bool   `json:"is_valid"`
}

// Path returns the OS-appropriate path to the credentials file.
func Path() string {
	var home string
	if runtime.GOOS == "windows" {
		home = os.Getenv("USERPROFILE")
	} else {
		home, _ = os.UserHomeDir()
		if home == "" {
			home = os.Getenv("HOME")
		}
	}
	return filepath.Join(home, ".vxcloud", "credentials.json")
}

// Load reads and parses the credentials file. Returns os.ErrNotExist if
// vxcli has never been used on this machine.
func Load() (*File, error) {
	p := Path()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, errors.New("vxsdk: credentials.json is corrupt — re-run `vxcli auth login`")
	}
	return &f, nil
}
