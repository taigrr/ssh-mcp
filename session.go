package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"golang.org/x/crypto/ssh"
)

// SSHSession holds the live state of a connected SSH session. The emulator
// captures all output for later rendering, and active gates background
// goroutines.
type SSHSession struct {
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	emulator *vt.Emulator
	mu       sync.RWMutex
	active   bool
	cancel   context.CancelFunc
}

// SSHManager owns all live sessions and the allowed-host policy.
type SSHManager struct {
	sessions     map[string]*SSHSession
	allowedHosts []string
	mu           sync.RWMutex
}

// NewSSHManager returns a manager limited to the given allowlist. An empty
// allowlist means no hosts can be connected to.
func NewSSHManager(allowedHosts []string) *SSHManager {
	return &SSHManager{
		sessions:     make(map[string]*SSHSession),
		allowedHosts: allowedHosts,
	}
}

func (s *SSHSession) markInactive() {
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
}

func (s *SSHSession) isActive() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

// isAllowed reports whether the alias is in the manager's allowlist.
func (m *SSHManager) isAllowed(alias string) bool {
	return slices.Contains(m.allowedHosts, alias)
}

// Connect dials the SSH host identified by alias and starts an interactive
// shell. Returns a session ID that can be used with the other manager
// methods.
func (m *SSHManager) Connect(alias string) (string, error) {
	if !m.isAllowed(alias) {
		return "", fmt.Errorf("%w: %q", ErrHostNotAllowed, alias)
	}

	hc := resolveHostConfig(alias)
	authMethods := buildAuthMethods(hc)

	if len(authMethods) == 0 {
		return "", fmt.Errorf("%w for host %q", ErrNoAuthMethods, alias)
	}

	config := &ssh.ClientConfig{
		User:            hc.User,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         connectionTimeout,
	}

	addr := net.JoinHostPort(hc.Hostname, hc.Port)
	conn, err := net.DialTimeout("tcp", addr, connectionTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %w", err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(keepaliveInterval)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, config)
	if err != nil {
		conn.Close()
		return "", fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	if err := session.RequestPty(defaultTermType, termHeight, termWidth, ssh.TerminalModes{}); err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("failed to request pty: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	if err := session.Shell(); err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("failed to start shell: %w", err)
	}

	emulator := vt.NewEmulator(termWidth, termHeight)
	sessionID := fmt.Sprintf("%s@%s:%s-%d", hc.User, hc.Hostname, hc.Port, time.Now().Unix())

	ctx, cancel := context.WithCancel(context.Background())
	sshSession := &SSHSession{
		client:   client,
		session:  session,
		stdin:    stdin,
		emulator: emulator,
		active:   true,
		cancel:   cancel,
	}

	go sshSession.pumpOutput(stdout)
	go sshSession.pumpInput()
	go sshSession.runKeepalive(ctx)

	m.mu.Lock()
	m.sessions[sessionID] = sshSession
	m.mu.Unlock()

	return sessionID, nil
}

// pumpOutput reads from the session's stdout into the virtual terminal until
// the underlying connection is closed. The session is marked inactive on any
// read error.
func (s *SSHSession) pumpOutput(stdout io.Reader) {
	buf := make([]byte, readBufferSize)
	for s.isActive() {
		n, err := stdout.Read(buf)
		if err != nil {
			s.markInactive()
			return
		}
		s.mu.Lock()
		_, _ = s.emulator.Write(buf[:n])
		s.mu.Unlock()
	}
}

// pumpInput forwards the emulator's terminal query responses (Primary DA,
// device-status/cursor-position reports, XTGETTCAP, etc.) back to the remote
// shell's stdin. A plain login shell never needs these, but full-screen apps
// like tmux block on them during startup and mis-detect the terminal when no
// reply arrives, leaving the session blank or garbled.
func (s *SSHSession) pumpInput() {
	buf := make([]byte, readBufferSize)
	for s.isActive() {
		n, err := s.emulator.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			if _, err := s.stdin.Write(buf[:n]); err != nil {
				return
			}
		}
	}
}

// runKeepalive sends periodic keepalive requests to detect dead connections
// and marks the session inactive when the request fails or times out.
// It also writes a null byte to stdin on each tick so that the SSH channel
// itself sees activity (PTY line discipline strips \x00 before the shell sees
// it, so the shell is unaffected).
func (s *SSHSession) runKeepalive(ctx context.Context) {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.isActive() {
				return
			}
			// Connection-level keepalive with a hard timeout so the goroutine
			// never blocks forever when the server doesn't reply.
			result := make(chan error, 1)
			go func() {
				_, _, err := s.client.SendRequest(keepaliveRequest, true, nil)
				result <- err
			}()
			select {
			case err := <-result:
				if err != nil {
					s.markInactive()
					return
				}
			case <-time.After(keepaliveTimeout):
				s.markInactive()
				return
			case <-ctx.Done():
				return
			}
			// Channel-level keepalive: null byte is stripped by PTY line
			// discipline so the shell never sees it.
			_, _ = s.stdin.Write([]byte{0})
		}
	}
}

// SendCommand writes a command to the session's stdin and returns the
// rendered screen content after a short delay to let the host respond.
func (m *SSHManager) SendCommand(sessionID, command string) (string, error) {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return "", err
	}

	if _, err := session.stdin.Write([]byte(command + "\n")); err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	time.Sleep(commandDelay)

	session.mu.RLock()
	screen := renderScreen(session.emulator)
	session.mu.RUnlock()

	return screen, nil
}

// GetScreen returns the current rendered screen for the session after a
// short settle delay.
func (m *SSHManager) GetScreen(sessionID string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	time.Sleep(screenDelay)

	session.mu.RLock()
	screen := renderScreen(session.emulator)
	session.mu.RUnlock()

	return screen, nil
}

// CloseSession tears down the session, closing all underlying resources.
func (m *SSHManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	if session.cancel != nil {
		session.cancel()
	}
	session.active = false

	if session.stdin != nil {
		_ = session.stdin.Close()
	}
	if session.session != nil {
		_ = session.session.Close()
	}
	if session.client != nil {
		_ = session.client.Close()
	}

	delete(m.sessions, sessionID)

	return nil
}

// ListSessions returns the IDs of all known sessions.
func (m *SSHManager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		sessions = append(sessions, id)
	}

	return sessions
}

// ListHosts returns a human-readable list of allowed hosts with their
// resolved connection details.
func (m *SSHManager) ListHosts() []string {
	results := make([]string, 0, len(m.allowedHosts))
	for _, alias := range m.allowedHosts {
		hc := resolveHostConfig(alias)
		results = append(results, fmt.Sprintf("%s: %s@%s:%s", alias, hc.User, hc.Hostname, hc.Port))
	}

	return results
}

// requireActive returns the session for sessionID or a sentinel error if it
// is missing or inactive.
func (m *SSHManager) requireActive(sessionID string) (*SSHSession, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if !session.isActive() {
		return nil, fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
	}

	return session, nil
}
