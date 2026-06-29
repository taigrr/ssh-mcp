package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"golang.org/x/crypto/ssh"
)

// SSHSession holds the live state of a connected SSH session. The emulator
// captures all output for later rendering, and active gates background
// goroutines.
type SSHSession struct {
	id       string
	manager  *SSHManager
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	stdinMu  sync.Mutex
	emulator *vt.SafeEmulator
	mu       sync.RWMutex
	active   bool
	cancel   context.CancelFunc
	width    int
	height   int

	// tap, when non-nil, receives a copy of every raw stdout chunk in
	// addition to the emulator. It is installed for the duration of an
	// ssh_exec call so completion can be detected without ever filtering the
	// emulator's view. Guarded by tapMu.
	tap   *execWatcher
	tapMu sync.Mutex

	// reads tracks the last time each remote path was read via ReadFile, the
	// SFTP analog of Crush's filetracker. EditFile uses it to enforce
	// read-before-edit and to detect a file that changed on the remote since
	// it was last read. Guarded by readsMu.
	reads   map[string]time.Time
	readsMu sync.Mutex
}

// recordRead notes that remotePath was just read, satisfying the
// read-before-edit guard for subsequent EditFile calls.
func (s *SSHSession) recordRead(remotePath string) {
	s.readsMu.Lock()
	defer s.readsMu.Unlock()
	if s.reads == nil {
		s.reads = make(map[string]time.Time)
	}
	s.reads[remotePath] = time.Now()
}

// lastReadTime returns the time remotePath was last read, or the zero time if
// it has not been read in this session.
func (s *SSHSession) lastReadTime(remotePath string) time.Time {
	s.readsMu.Lock()
	defer s.readsMu.Unlock()
	return s.reads[remotePath]
}

// writeStdin serializes all writes to the remote shell's stdin. Terminal
// query responses, keepalive bytes, and user commands all share this pipe;
// without serialization their bytes interleave mid-sequence and corrupt the
// query/response handshake that full-screen apps like tmux rely on at startup.
func (s *SSHSession) writeStdin(p []byte) error {
	s.stdinMu.Lock()
	defer s.stdinMu.Unlock()
	_, err := s.stdin.Write(p)
	return err
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

// markInactive flips the active flag and asynchronously removes the session
// from its manager. Background goroutines (pumpOutput, runKeepalive) call
// this when they detect the remote side is gone, so a stale entry can never
// be returned by ListSessions or matched by SendCommand/GetScreen with a
// confusing "session inactive" error.
//
// The cleanup runs in a goroutine because callers hold goroutine-local
// resources (stdin/stdout pipes) that the underlying ssh.Session.Close()
// closes synchronously; doing the close inline from pumpOutput would
// deadlock against the very read that just failed.
func (s *SSHSession) markInactive() {
	s.mu.Lock()
	was := s.active
	s.active = false
	s.mu.Unlock()
	if was && s.manager != nil {
		go s.manager.reapSession(s.id)
	}
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
// shell using the default terminal size. Returns a session ID that can be
// used with the other manager methods.
func (m *SSHManager) Connect(alias string) (string, error) {
	return m.ConnectWithSize(alias, defaultTermWidth, defaultTermHeight)
}

// clampTermSize clamps a requested terminal size to the configured min/max
// bounds, substituting the default when the caller passes a non-positive
// value. This keeps clients from accidentally requesting a 0x0 terminal
// (which the vt emulator rejects) or a multi-thousand-cell screen that
// would balloon every screen render.
func clampTermSize(w, h int) (int, int) {
	if w <= 0 {
		w = defaultTermWidth
	}
	if h <= 0 {
		h = defaultTermHeight
	}
	if w < minTermWidth {
		w = minTermWidth
	}
	if w > maxTermWidth {
		w = maxTermWidth
	}
	if h < minTermHeight {
		h = minTermHeight
	}
	if h > maxTermHeight {
		h = maxTermHeight
	}
	return w, h
}

// ConnectWithSize is like Connect but lets the caller pick the initial PTY
// and emulator dimensions. Non-positive values fall back to the defaults,
// and out-of-range values are clamped to [minTermWidth..maxTermWidth] x
// [minTermHeight..maxTermHeight].
func (m *SSHManager) ConnectWithSize(alias string, width, height int) (string, error) {
	if !m.isAllowed(alias) {
		return "", fmt.Errorf("%w: %q", ErrHostNotAllowed, alias)
	}

	width, height = clampTermSize(width, height)

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

	if err := session.RequestPty(defaultTermType, height, width, ssh.TerminalModes{}); err != nil {
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

	emulator := vt.NewSafeEmulator(width, height)
	sessionID := fmt.Sprintf("%s@%s:%s-%d", hc.User, hc.Hostname, hc.Port, time.Now().Unix())

	ctx, cancel := context.WithCancel(context.Background())
	sshSession := &SSHSession{
		id:       sessionID,
		manager:  m,
		client:   client,
		session:  session,
		stdin:    stdin,
		emulator: emulator,
		active:   true,
		cancel:   cancel,
		width:    width,
		height:   height,
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
// read error. Each chunk is also fed to the exec watcher tap when one is
// installed, so ssh_exec can detect completion without altering the bytes the
// emulator sees.
func (s *SSHSession) pumpOutput(stdout io.Reader) {
	buf := make([]byte, readBufferSize)
	for s.isActive() {
		n, err := stdout.Read(buf)
		if err != nil {
			s.markInactive()
			return
		}
		_, _ = s.emulator.Write(buf[:n])
		s.feedTap(buf[:n])
	}
}

// feedTap forwards a chunk to the installed exec watcher, if any. It snapshots
// the tap pointer under tapMu so a concurrent uninstall can't race the write.
func (s *SSHSession) feedTap(p []byte) {
	s.tapMu.Lock()
	w := s.tap
	s.tapMu.Unlock()
	if w != nil {
		w.write(p)
	}
}

// execInFlight reports whether an ssh_exec call is currently blocked waiting
// for its command in this session's shell. While true, SendCommand is refused
// so a second command line can't be interleaved into the shell mid-exec and
// pollute the captured output. SendKeys is intentionally NOT gated by this, so
// the caller can still send Ctrl-C to interrupt the running command.
func (s *SSHSession) execInFlight() bool {
	s.tapMu.Lock()
	defer s.tapMu.Unlock()
	return s.tap != nil
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
			if err := s.writeStdin(buf[:n]); err != nil {
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
			_ = s.writeStdin([]byte{0})
		}
	}
}

// Resize changes the PTY and emulator dimensions for an existing session,
// matching what tput/`stty rows`/`stty cols` would report after a window
// resize. The remote shell receives a SIGWINCH so full-screen apps redraw
// at the new size. Out-of-range values are clamped to the same bounds as
// ConnectWithSize.
func (m *SSHManager) Resize(sessionID string, width, height int) (int, int, error) {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return 0, 0, err
	}

	width, height = clampTermSize(width, height)

	if err := session.session.WindowChange(height, width); err != nil {
		return 0, 0, fmt.Errorf("failed to resize pty: %w", err)
	}
	session.emulator.Resize(width, height)

	session.mu.Lock()
	session.width = width
	session.height = height
	session.mu.Unlock()

	return width, height, nil
}

// SendCommand writes a command to the session's stdin and returns the
// rendered screen content after a short delay to let the host respond.
func (m *SSHManager) SendCommand(sessionID, command string) (string, error) {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return "", err
	}

	if session.execInFlight() {
		return "", ErrExecInFlight
	}

	if err := session.writeStdin([]byte(command + "\n")); err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	time.Sleep(commandDelay)

	screen := renderScreen(session.emulator)

	return screen, nil
}

// ExecResult is the outcome of an ssh_exec call.
type ExecResult struct {
	// Completed is true when the command finished before the timeout.
	Completed bool
	// ExitCode is the command's exit status, valid only when Completed.
	ExitCode int
	// Output is the command's plain-text (ANSI-stripped) stdout+stderr,
	// valid only when Completed.
	Output string
	// Screen is the rendered terminal, populated only on timeout so the
	// caller can see what the command is stuck on.
	Screen string
	// Duration is how long the call waited.
	Duration time.Duration
}

// Exec runs a single command in the session's existing shell and blocks until
// it completes or timeout elapses. Unlike SendCommand it returns clean output
// and an exit code, by wrapping the command with begin/end sentinels and
// watching the raw stdout stream for them.
//
// On timeout the command is NOT killed — it keeps running in the shell, and
// the rendered screen is returned so the caller can decide whether to wait
// longer (call again / GetScreen), interrupt it (SendKeys Ctrl-C), or move on.
// This is the key safety property: a runaway or interactive command can never
// block the server, yet its state remains inspectable in the same emulator.
//
// Only one Exec may be in flight per session; concurrent calls return an
// error. Multiline commands are rejected — use SendCommand for those.
func (m *SSHManager) Exec(sessionID, command string, timeout time.Duration) (ExecResult, error) {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return ExecResult{}, err
	}

	if strings.ContainsAny(command, "\n\r") {
		return ExecResult{}, ErrMultilineExec
	}
	if strings.Contains(command, execMarkerPrefix) {
		return ExecResult{}, ErrReservedMarker
	}
	if timeout <= 0 {
		timeout = defaultExecTimeout
	}
	if timeout > maxExecTimeout {
		timeout = maxExecTimeout
	}

	markers := newExecMarkers()
	watcher := newExecWatcher(markers)

	session.tapMu.Lock()
	if session.tap != nil {
		session.tapMu.Unlock()
		return ExecResult{}, ErrExecInFlight
	}
	session.tap = watcher
	session.tapMu.Unlock()

	defer func() {
		session.tapMu.Lock()
		session.tap = nil
		session.tapMu.Unlock()
	}()

	start := time.Now()
	if err := session.writeStdin([]byte(markers.wrap(command))); err != nil {
		return ExecResult{}, fmt.Errorf("failed to send command: %w", err)
	}

	select {
	case code := <-watcher.done:
		raw, ok := watcher.output()
		out := ""
		if ok {
			out = strings.Trim(stripANSI(string(raw)), "\n")
		}
		return ExecResult{
			Completed: true,
			ExitCode:  code,
			Output:    out,
			Duration:  time.Since(start),
		}, nil
	case <-time.After(timeout):
		return ExecResult{
			Completed: false,
			Screen:    renderScreen(session.emulator),
			Duration:  time.Since(start),
		}, nil
	}
}

// SendKeys writes raw bytes to the shell's stdin with no trailing newline,
// for control characters (Ctrl-C = "\x03", Ctrl-D = "\x04"), escape, arrow
// keys, and TUI key prefixes. To run a line, use Exec or SendCommand instead.
func (m *SSHManager) SendKeys(sessionID, keys string) error {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return err
	}
	if err := session.writeStdin([]byte(keys)); err != nil {
		return fmt.Errorf("failed to send keys: %w", err)
	}
	return nil
}

// ClearScreen resets the local emulator buffer (screen + scrollback) without
// sending anything to the remote shell. Useful before a SendCommand read when
// stale scrollback would otherwise confuse parsing. The remote shell's state
// is untouched.
func (m *SSHManager) ClearScreen(sessionID string) error {
	session, err := m.requireActive(sessionID)
	if err != nil {
		return err
	}
	// ED(2) clear screen, ED(3) clear scrollback, CUP home.
	_, _ = session.emulator.Write([]byte("\x1b[2J\x1b[3J\x1b[H"))
	return nil
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

	screen := renderScreen(session.emulator)

	return screen, nil
}

// CloseSession tears down the session, closing all underlying resources.
func (m *SSHManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	session, exists := m.sessions[sessionID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	closeSessionResources(session)
	return nil
}

// reapSession is called asynchronously by the session's own background
// goroutines when they detect the remote end is gone (EOF on stdout,
// keepalive timeout, etc). It removes the entry from the manager map and
// releases the underlying network resources. It is a no-op if the session
// was already closed by the user via CloseSession.
func (m *SSHManager) reapSession(sessionID string) {
	m.mu.Lock()
	session, exists := m.sessions[sessionID]
	if !exists {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()

	closeSessionResources(session)
}

// closeSessionResources is the shared teardown path used by both an explicit
// CloseSession call and the background reaper. It must be safe to call on a
// session whose underlying transport is already gone, which is why each
// close is swallowed.
func closeSessionResources(session *SSHSession) {
	if session.cancel != nil {
		session.cancel()
	}
	session.mu.Lock()
	session.active = false
	session.mu.Unlock()

	if session.stdin != nil {
		_ = session.stdin.Close()
	}
	if session.session != nil {
		_ = session.session.Close()
	}
	if session.client != nil {
		_ = session.client.Close()
	}
}

// SessionInfo is a snapshot of a session's identity and live state used by
// ListSessions so callers can see at a glance whether a session is still
// usable instead of discovering it via an ErrSessionInactive on the next
// command.
type SessionInfo struct {
	ID     string
	Active bool
	Width  int
	Height int
}

// ListSessions returns a snapshot of every known session along with its
// current active state. Inactive entries are normally reaped automatically
// when their goroutines detect a dead remote, but a session can briefly
// appear here as Active=false in the window between detection and reap.
func (m *SSHManager) ListSessions() []SessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]SessionInfo, 0, len(m.sessions))
	for id, s := range m.sessions {
		s.mu.RLock()
		info := SessionInfo{ID: id, Active: s.active, Width: s.width, Height: s.height}
		s.mu.RUnlock()
		sessions = append(sessions, info)
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
