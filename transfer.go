package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// newSFTPClient opens an SFTP subsystem on the given SSH client. The caller
// is responsible for closing the returned client.
func newSFTPClient(client *ssh.Client) (*sftp.Client, error) {
	return sftp.NewClient(client)
}

// Upload copies a local path (file or directory) to a remote destination on
// the given session's host. Directories are copied recursively.
func (m *SSHManager) Upload(sessionID, localPath, remotePath string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("session %s not found", sessionID)
	}
	if !session.active {
		return "", fmt.Errorf("session %s is not active", sessionID)
	}

	client, err := newSFTPClient(session.client)
	if err != nil {
		return "", fmt.Errorf("failed to start sftp: %w", err)
	}
	defer client.Close()

	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat local path: %w", err)
	}

	if info.IsDir() {
		count, err := uploadDir(client, localPath, remotePath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Uploaded %d files from %s to %s", count, localPath, remotePath), nil
	}

	if err := uploadFile(client, localPath, remotePath, info.Mode()); err != nil {
		return "", err
	}
	return fmt.Sprintf("Uploaded %s to %s", localPath, remotePath), nil
}

// Download copies a remote path (file or directory) to a local destination
// from the given session's host. Directories are copied recursively.
func (m *SSHManager) Download(sessionID, remotePath, localPath string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("session %s not found", sessionID)
	}
	if !session.active {
		return "", fmt.Errorf("session %s is not active", sessionID)
	}

	client, err := newSFTPClient(session.client)
	if err != nil {
		return "", fmt.Errorf("failed to start sftp: %w", err)
	}
	defer client.Close()

	info, err := client.Stat(remotePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat remote path: %w", err)
	}

	if info.IsDir() {
		count, err := downloadDir(client, remotePath, localPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Downloaded %d files from %s to %s", count, remotePath, localPath), nil
	}

	if err := downloadFile(client, remotePath, localPath, info.Mode()); err != nil {
		return "", err
	}
	return fmt.Sprintf("Downloaded %s to %s", remotePath, localPath), nil
}

func uploadFile(client *sftp.Client, localPath, remotePath string, mode os.FileMode) error {
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer src.Close()

	if err := client.MkdirAll(filepath.Dir(remotePath)); err != nil {
		return fmt.Errorf("failed to create remote directory: %w", err)
	}

	dst, err := client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("failed to create remote file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	if err := client.Chmod(remotePath, mode); err != nil {
		return fmt.Errorf("failed to set remote file mode: %w", err)
	}

	return nil
}

func uploadDir(client *sftp.Client, localRoot, remoteRoot string) (int, error) {
	if err := client.MkdirAll(remoteRoot); err != nil {
		return 0, fmt.Errorf("failed to create remote directory: %w", err)
	}

	count := 0
	err := filepath.Walk(localRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(localRoot, path)
		if err != nil {
			return err
		}
		// Use forward slashes for remote paths regardless of local OS
		remotePath := remoteRoot
		if rel != "." {
			remotePath = remoteRoot + "/" + filepath.ToSlash(rel)
		}

		if info.IsDir() {
			return client.MkdirAll(remotePath)
		}

		if err := uploadFile(client, path, remotePath, info.Mode()); err != nil {
			return err
		}
		count++
		return nil
	})

	return count, err
}

func downloadFile(client *sftp.Client, remotePath, localPath string, mode os.FileMode) error {
	src, err := client.Open(remotePath)
	if err != nil {
		return fmt.Errorf("failed to open remote file: %w", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	dst, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	if err := os.Chmod(localPath, mode); err != nil {
		return fmt.Errorf("failed to set local file mode: %w", err)
	}

	return nil
}

func downloadDir(client *sftp.Client, remoteRoot, localRoot string) (int, error) {
	if err := os.MkdirAll(localRoot, 0o755); err != nil {
		return 0, fmt.Errorf("failed to create local directory: %w", err)
	}

	count := 0
	walker := client.Walk(remoteRoot)
	for walker.Step() {
		if err := walker.Err(); err != nil {
			return count, err
		}

		remotePath := walker.Path()
		info := walker.Stat()

		rel := strings.TrimPrefix(remotePath, remoteRoot)
		rel = strings.TrimPrefix(rel, "/")
		localPath := localRoot
		if rel != "" {
			localPath = filepath.Join(localRoot, filepath.FromSlash(rel))
		}

		if info.IsDir() {
			if err := os.MkdirAll(localPath, info.Mode()); err != nil {
				return count, fmt.Errorf("failed to create local directory: %w", err)
			}
			continue
		}

		if err := downloadFile(client, remotePath, localPath, info.Mode()); err != nil {
			return count, err
		}
		count++
	}

	return count, nil
}
