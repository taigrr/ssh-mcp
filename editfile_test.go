package main

import (
	"errors"
	"testing"
)

func TestApplyEditReplaceUnique(t *testing.T) {
	got, isDelete, err := applyEdit("hello world", "world", "gophers", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isDelete {
		t.Fatal("replace should not be flagged as delete")
	}
	if got != "hello gophers" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyEditNotFound(t *testing.T) {
	_, _, err := applyEdit("hello world", "absent", "x", false)
	if !errors.Is(err, ErrEditOldStringNotFound) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestApplyEditMultipleMatches(t *testing.T) {
	_, _, err := applyEdit("a a a", "a", "b", false)
	if !errors.Is(err, ErrEditMultipleMatches) {
		t.Fatalf("expected multiple-matches error, got %v", err)
	}
}

func TestApplyEditReplaceAll(t *testing.T) {
	got, _, err := applyEdit("a a a", "a", "b", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b b b" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyEditDeleteUnique(t *testing.T) {
	got, isDelete, err := applyEdit("keep DROP keep", " DROP", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isDelete {
		t.Fatal("expected delete mode")
	}
	if got != "keep keep" {
		t.Fatalf("got %q", got)
	}
}

func TestApplyEditDeleteAllNotFound(t *testing.T) {
	// In replace_all delete mode, no occurrences means the content is
	// unchanged, which Crush reports as old_string not found.
	_, _, err := applyEdit("nothing here", "absent", "", true)
	if !errors.Is(err, ErrEditOldStringNotFound) {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestApplyEditMultipleMatchesDelete(t *testing.T) {
	_, _, err := applyEdit("x x", "x", "", false)
	if !errors.Is(err, ErrEditMultipleMatches) {
		t.Fatalf("expected multiple-matches error, got %v", err)
	}
}

func TestToUnixLineEndings(t *testing.T) {
	got, was := toUnixLineEndings("a\r\nb\r\nc")
	if !was {
		t.Fatal("expected CRLF to be detected")
	}
	if got != "a\nb\nc" {
		t.Fatalf("got %q", got)
	}

	got, was = toUnixLineEndings("a\nb")
	if was {
		t.Fatal("LF-only content should not report CRLF")
	}
	if got != "a\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestToWindowsLineEndings(t *testing.T) {
	got, changed := toWindowsLineEndings("a\nb\nc")
	if !changed {
		t.Fatal("expected conversion to change content")
	}
	if got != "a\r\nb\r\nc" {
		t.Fatalf("got %q", got)
	}

	// Already CRLF: normalized then re-converted, no net change.
	got, changed = toWindowsLineEndings("a\r\nb")
	if changed {
		t.Fatal("already-CRLF content should report no change")
	}
	if got != "a\r\nb" {
		t.Fatalf("got %q", got)
	}
}

// TestEditCrlfRoundTrip verifies that matching is done on LF-normalized content
// but the written result keeps the file's original CRLF endings, exactly like
// Crush.
func TestEditCrlfRoundTrip(t *testing.T) {
	original := "line1\r\nline2\r\nline3"
	unix, isCrlf := toUnixLineEndings(original)
	if !isCrlf {
		t.Fatal("expected CRLF detection")
	}

	// old_string is provided with plain LF (as a model would type it).
	newUnix, _, err := applyEdit(unix, "line2", "LINE2", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out, _ := toWindowsLineEndings(newUnix)
	if out != "line1\r\nLINE2\r\nline3" {
		t.Fatalf("CRLF not preserved, got %q", out)
	}
}

func TestEditFileSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.EditFile("nope", "/tmp/x", "a", "b", false)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestEditFileInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)
	mgr.mu.Lock()
	mgr.sessions["s"] = &SSHSession{id: "s", active: false}
	mgr.mu.Unlock()
	_, err := mgr.EditFile("s", "/tmp/x", "a", "b", false)
	if !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("expected ErrSessionInactive, got %v", err)
	}
}

func TestReadTrackerRecordsAndReports(t *testing.T) {
	s := &SSHSession{id: "s"}
	if !s.lastReadTime("/etc/hosts").IsZero() {
		t.Fatal("unread path should report zero time")
	}
	s.recordRead("/etc/hosts")
	if s.lastReadTime("/etc/hosts").IsZero() {
		t.Fatal("recorded read should report a non-zero time")
	}
}
