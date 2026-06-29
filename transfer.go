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
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if !session.active {
		return "", fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
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
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if !session.active {
		return "", fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
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
	var failPath string
	walkErr := filepath.Walk(localRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			failPath = path
			return err
		}

		rel, err := filepath.Rel(localRoot, path)
		if err != nil {
			failPath = path
			return err
		}
		// Use forward slashes for remote paths regardless of local OS
		remotePath := remoteRoot
		if rel != "." {
			remotePath = remoteRoot + "/" + filepath.ToSlash(rel)
		}

		if info.IsDir() {
			if err := client.MkdirAll(remotePath); err != nil {
				failPath = path
				return err
			}
			return nil
		}

		if err := uploadFile(client, path, remotePath, info.Mode()); err != nil {
			failPath = path
			return err
		}
		count++
		return nil
	})

	if walkErr != nil {
		return count, &TransferError{Op: "upload", Transferred: count, Path: failPath, Err: walkErr}
	}
	return count, nil
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
			return count, &TransferError{Op: "download", Transferred: count, Path: walker.Path(), Err: err}
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
				return count, &TransferError{Op: "download", Transferred: count, Path: remotePath, Err: fmt.Errorf("failed to create local directory: %w", err)}
			}
			continue
		}

		if err := downloadFile(client, remotePath, localPath, info.Mode()); err != nil {
			return count, &TransferError{Op: "download", Transferred: count, Path: remotePath, Err: err}
		}
		count++
	}

	return count, nil
}

// ReadFile reads a remote file over SFTP and returns its content as a string,
// without going through the interactive shell or the emulator. offset is the
// starting byte; length caps how many bytes are returned (0 means "up to
// maxRemoteReadBytes"). The result is truncated to maxRemoteReadBytes
// regardless, and the truncated flag reports whether more data remains beyond
// what was returned.
func (m *SSHManager) ReadFile(sessionID, remotePath string, offset, length int64) (content string, truncated bool, err error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", false, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if !session.isActive() {
		return "", false, fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
	}

	client, err := newSFTPClient(session.client)
	if err != nil {
		return "", false, fmt.Errorf("failed to start sftp: %w", err)
	}
	defer client.Close()

	f, err := client.Open(remotePath)
	if err != nil {
		return "", false, fmt.Errorf("failed to open remote file: %w", err)
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return "", false, fmt.Errorf("failed to seek remote file: %w", err)
		}
	}

	limit := int64(maxRemoteReadBytes)
	if length > 0 && length < limit {
		limit = length
	}

	// Read one extra byte to detect whether more data remains.
	buf := make([]byte, limit+1)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", false, fmt.Errorf("failed to read remote file: %w", err)
	}

	if int64(n) > limit {
		return string(buf[:limit]), true, nil
	}
	// Only a complete read (from the start, not truncated) satisfies the
	// read-before-edit guard, so a partial slice can't unlock a blind edit.
	if offset == 0 {
		session.recordRead(remotePath)
	}
	return string(buf[:n]), false, nil
}

// WriteFile writes content to a remote file over SFTP, creating parent
// directories as needed and truncating any existing file. It does not touch
// the interactive shell or emulator.
func (m *SSHManager) WriteFile(sessionID, remotePath, content string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if !session.isActive() {
		return "", fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
	}

	client, err := newSFTPClient(session.client)
	if err != nil {
		return "", fmt.Errorf("failed to start sftp: %w", err)
	}
	defer client.Close()

	if dir := filepath.ToSlash(filepath.Dir(remotePath)); dir != "." && dir != "" {
		if err := client.MkdirAll(dir); err != nil {
			return "", fmt.Errorf("failed to create remote directory: %w", err)
		}
	}

	f, err := client.Create(remotePath)
	if err != nil {
		return "", fmt.Errorf("failed to create remote file: %w", err)
	}
	defer f.Close()

	n, err := f.Write([]byte(content))
	if err != nil {
		return "", fmt.Errorf("failed to write remote file: %w", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", n, remotePath), nil
}
