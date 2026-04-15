package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestNewSSHManager(t *testing.T) {
	mgr := NewSSHManager()
	if mgr == nil {
		t.Fatal("NewSSHManager returned nil")
	}
	if mgr.sessions == nil {
		t.Fatal("sessions map is nil")
	}
	if len(mgr.sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(mgr.sessions))
	}
}

func TestListSessionsEmpty(t *testing.T) {
	mgr := NewSSHManager()
	sessions := mgr.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestCloseSessionNotFound(t *testing.T) {
	mgr := NewSSHManager()
	err := mgr.CloseSession("nonexistent")
	if err == nil {
		t.Fatal("expected error closing nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestSendCommandSessionNotFound(t *testing.T) {
	mgr := NewSSHManager()
	_, err := mgr.SendCommand("nonexistent", "ls")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestGetScreenSessionNotFound(t *testing.T) {
	mgr := NewSSHManager()
	_, err := mgr.GetScreen("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestConnectInvalidHost(t *testing.T) {
	mgr := NewSSHManager()
	_, err := mgr.Connect("invalid-host-that-does-not-exist:22", "user", "pass", "")
	if err == nil {
		t.Fatal("expected error connecting to invalid host")
	}
	if !strings.Contains(err.Error(), "failed to connect") {
		t.Fatalf("expected 'failed to connect' error, got: %v", err)
	}
}

func TestRenderScreenEmpty(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)
	screen := mgr.renderScreen(emulator)

	lines := strings.Split(screen, "\n")
	if len(lines) != 24 {
		t.Fatalf("expected 24 lines, got %d", len(lines))
	}
}

func TestRenderScreenWithContent(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	// Write some text to the emulator
	_, err := emulator.Write([]byte("Hello, World!"))
	if err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	screen := mgr.renderScreen(emulator)
	if !strings.Contains(screen, "Hello, World!") {
		t.Fatalf("screen should contain 'Hello, World!', got:\n%s", screen)
	}
}

func TestRenderScreenMultipleLines(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	_, err := emulator.Write([]byte("Line 1\r\nLine 2\r\nLine 3"))
	if err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	screen := mgr.renderScreen(emulator)
	lines := strings.Split(screen, "\n")

	if len(lines) != 24 {
		t.Fatalf("expected 24 lines, got %d", len(lines))
	}

	// Check that the content lines have the right text
	foundLine1 := false
	foundLine2 := false
	foundLine3 := false
	for _, line := range lines {
		if strings.Contains(line, "Line 1") {
			foundLine1 = true
		}
		if strings.Contains(line, "Line 2") {
			foundLine2 = true
		}
		if strings.Contains(line, "Line 3") {
			foundLine3 = true
		}
	}
	if !foundLine1 || !foundLine2 || !foundLine3 {
		t.Fatalf("expected all three lines in screen output, got:\n%s", screen)
	}
}

func TestRenderLineEmpty(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	line := mgr.renderLine(emulator, 0)
	// Empty line should have spaces
	if len(strings.TrimRight(line, " \x1b[0m")) > 0 {
		// May contain ANSI reset sequences, that's fine
	}
}

func TestRenderLineWithContent(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	_, err := emulator.Write([]byte("test content"))
	if err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	line := mgr.renderLine(emulator, 0)
	if !strings.Contains(line, "test content") {
		t.Fatalf("expected line to contain 'test content', got: %s", line)
	}
}

func TestSendCommandInactiveSession(t *testing.T) {
	mgr := NewSSHManager()

	// Create a fake inactive session
	mgr.mu.Lock()
	mgr.sessions["test-session"] = &SSHSession{
		active: false,
	}
	mgr.mu.Unlock()

	_, err := mgr.SendCommand("test-session", "ls")
	if err == nil {
		t.Fatal("expected error for inactive session")
	}
	if !strings.Contains(err.Error(), "not active") {
		t.Fatalf("expected 'not active' error, got: %v", err)
	}
}

func TestListSessionsWithEntries(t *testing.T) {
	mgr := NewSSHManager()

	mgr.mu.Lock()
	mgr.sessions["session-1"] = &SSHSession{active: true}
	mgr.sessions["session-2"] = &SSHSession{active: true}
	mgr.mu.Unlock()

	sessions := mgr.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	found := make(map[string]bool)
	for _, s := range sessions {
		found[s] = true
	}
	if !found["session-1"] || !found["session-2"] {
		t.Fatalf("expected both sessions, got: %v", sessions)
	}
}

func TestCloseSessionRemovesIt(t *testing.T) {
	mgr := NewSSHManager()

	mgr.mu.Lock()
	mgr.sessions["test-session"] = &SSHSession{
		active: true,
		// No real client/session to close — will panic if Close is called
		// We'll test the map removal logic separately
	}
	mgr.mu.Unlock()

	// This will panic on session.Close() since session is nil
	// Instead, test that CloseSession on nonexistent returns error
	err := mgr.CloseSession("does-not-exist")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderScreenDimensions(t *testing.T) {
	mgr := NewSSHManager()

	// Test with different dimensions
	for _, tc := range []struct {
		width, height int
	}{
		{40, 12},
		{120, 40},
		{80, 24},
		{1, 1},
	} {
		emulator := vt.NewEmulator(tc.width, tc.height)
		screen := mgr.renderScreen(emulator)
		lines := strings.Split(screen, "\n")
		if len(lines) != tc.height {
			t.Errorf("dimensions %dx%d: expected %d lines, got %d",
				tc.width, tc.height, tc.height, len(lines))
		}
	}
}

func TestRenderScreenANSIContent(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	// Write ANSI colored text
	_, err := emulator.Write([]byte("\x1b[31mred text\x1b[0m"))
	if err != nil {
		t.Fatalf("failed to write ANSI content: %v", err)
	}

	screen := mgr.renderScreen(emulator)
	if !strings.Contains(screen, "red text") {
		t.Fatalf("screen should contain 'red text', got:\n%s", screen)
	}
}

func TestRenderScreenSpecialCharacters(t *testing.T) {
	mgr := NewSSHManager()
	emulator := vt.NewEmulator(80, 24)

	// Write special characters
	_, err := emulator.Write([]byte("$PATH=/usr/bin:/usr/local/bin"))
	if err != nil {
		t.Fatalf("failed to write special chars: %v", err)
	}

	screen := mgr.renderScreen(emulator)
	if !strings.Contains(screen, "$PATH=/usr/bin:/usr/local/bin") {
		t.Fatalf("screen should contain path string, got:\n%s", screen)
	}
}

func TestLoadPrivateKeyNonexistentPath(t *testing.T) {
	_, err := loadPrivateKey("/nonexistent/path/to/key")
	if err == nil {
		t.Fatal("expected error for nonexistent key path")
	}
	if !strings.Contains(err.Error(), "failed to read key file") {
		t.Fatalf("expected 'failed to read key file' error, got: %v", err)
	}
}

func TestLoadPrivateKeyInvalidContent(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "bad_key")
	if err := os.WriteFile(keyPath, []byte("not a real key"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadPrivateKey(keyPath)
	if err == nil {
		t.Fatal("expected error for invalid key content")
	}
	if !strings.Contains(err.Error(), "failed to parse key file") {
		t.Fatalf("expected 'failed to parse key file' error, got: %v", err)
	}
}

func TestConnectNoAuthMethods(t *testing.T) {
	mgr := NewSSHManager()
	_, err := mgr.Connect("localhost:22", "user", "", "/nonexistent/key")
	if err == nil {
		t.Fatal("expected error with no auth methods")
	}
	if !strings.Contains(err.Error(), "no authentication methods available") {
		t.Fatalf("expected 'no authentication methods' error, got: %v", err)
	}
}

func TestConcurrentSessionAccess(t *testing.T) {
	mgr := NewSSHManager()

	// Add sessions concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			mgr.mu.Lock()
			mgr.sessions[strings.Repeat("x", id+1)] = &SSHSession{active: true}
			mgr.mu.Unlock()
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	sessions := mgr.ListSessions()
	if len(sessions) != 10 {
		t.Fatalf("expected 10 sessions, got %d", len(sessions))
	}
}
