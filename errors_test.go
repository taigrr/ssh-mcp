package main

import (
	"errors"
	"testing"
)

func TestTransferErrorErrorIncludesPathWhenPresent(t *testing.T) {
	inner := errors.New("permission denied")
	err := &TransferError{Op: "upload", Transferred: 3, Path: "/tmp/data", Err: inner}

	got := err.Error()
	want := "upload failed at /tmp/data after 3 file(s): permission denied"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestTransferErrorErrorOmitsPathWhenEmpty(t *testing.T) {
	inner := errors.New("broken pipe")
	err := &TransferError{Op: "download", Transferred: 1, Err: inner}

	got := err.Error()
	want := "download failed after 1 file(s): broken pipe"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
