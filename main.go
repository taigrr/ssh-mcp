package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/fang/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	sshconfig "github.com/kevinburke/ssh_config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/taigrr/jety"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	defaultPort       = "22"
	defaultTermType   = "xterm-256color"
	termWidth         = 80
	termHeight        = 24
	commandDelay      = 3500 * time.Millisecond
	screenDelay       = 2500 * time.Millisecond
	connectionTimeout = 30 * time.Second
	keepaliveInterval = 30 * time.Second
	keepaliveTimeout  = 15 * time.Second
	readBufferSize    = 4096
	version           = "2.0.0"
)

// HostConfig holds resolved SSH connection details for a host alias.
type HostConfig struct {
	Hostname string
	Port     string
	User     string
	KeyPath  string
	Password string
}

// SSHSession represents a persistent SSH connection with a virtual terminal.
type SSHSession struct {
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	emulator *vt.Emulator
	mu       sync.RWMutex
	active   bool
}

// SSHManager manages SSH sessions and host access control.
type SSHManager struct {
	sessions     map[string]*SSHSession
	allowedHosts []string
	mu           sync.RWMutex
}

func NewSSHManager(allowedHosts []string) *SSHManager {
	return &SSHManager{
		sessions:     make(map[string]*SSHSession),
		allowedHosts: allowedHosts,
	}
}

func resolveHostConfig(alias string) HostConfig {
	hc := HostConfig{
		Hostname: sshconfig.Get(alias, "HostName"),
		Port:     sshconfig.Get(alias, "Port"),
		User:     sshconfig.Get(alias, "User"),
		KeyPath:  sshconfig.Get(alias, "IdentityFile"),
	}

	if hc.Hostname == "" {
		hc.Hostname = alias
	}
	if hc.Port == "" {
		hc.Port = defaultPort
	}
	if hc.User == "" {
		hc.User = os.Getenv("USER")
	}

	if strings.HasPrefix(hc.KeyPath, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			hc.KeyPath = filepath.Join(home, hc.KeyPath[2:])
		}
	}

	return hc
}

func loadAllowedHosts() []string {
	raw := jety.Get("hosts")
	if raw == nil {
		return nil
	}

	slice, ok := raw.([]any)
	if !ok {
		return nil
	}

	hosts := make([]string, 0, len(slice))
	for _, val := range slice {
		if host, ok := val.(string); ok {
			hosts = append(hosts, host)
		}
	}

	return hosts
}

func loadPrivateKey(keyPath string) (ssh.Signer, error) {
	if keyPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}

		keyPath = filepath.Join(home, ".ssh", "id_ed25519")
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			keyPath = filepath.Join(home, ".ssh", "id_rsa")
		}
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to parse key file %s: %w", keyPath, err)
	}

	return signer, nil
}

func buildAuthMethods(hc HostConfig) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	if hc.KeyPath != "" {
		if signer, err := loadPrivateKey(hc.KeyPath); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	if len(methods) == 0 {
		if signer, err := loadPrivateKey(""); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}

	if hc.Password != "" {
		methods = append(methods, ssh.Password(hc.Password))
	}

	return methods
}

func (m *SSHManager) isAllowed(host string) bool {
	return slices.Contains(m.allowedHosts, host)
}

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

	addr := fmt.Sprintf("%s:%s", hc.Hostname, hc.Port)
	conn, err := net.DialTimeout("tcp", addr, connectionTimeout)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %w", err)
	}

	tcpConn := conn.(*net.TCPConn)
	_ = tcpConn.SetKeepAlive(true)
	_ = tcpConn.SetKeepAlivePeriod(keepaliveInterval)

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

	sshSession := &SSHSession{
		client:   client,
		session:  session,
		stdin:    stdin,
		emulator: emulator,
		active:   true,
	}

	go func() {
		buf := make([]byte, readBufferSize)
		for sshSession.active {
			n, err := stdout.Read(buf)
			if err != nil {
				sshSession.mu.Lock()
				sshSession.active = false
				sshSession.mu.Unlock()
				break
			}
			sshSession.mu.Lock()
			_, _ = emulator.Write(buf[:n])
			sshSession.mu.Unlock()
		}
	}()

	// SSH-level keepalive
	go func() {
		ticker := time.NewTicker(keepaliveInterval)
		defer ticker.Stop()
		for range ticker.C {
			if !sshSession.active {
				return
			}
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				sshSession.mu.Lock()
				sshSession.active = false
				sshSession.mu.Unlock()
				return
			}
		}
	}()

	m.mu.Lock()
	m.sessions[sessionID] = sshSession
	m.mu.Unlock()

	return sessionID, nil
}

func (m *SSHManager) SendCommand(sessionID, command string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	session.mu.RLock()
	active := session.active
	session.mu.RUnlock()
	if !active {
		return "", fmt.Errorf("%w: %s", ErrSessionInactive, sessionID)
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

func (m *SSHManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
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

func (m *SSHManager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		sessions = append(sessions, id)
	}

	return sessions
}

func (m *SSHManager) ListHosts() []string {
	results := make([]string, 0, len(m.allowedHosts))
	for _, alias := range m.allowedHosts {
		hc := resolveHostConfig(alias)
		results = append(results, fmt.Sprintf("%s: %s@%s:%s", alias, hc.User, hc.Hostname, hc.Port))
	}

	return results
}

func renderScreen(emulator *vt.Emulator) string {
	rows := make([]string, emulator.Height())
	for y := range emulator.Height() {
		rows[y] = renderLine(emulator, y)
	}

	return strings.Join(rows, "\n")
}

func renderLine(emulator *vt.Emulator, y int) string {
	var builder strings.Builder
	var lastStyle uv.Style

	for x := range emulator.Width() {
		cell := emulator.CellAt(x, y)
		if cell == nil {
			builder.WriteByte(' ')
			continue
		}

		if x == 0 || !cell.Style.Equal(&lastStyle) {
			builder.WriteString(cell.Style.String())
			lastStyle = cell.Style
		}

		if cell.Content == "" {
			builder.WriteByte(' ')
		} else {
			builder.WriteString(cell.Content)
		}
	}

	if builder.Len() > 0 {
		builder.WriteString("\x1b[0m")
	}

	return builder.String()
}

type connectArgs struct {
	Host string `json:"host" jsonschema:"SSH host alias from allowed hosts list (matches ~/.ssh/config Host entries)"`
}

type sessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
}

type commandArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Command   string `json:"command" jsonschema:"Command to send"`
}

type uploadArgs struct {
	SessionID  string `json:"session_id" jsonschema:"SSH session ID"`
	LocalPath  string `json:"local_path" jsonschema:"Local file or directory path to upload"`
	RemotePath string `json:"remote_path" jsonschema:"Destination path on the remote host"`
}

type downloadArgs struct {
	SessionID  string `json:"session_id" jsonschema:"SSH session ID"`
	RemotePath string `json:"remote_path" jsonschema:"Remote file or directory path to download"`
	LocalPath  string `json:"local_path" jsonschema:"Destination path on the local host"`
}

func run(allowedHosts []string) {
	mgr := NewSSHManager(allowedHosts)

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ssh-mcp",
		Version: version,
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_connect",
		Description: "Connect to an allowed SSH host by alias (resolved via ~/.ssh/config)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args connectArgs) (*mcp.CallToolResult, any, error) {
		sessionID, err := mgr.Connect(args.Host)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to connect: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Connected successfully. Session ID: %s", sessionID)}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_send_command",
		Description: "Send a command to an SSH session and get the screen output",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args commandArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.SendCommand(args.SessionID, args.Command)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to send command: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: screen}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_get_screen",
		Description: "Get the current screen content of an SSH session",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.GetScreen(args.SessionID)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to get screen: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: screen}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_list_sessions",
		Description: "List all active SSH sessions",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		sessions := mgr.ListSessions()
		var text string
		if len(sessions) == 0 {
			text = "No active SSH sessions"
		} else {
			text = "Active SSH sessions:\n" + strings.Join(sessions, "\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_list_hosts",
		Description: "List all allowed SSH hosts with resolved connection details",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		hosts := mgr.ListHosts()
		var text string
		if len(hosts) == 0 {
			text = "No configured hosts"
		} else {
			text = "Available hosts:\n" + strings.Join(hosts, "\n")
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_upload",
		Description: "Upload a local file or directory to a remote host via SFTP (recursive for directories)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args uploadArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.Upload(args.SessionID, args.LocalPath, args.RemotePath)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to upload: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_download",
		Description: "Download a remote file or directory from a remote host via SFTP (recursive for directories)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args downloadArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.Download(args.SessionID, args.RemotePath, args.LocalPath)
		if err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to download: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_close_session",
		Description: "Close an SSH session",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		if err := mgr.CloseSession(args.SessionID); err != nil {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Failed to close session: %v", err)}},
				IsError: true,
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Session %s closed successfully", args.SessionID)}},
		}, nil, nil
	})

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func main() {
	var allowedHostsFlag string

	cmd := &cobra.Command{
		Use:   "ssh-mcp",
		Short: "SSH MCP server providing remote shell access via Model Context Protocol",
		RunE: func(c *cobra.Command, args []string) error {
			var hosts []string

			if allowedHostsFlag != "" {
				for host := range strings.SplitSeq(allowedHostsFlag, ",") {
					host = strings.TrimSpace(host)
					if host != "" {
						hosts = append(hosts, host)
					}
				}
			} else {
				jety.SetConfigFile("config.json")
				_ = jety.SetConfigType("json")
				if err := jety.ReadInConfig(); err != nil {
					log.Printf("Warning: config file not found: %v", err)
				}
				hosts = loadAllowedHosts()
			}

			run(hosts)
			return nil
		},
	}

	cmd.Flags().StringVar(&allowedHostsFlag, "allowed-hosts", "", "Comma-separated list of allowed SSH host aliases")

	if err := fang.Execute(context.Background(), cmd); err != nil {
		os.Exit(1)
	}
}
