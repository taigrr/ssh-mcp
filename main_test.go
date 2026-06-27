package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

func TestNewSSHManager(t *testing.T) {
	mgr := NewSSHManager(nil)
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
	mgr := NewSSHManager(nil)
	sessions := mgr.ListSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestCloseSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	err := mgr.CloseSession("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestSendCommandSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.SendCommand("nonexistent", "ls")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestGetScreenSessionNotFound(t *testing.T) {
	mgr := NewSSHManager(nil)
	_, err := mgr.GetScreen("nonexistent")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("expected ErrSessionNotFound, got: %v", err)
	}
}

func TestConnectNotAllowed(t *testing.T) {
	mgr := NewSSHManager([]string{"server1"})
	_, err := mgr.Connect("server2")
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("expected ErrHostNotAllowed, got: %v", err)
	}
}

func TestConnectAllowedButUnreachable(t *testing.T) {
	mgr := NewSSHManager([]string{"nonexistent-host-xyz"})
	_, err := mgr.Connect("nonexistent-host-xyz")
	if err == nil {
		t.Fatal("expected error connecting to unreachable host")
	}
}

func TestIsAllowed(t *testing.T) {
	mgr := NewSSHManager([]string{"server1", "server2"})

	if !mgr.isAllowed("server1") {
		t.Fatal("expected server1 to be allowed")
	}
	if !mgr.isAllowed("server2") {
		t.Fatal("expected server2 to be allowed")
	}
	if mgr.isAllowed("server3") {
		t.Fatal("expected server3 to NOT be allowed")
	}
}

func TestRenderScreenEmpty(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)
	screen := renderScreen(emulator)

	lines := strings.Split(screen, "\n")
	if len(lines) != termHeight {
		t.Fatalf("expected %d lines, got %d", termHeight, len(lines))
	}
}

func TestRenderScreenWithContent(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)

	if _, err := emulator.Write([]byte("Hello, World!")); err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	screen := renderScreen(emulator)
	if !strings.Contains(screen, "Hello, World!") {
		t.Fatalf("screen should contain 'Hello, World!', got:\n%s", screen)
	}
}

func TestRenderScreenMultipleLines(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)

	if _, err := emulator.Write([]byte("Line 1\r\nLine 2\r\nLine 3")); err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	screen := renderScreen(emulator)
	lines := strings.Split(screen, "\n")

	if len(lines) != termHeight {
		t.Fatalf("expected %d lines, got %d", termHeight, len(lines))
	}

	foundLine1 := strings.Contains(screen, "Line 1")
	foundLine2 := strings.Contains(screen, "Line 2")
	foundLine3 := strings.Contains(screen, "Line 3")
	if !foundLine1 || !foundLine2 || !foundLine3 {
		t.Fatalf("expected all three lines in screen output, got:\n%s", screen)
	}
}

func TestRenderLineEmpty(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)
	line := renderLine(emulator, 0)
	_ = line
}

func TestRenderLineWithContent(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)

	if _, err := emulator.Write([]byte("test content")); err != nil {
		t.Fatalf("failed to write to emulator: %v", err)
	}

	line := renderLine(emulator, 0)
	if !strings.Contains(line, "test content") {
		t.Fatalf("expected line to contain 'test content', got: %s", line)
	}
}

func TestSendCommandInactiveSession(t *testing.T) {
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test-session"] = &SSHSession{active: false}
	mgr.mu.Unlock()

	_, err := mgr.SendCommand("test-session", "ls")
	if !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("expected ErrSessionInactive, got: %v", err)
	}
}

func TestListSessionsWithEntries(t *testing.T) {
	mgr := NewSSHManager(nil)

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
	mgr := NewSSHManager(nil)

	mgr.mu.Lock()
	mgr.sessions["test-session"] = &SSHSession{active: true}
	mgr.mu.Unlock()

	if err := mgr.CloseSession("test-session"); err != nil {
		t.Fatalf("expected close to succeed, got: %v", err)
	}

	mgr.mu.RLock()
	_, exists := mgr.sessions["test-session"]
	mgr.mu.RUnlock()
	if exists {
		t.Fatal("expected session to be removed after close")
	}
}

func TestRenderScreenDimensions(t *testing.T) {
	for _, tc := range []struct {
		width, height int
	}{
		{40, 12},
		{120, 40},
		{80, 24},
		{1, 1},
	} {
		emulator := vt.NewSafeEmulator(tc.width, tc.height)
		screen := renderScreen(emulator)
		lines := strings.Split(screen, "\n")
		if len(lines) != tc.height {
			t.Errorf("dimensions %dx%d: expected %d lines, got %d",
				tc.width, tc.height, tc.height, len(lines))
		}
	}
}

func TestRenderScreenANSIContent(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)

	if _, err := emulator.Write([]byte("\x1b[31mred text\x1b[0m")); err != nil {
		t.Fatalf("failed to write ANSI content: %v", err)
	}

	screen := renderScreen(emulator)
	if !strings.Contains(screen, "red text") {
		t.Fatalf("screen should contain 'red text', got:\n%s", screen)
	}
}

func TestRenderScreenSpecialCharacters(t *testing.T) {
	emulator := vt.NewSafeEmulator(termWidth, termHeight)

	if _, err := emulator.Write([]byte("$PATH=/usr/bin:/usr/local/bin")); err != nil {
		t.Fatalf("failed to write special chars: %v", err)
	}

	screen := renderScreen(emulator)
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

func TestConcurrentSessionAccess(t *testing.T) {
	mgr := NewSSHManager(nil)

	done := make(chan bool, 10)
	for i := range 10 {
		go func(id int) {
			mgr.mu.Lock()
			mgr.sessions[strings.Repeat("x", id+1)] = &SSHSession{active: true}
			mgr.mu.Unlock()
			done <- true
		}(i)
	}

	for range 10 {
		<-done
	}

	sessions := mgr.ListSessions()
	if len(sessions) != 10 {
		t.Fatalf("expected 10 sessions, got %d", len(sessions))
	}
}

func TestListHosts(t *testing.T) {
	mgr := NewSSHManager([]string{"localhost"})

	hosts := mgr.ListHosts()
	if len(hosts) != 1 {
		t.Fatalf("expected 1 host, got %d", len(hosts))
	}
	if !strings.Contains(hosts[0], "localhost") {
		t.Fatalf("expected localhost entry, got: %s", hosts[0])
	}
}

func TestListHostsEmpty(t *testing.T) {
	mgr := NewSSHManager(nil)
	hosts := mgr.ListHosts()
	if len(hosts) != 0 {
		t.Fatalf("expected 0 hosts, got %d", len(hosts))
	}
}

func TestResolveHostConfig(t *testing.T) {
	hc := resolveHostConfig("some-random-host-xyz")
	if hc.Hostname != "some-random-host-xyz" {
		t.Fatalf("expected hostname to be the alias itself, got: %s", hc.Hostname)
	}
	if hc.Port != defaultPort {
		t.Fatalf("expected port %s, got: %s", defaultPort, hc.Port)
	}
}
