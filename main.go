package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/joho/godotenv"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/crypto/ssh"
)

type SSHSession struct {
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	emulator *vt.Emulator
	mu       sync.RWMutex
	active   bool
}

type SSHManager struct {
	sessions map[string]*SSHSession
	mu       sync.RWMutex
}

func NewSSHManager() *SSHManager {
	return &SSHManager{
		sessions: make(map[string]*SSHSession),
	}
}

func (m *SSHManager) Connect(host, user, password string) (string, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}

	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return "", fmt.Errorf("failed to create session: %w", err)
	}

	if err := session.RequestPty("xterm-256color", 80, 24, ssh.TerminalModes{}); err != nil {
		session.Close()
		client.Close()
		return "", fmt.Errorf("failed to request pty: %w", err)
	}

	emulator := vt.NewEmulator(80, 24)

	sessionID := fmt.Sprintf("%s@%s-%d", user, host, time.Now().Unix())

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

	sshSession := &SSHSession{
		client:   client,
		session:  session,
		stdin:    stdin,
		emulator: emulator,
		active:   true,
	}

	go func() {
		buf := make([]byte, 4096)
		for sshSession.active {
			n, err := stdout.Read(buf)
			if err != nil {
				break
			}
			sshSession.mu.Lock()
			_, _ = emulator.Write(buf[:n])
			sshSession.mu.Unlock()
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
		return "", fmt.Errorf("session %s not found", sessionID)
	}

	if !session.active {
		return "", fmt.Errorf("session %s is not active", sessionID)
	}

	_, err := session.stdin.Write([]byte(command + "\n"))
	if err != nil {
		return "", fmt.Errorf("failed to send command: %w", err)
	}

	time.Sleep(3500 * time.Millisecond)

	session.mu.RLock()
	screen := m.renderScreen(session.emulator)
	session.mu.RUnlock()

	return screen, nil
}

func (m *SSHManager) GetScreen(sessionID string) (string, error) {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("session %s not found", sessionID)
	}

	time.Sleep(2500 * time.Millisecond)
	session.mu.RLock()
	screen := m.renderScreen(session.emulator)
	session.mu.RUnlock()

	return screen, nil
}

func (m *SSHManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	session.active = false
	session.session.Close()
	session.client.Close()
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

func (m *SSHManager) renderScreen(emulator *vt.Emulator) string {
	rows := make([]string, emulator.Height())

	for y := 0; y < emulator.Height(); y++ {
		rows[y] = m.renderLine(emulator, y)
	}

	return strings.Join(rows, "\n")
}

func (m *SSHManager) renderLine(emulator *vt.Emulator, y int) string {
	var result string
	var lastStyle uv.Style

	for x := 0; x < emulator.Width(); x++ {
		cell := emulator.CellAt(x, y)
		if cell == nil {
			result += " "
			continue
		}

		if x == 0 || !cell.Style.Equal(&lastStyle) {
			result += cell.Style.String()
			lastStyle = cell.Style
		}

		if cell.Content == "" {
			result += " "
		} else {
			result += cell.Content
		}
	}

	if result != "" {
		result += "\x1b[0m"
	}

	return result
}

type connectArgs struct {
	Host     string `json:"host" jsonschema:"SSH host (e.g., hostname:port)"`
	User     string `json:"user,omitempty" jsonschema:"SSH username (if not from env)"`
	Password string `json:"password,omitempty" jsonschema:"SSH password (if not from env)"`
}

type sessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
}

type commandArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Command   string `json:"command" jsonschema:"Command to send"`
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	mgr := NewSSHManager()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ssh-mcp",
		Version: "1.0.0",
	}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "ssh_connect",
		Description: "Connect to an SSH server and establish a persistent session",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args connectArgs) (*mcp.CallToolResult, any, error) {
		user := args.User
		password := args.Password
		if user == "" {
			user = os.Getenv("SSH_USER")
		}
		if password == "" {
			password = os.Getenv("SSH_PASSWORD")
		}
		if user == "" || password == "" {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "SSH_USER and SSH_PASSWORD must be provided via arguments or .env file"}},
				IsError: true,
			}, nil, nil
		}

		sessionID, err := mgr.Connect(args.Host, user, password)
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
			text = fmt.Sprintf("Active SSH sessions:\n%v", sessions)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
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
