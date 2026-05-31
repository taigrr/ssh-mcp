package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool names exposed to MCP clients.
const (
	toolConnect      = "ssh_connect"
	toolSendCommand  = "ssh_send_command"
	toolGetScreen    = "ssh_get_screen"
	toolListSessions = "ssh_list_sessions"
	toolListHosts    = "ssh_list_hosts"
	toolUpload       = "ssh_upload"
	toolDownload     = "ssh_download"
	toolCloseSession = "ssh_close_session"
)

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

// textResult builds a successful CallToolResult containing only the given
// text.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// errorResult builds an error CallToolResult with the IsError flag set so the
// MCP client can distinguish failures from regular output.
func errorResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

// registerTools wires every MCP tool exposed by the server onto the manager.
func registerTools(server *mcp.Server, mgr *SSHManager) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        toolConnect,
		Description: "Connect to an allowed SSH host by alias (resolved via ~/.ssh/config)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args connectArgs) (*mcp.CallToolResult, any, error) {
		sessionID, err := mgr.Connect(args.Host)
		if err != nil {
			return errorResult("Failed to connect: %v", err), nil, nil
		}
		return textResult(fmt.Sprintf("Connected successfully. Session ID: %s", sessionID)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolSendCommand,
		Description: "Send a command to an SSH session and get the screen output",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args commandArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.SendCommand(args.SessionID, args.Command)
		if err != nil {
			return errorResult("Failed to send command: %v", err), nil, nil
		}
		return textResult(screen), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolGetScreen,
		Description: "Get the current screen content of an SSH session",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.GetScreen(args.SessionID)
		if err != nil {
			return errorResult("Failed to get screen: %v", err), nil, nil
		}
		return textResult(screen), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolListSessions,
		Description: "List all active SSH sessions",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		sessions := mgr.ListSessions()
		if len(sessions) == 0 {
			return textResult("No active SSH sessions"), nil, nil
		}
		return textResult("Active SSH sessions:\n" + strings.Join(sessions, "\n")), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolListHosts,
		Description: "List all allowed SSH hosts with resolved connection details",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		hosts := mgr.ListHosts()
		if len(hosts) == 0 {
			return textResult("No configured hosts"), nil, nil
		}
		return textResult("Available hosts:\n" + strings.Join(hosts, "\n")), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolUpload,
		Description: "Upload a local file or directory to a remote host via SFTP (recursive for directories)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args uploadArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.Upload(args.SessionID, args.LocalPath, args.RemotePath)
		if err != nil {
			return errorResult("Failed to upload: %v", err), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolDownload,
		Description: "Download a remote file or directory from a remote host via SFTP (recursive for directories)",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args downloadArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.Download(args.SessionID, args.RemotePath, args.LocalPath)
		if err != nil {
			return errorResult("Failed to download: %v", err), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolCloseSession,
		Description: "Close an SSH session",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		if err := mgr.CloseSession(args.SessionID); err != nil {
			return errorResult("Failed to close session: %v", err), nil, nil
		}
		return textResult(fmt.Sprintf("Session %s closed successfully", args.SessionID)), nil, nil
	})
}
