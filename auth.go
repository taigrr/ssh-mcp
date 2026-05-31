package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// loadPrivateKey reads and parses the private key at keyPath. When keyPath is
// empty, it falls back to the user's default keys (~/.ssh/id_ed25519 then
// ~/.ssh/id_rsa).
func loadPrivateKey(keyPath string) (ssh.Signer, error) {
	if keyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}

		keyPath = filepath.Join(home, ".ssh", "id_ed25519")
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			keyPath = filepath.Join(home, ".ssh", "id_rsa")
		}
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key file %s: %w", keyPath, err)
	}

	return signer, nil
}

// buildAuthMethods assembles the authentication methods for an SSH connection
// in priority order: SSH agent, configured key, default key files, then
// password.
func buildAuthMethods(hc HostConfig) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	if hc.KeyPath != "" {
		if signer, err := loadPrivateKey(hc.KeyPath); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	if len(methods) == 0 {
		if signer, err := loadPrivateKey(""); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	if hc.Password != "" {
		methods = append(methods, ssh.Password(hc.Password))
	}

	return methods
}
