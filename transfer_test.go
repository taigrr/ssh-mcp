package main

import (
	"errors"
	"fmt"
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

func TestTransferErrorTyped(t *testing.T) {
	inner := errors.New("disk full")
	wrapped := fmt.Errorf("transfer wrapper: %w", &TransferError{
		Op:          "upload",
		Transferred: 7,
		Path:        "/tmp/big",
		Err:         inner,
	})

	te, ok := errors.AsType[*TransferError](wrapped)
	if !ok {
		t.Fatalf("expected to extract *TransferError via errors.AsType, got: %v", wrapped)
	}
	if te.Transferred != 7 || te.Path != "/tmp/big" || te.Op != "upload" {
		t.Fatalf("unexpected fields: %+v", te)
	}
	if !errors.Is(wrapped, inner) {
		t.Fatalf("expected errors.Is to find inner cause through TransferError.Unwrap")
	}
}
