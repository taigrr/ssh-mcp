package main

import (
	"errors"
	"testing"
)

func TestUploadSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.Upload("nonexistent", "/tmp/foo", "/tmp/bar")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestDownloadSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.Download("nonexistent", "/tmp/foo", "/tmp/bar")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestUploadInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test"] = &SSHSession{active: false}
	mgr.mu.Unlock()

	_, err := mgr.Upload("test", "/tmp/foo", "/tmp/bar")
	if !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("expected ErrSessionInactive, got: %v", err)
	}
}

func TestDownloadInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test"] = &SSHSession{active: false}
	mgr.mu.Unlock()

	_, err := mgr.Download("test", "/tmp/foo", "/tmp/bar")
	if !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("expected ErrSessionInactive, got: %v", err)
	}
}
