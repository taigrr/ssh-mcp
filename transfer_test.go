package main

import (
	"strings"
	"testing"
)

func TestUploadSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.Upload("nonexistent", "/tmp/foo", "/tmp/bar")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestDownloadSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.Download("nonexistent", "/tmp/foo", "/tmp/bar")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestUploadInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test"] = &SSHSession{active: false}
	mgr.mu.Unlock()

	_, err := mgr.Upload("test", "/tmp/foo", "/tmp/bar")
	if err == nil {
		t.Fatal("expected error for inactive session")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected 'not active' error, got: %v", err)
	}
}

func TestDownloadInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test"] = &SSHSession{active: false}
	mgr.mu.Unlock()

	_, err := mgr.Download("test", "/tmp/foo", "/tmp/bar")
	if err == nil {
		t.Fatal("expected error for inactive session")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected 'not active' error, got: %v", err)
	}
}
