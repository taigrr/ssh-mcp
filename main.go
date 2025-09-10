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
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
			_, err = emulator.Write(buf[:n])
			sshSession.mu.Unlock()
		}
	}()

	sshSession.session = session
	sshSession.client = client

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

// renderScreen converts the VT emulator screen to a string representation
func (m *SSHManager) renderScreen(emulator *vt.Emulator) string {
	rows := make([]string, emulator.Height())

	for y := 0; y < emulator.Height(); y++ {
		rows[y] = m.renderLine(emulator, y)
	}

	return strings.Join(rows, "\n")
}

// renderLine converts a VT screen line to ANSI string
func (m *SSHManager) renderLine(emulator *vt.Emulator, y int) string {
	var result string
	var lastStyle uv.Style

	for x := 0; x < emulator.Width(); x++ {
		cell := emulator.CellAt(x, y)
		if cell == nil {
			result += " "
			continue
		}

		// Only emit style changes when needed
		if x == 0 || !cell.Style.Equal(&lastStyle) {
			result += cell.Style.Sequence()
			lastStyle = cell.Style
		}

		if cell.Content == "" {
			result += " "
		} else {
			result += cell.Content
		}
	}

	// Reset styles at end of line
	if result != "" {
		result += "\x1b[0m"
	}

	return result
}

type ConnectRequest struct {
	Host     string `json:"host" jsonschema:"required" jsonschema_description:"SSH host (e.g., user@hostname:port or hostname:port)"`
	User     string `json:"user,omitempty" jsonschema_description:"SSH username (if not in host)"`
	Password string `json:"password,omitempty" jsonschema_description:"SSH password (if not from .env)"`
}

type SendCommandRequest struct {
	SessionID string `json:"session_id" jsonschema:"required" jsonschema_description:"SSH session ID"`
	Command   string `json:"command" jsonschema:"required" jsonschema_description:"Command to send to the SSH session"`
}

type GetScreenRequest struct {
	SessionID string `json:"session_id" jsonschema:"required" jsonschema_description:"SSH session ID"`
}

type CloseSessionRequest struct {
	SessionID string `json:"session_id" jsonschema:"required" jsonschema_description:"SSH session ID to close"`
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Printf("Warning: .env file not found: %v", err)
	}

	sshManager := NewSSHManager()

	mcpServer := server.NewMCPServer(
		"ssh-mcp",
		"1.0.0",
	)

	connectTool := mcp.NewTool("ssh_connect",
		mcp.WithDescription("Connect to an SSH server and establish a persistent session"),
		mcp.WithString("host", mcp.Required(), mcp.Description("SSH host (e.g., user@hostname:port or hostname:port)")),
		mcp.WithString("user", mcp.Description("SSH username (if not in host)")),
		mcp.WithString("password", mcp.Description("SSH password (if not from .env)")),
	)

	mcpServer.AddTool(connectTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		host, _ := request.Params.Arguments["host"].(string)
		user, _ := request.Params.Arguments["user"].(string)
		password, _ := request.Params.Arguments["password"].(string)

		if user == "" {
			user = os.Getenv("SSH_USER")
		}
		if password == "" {
			password = os.Getenv("SSH_PASSWORD")
		}

		if user == "" || password == "" {
			return mcp.NewToolResultError("SSH_USER and SSH_PASSWORD must be provided via arguments or .env file"), nil
		}

		sessionID, err := sshManager.Connect(host, user, password)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to connect: %v", err)), nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{
				mcp.NewTextContent(fmt.Sprintf("Connected successfully. Session ID: %s", sessionID)),
			},
		}, nil
	})

	sendCommandTool := mcp.NewTool("ssh_send_command",
		mcp.WithDescription("Send a command to an SSH session and get the screen output"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("SSH session ID")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Command to send to the SSH session")),
	)

	mcpServer.AddTool(sendCommandTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, _ := request.Params.Arguments["session_id"].(string)
		command, _ := request.Params.Arguments["command"].(string)

		screen, err := sshManager.SendCommand(sessionID, command)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to send command: %v", err)), nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{
				mcp.NewTextContent(screen),
			},
		}, nil
	})

	getScreenTool := mcp.NewTool("ssh_get_screen",
		mcp.WithDescription("Get the current screen content of an SSH session"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("SSH session ID")),
	)

	mcpServer.AddTool(getScreenTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, _ := request.Params.Arguments["session_id"].(string)

		screen, err := sshManager.GetScreen(sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to get screen: %v", err)), nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{
				mcp.NewTextContent(screen),
			},
		}, nil
	})

	listSessionsTool := mcp.NewTool("ssh_list_sessions",
		mcp.WithDescription("List all active SSH sessions"),
	)

	mcpServer.AddTool(listSessionsTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessions := sshManager.ListSessions()

		var text string
		if len(sessions) == 0 {
			text = "No active SSH sessions"
		} else {
			text = fmt.Sprintf("Active SSH sessions:\n%v", sessions)
		}

		return &mcp.CallToolResult{
			Content: []interface{}{
				mcp.NewTextContent(text),
			},
		}, nil
	})

	closeSessionTool := mcp.NewTool("ssh_close_session",
		mcp.WithDescription("Close an SSH session"),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("SSH session ID to close")),
	)

	mcpServer.AddTool(closeSessionTool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sessionID, _ := request.Params.Arguments["session_id"].(string)

		err := sshManager.CloseSession(sessionID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("Failed to close session: %v", err)), nil
		}

		return &mcp.CallToolResult{
			Content: []interface{}{
				mcp.NewTextContent(fmt.Sprintf("Session %s closed successfully", sessionID)),
			},
		}, nil
	})

	if err := server.ServeStdio(mcpServer); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
