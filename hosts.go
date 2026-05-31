package main

import (
	"os"
	"path/filepath"
	"strings"

	sshconfig "github.com/kevinburke/ssh_config"
	"github.com/taigrr/jety"
)

// HostConfig holds resolved SSH connection details for a host alias.
type HostConfig struct {
	Hostname string
	Port     string
	User     string
	KeyPath  string
	Password string
}

// resolveHostConfig looks up an alias in ~/.ssh/config and applies sane
// fallbacks for missing fields. The IdentityFile path is expanded so that
// tilde-prefixed paths work without manual handling.
func resolveHostConfig(alias string) HostConfig {
	hc := HostConfig{
		Hostname: sshconfig.Get(alias, "HostName"),
		Port:     sshconfig.Get(alias, "Port"),
		User:     sshconfig.Get(alias, "User"),
		KeyPath:  sshconfig.Get(alias, "IdentityFile"),
	}

	if hc.Hostname == "" {
		hc.Hostname = alias
	}
	if hc.Port == "" {
		hc.Port = defaultPort
	}
	if hc.User == "" {
		hc.User = os.Getenv("USER")
	}

	if strings.HasPrefix(hc.KeyPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			hc.KeyPath = filepath.Join(home, hc.KeyPath[2:])
		}
	}

	return hc
}

// loadAllowedHosts reads the "hosts" key from the active jety configuration
// and returns its string entries. Returns nil when the key is absent or not a
// list of strings.
func loadAllowedHosts() []string {
	raw := jety.Get("hosts")
	if raw == nil {
		return nil
	}

	slice, ok := raw.([]any)
	if !ok {
		return nil
	}

	hosts := make([]string, 0, len(slice))
	for _, val := range slice {
		if host, ok := val.(string); ok {
			hosts = append(hosts, host)
		}
	}

	return hosts
}
