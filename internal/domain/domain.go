// Package domain holds the business logic for creality-slicer-mcp.
// This is where you implement your API client, data types, and auth.
package domain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sairaph/mcp-wizard/installer"
	"github.com/sairaph/mcp-wizard/secret"
)

// Identity used by install, doctor and update. Owner and Repo point at the
// GitHub repository that publishes releases.
const (
	ServerName = "creality-slicer-mcp"
	BinaryName = "creality-slicer-mcp"
	Owner      = "sairaph"
	Repo       = "creality-slicer-mcp"
)

// AssetName maps a GOOS/GOARCH pair to the release asset published by
// .goreleaser.yml, which names binaries <project>-<os>-<arch>[.exe].
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s", BinaryName, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// CredentialPath returns the path where credentials are stored.
func CredentialPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".creality-slicer-mcp", "credentials.json")
	}
	return filepath.Join(home, ".creality-slicer-mcp", "credentials.json")
}

// ProjectCredentialPath returns the credentials path inside a project directory.
// Use this with the project-scoped "add" subcommand.
func ProjectCredentialPath(dir string) string {
	return filepath.Join(dir, ".creality-slicer-mcp", "credentials.json")
}

// RemoteConfig describes a hosted MCP endpoint that this binary bridges to.
// When Remote returns non-nil, `creality-slicer-mcp mcp` forwards the AI client's
// stdio session to the remote over Streamable HTTP and adds the credential
// collected by the login step as a request header. The secret lives only in
// the credential store, never in any AI client's config file.
type RemoteConfig struct {
	URL           string // Streamable HTTP endpoint, e.g. https://mcp.example.com/mcp
	HeaderName    string // request header carrying the credential, e.g. "Authorization"
	HeaderPrefix  string // prefix before the credential value, e.g. "Bearer "
	CredentialKey string // key in the credential store, e.g. "token"
	Prompt        string // what the login step asks for, e.g. "your API token"
}

// Remote returns the hosted endpoint this binary bridges to, or nil when the
// binary serves its own MCP server locally (the default).
//
// To ship a one-line installer for a hosted server, return for example:
//
//	return &RemoteConfig{
//	    URL:           "https://mcp.example.com/mcp",
//	    HeaderName:    "Authorization",
//	    HeaderPrefix:  "Bearer ",
//	    CredentialKey: "token",
//	    Prompt:        "your creality-slicer-mcp API token",
//	}
func Remote() *RemoteConfig {
	return nil
}

// LoginConfig returns the credential collection configuration: an API token
// when bridging to a remote endpoint, otherwise a placeholder email stage.
// Replace the placeholder with your actual auth flow.
func LoginConfig(store secret.Store) installer.LoginConfig {
	return installer.LoginConfig{
		ID:        "creality-slicer-mcp-login",
		Label:     "creality-slicer-mcp account",
		Skippable: true,
		Stages:    loginStages(),
		Store:     store,
	}
}

func loginStages() []installer.LoginStage {
	if r := Remote(); r != nil {
		return []installer.LoginStage{{
			Prompt: r.Prompt,
			Field: installer.LoginField{
				Name: r.CredentialKey, Label: "Token", Masked: true,
				Validate: func(v string) error {
					if strings.TrimSpace(v) == "" {
						return fmt.Errorf("the token must not be empty")
					}
					return nil
				},
			},
		}}
	}
	return []installer.LoginStage{{
		Prompt: "your email address",
		Field: installer.LoginField{
			Name: "email", Label: "Email",
		},
	}}
}

// ProjectLoginConfig returns a project-scoped credential collection configuration.
// Credentials are stored via the provided store (built from ProjectCredentialPath).
func ProjectLoginConfig(store secret.Store, dir string) installer.LoginConfig {
	return installer.LoginConfig{
		ID:        "creality-slicer-mcp-login-project",
		Label:     fmt.Sprintf("creality-slicer-mcp account (%s)", filepath.Base(dir)),
		Skippable: true,
		Stages:    loginStages(),
		Store:     store,
	}
}

// DefaultEnv returns the environment variables written into AI client
// configs when the server is registered. The server reads TRANSPORT (stdio
// or http) and ADDR (listen address for http) at startup.
func DefaultEnv() map[string]string {
	return map[string]string{
		"TRANSPORT": "stdio",
	}
}
