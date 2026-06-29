package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool names exposed to MCP clients.
const (
	toolConnect      = "ssh_connect"
	toolExec         = "ssh_exec"
	toolSendCommand  = "ssh_send_command"
	toolSendKeys     = "ssh_send_keys"
	toolGetScreen    = "ssh_get_screen"
	toolClearScreen  = "ssh_clear_screen"
	toolResize       = "ssh_resize"
	toolListSessions = "ssh_list_sessions"
	toolListHosts    = "ssh_list_hosts"
	toolReadFile     = "ssh_read_file"
	toolWriteFile    = "ssh_write_file"
	toolEditFile     = "ssh_edit_file"
	toolUpload       = "ssh_upload"
	toolDownload     = "ssh_download"
	toolCloseSession = "ssh_close_session"
)

type connectArgs struct {
	Host   string `json:"host" jsonschema:"SSH host alias from allowed hosts list (matches ~/.ssh/config Host entries)"`
	Width  int    `json:"width,omitempty" jsonschema:"Optional initial terminal width in columns. Defaults to 200 if omitted or non-positive. Clamped to [20, 500]."`
	Height int    `json:"height,omitempty" jsonschema:"Optional initial terminal height in rows. Defaults to 50 if omitted or non-positive. Clamped to [5, 200]."`
}

type sessionArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
}

type commandArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Command   string `json:"command" jsonschema:"Command to send. A literal newline is appended so the remote shell executes it. Note that this call is NON-BLOCKING: it returns the screen after only a short settle delay (a few seconds), regardless of whether the command has finished. Long-running commands (e.g. 'sleep 60', 'make build', training jobs) keep running on the remote host after the call returns; do NOT chain a local-style 'sleep N;' before the command expecting it to block here. Instead wait externally (your own sleep/timer outside this MCP) and then call ssh_get_screen to read fresh output."`
}

type execArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Command   string `json:"command" jsonschema:"A single shell command line to run and wait for. Must NOT contain newlines (use ssh_send_command for multi-line/heredoc input). Runs in the same persistent shell as ssh_send_command, so cwd and environment from earlier commands carry over."`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"How long to wait for completion before returning with the command still running. Defaults to 60000ms. Maximum 600000ms (10m)."`
}

type sendKeysArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Keys      string `json:"keys" jsonschema:"Raw bytes to write to the shell with NO trailing newline. Use for control characters and TUI navigation: Ctrl-C is \"\\u0003\", Ctrl-D \"\\u0004\", Escape \"\\u001b\", Enter \"\\r\". To run a command line, use ssh_exec or ssh_send_command instead."`
}

type resizeArgs struct {
	SessionID string `json:"session_id" jsonschema:"SSH session ID"`
	Width     int    `json:"width" jsonschema:"New terminal width in columns. Clamped to [20, 500]."`
	Height    int    `json:"height" jsonschema:"New terminal height in rows. Clamped to [5, 200]."`
}

type readFileArgs struct {
	SessionID  string `json:"session_id" jsonschema:"SSH session ID"`
	RemotePath string `json:"remote_path" jsonschema:"Absolute path of the remote file to read"`
	Offset     int64  `json:"offset,omitempty" jsonschema:"Byte offset to start reading from. Defaults to 0. Use with length to page through large files."`
	Length     int64  `json:"length,omitempty" jsonschema:"Maximum bytes to return. Defaults to and is capped at 262144 (256KiB). If the file has more data beyond what is returned, the response notes it was truncated."`
}

type writeFileArgs struct {
	SessionID  string `json:"session_id" jsonschema:"SSH session ID"`
	RemotePath string `json:"remote_path" jsonschema:"Absolute path of the remote file to write. Parent directories are created as needed; an existing file is overwritten."`
	Content    string `json:"content" jsonschema:"Exact content to write to the file"`
}

type editFileArgs struct {
	SessionID  string `json:"session_id" jsonschema:"SSH session ID"`
	RemotePath string `json:"remote_path" jsonschema:"Absolute path of the remote file to modify"`
	OldString  string `json:"old_string" jsonschema:"The text to replace. Leave empty to create a new file with new_string as its content (the file must not already exist)."`
	NewString  string `json:"new_string" jsonschema:"The text to replace it with. Leave empty to delete old_string from the file."`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"Replace all occurrences of old_string (default false)."`
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
		Name: toolConnect,
		Description: "Connect to an allowed SSH host by alias (resolved via ~/.ssh/config). " +
			"Optional width/height set the initial terminal size; defaults are 200x50. " +
			"The returned session ID is used by ssh_send_command, ssh_get_screen, " +
			"ssh_resize, ssh_upload, ssh_download, and ssh_close_session. " +
			"Note: subsequent ssh_send_command calls are non-blocking -- they do not " +
			"wait for the remote command to finish. See ssh_send_command for details.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args connectArgs) (*mcp.CallToolResult, any, error) {
		sessionID, err := mgr.ConnectWithSize(args.Host, args.Width, args.Height)
		if err != nil {
			return errorResult("Failed to connect: %v", err), nil, nil
		}
		return textResult(fmt.Sprintf("Connected successfully. Session ID: %s", sessionID)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolExec,
		Description: "Run a single shell command and BLOCK until it finishes or the timeout elapses. " +
			"This is the tool you want for almost everything: builds, git, package installs, queries, " +
			"scripts, `cat`-ing a file, checking status -- anything that is supposed to finish. " +
			"It runs in the SAME persistent shell as ssh_send_command, so working directory and " +
			"environment set by earlier commands carry over. " +
			"On success it returns the command's clean output (ANSI stripped, prompt and echo removed) " +
			"and its exit code. " +
			"If the command does not finish within timeout_ms (default 60s), the call returns with " +
			"`completed=false` and a snapshot of the screen so you can see what it is stuck on (a " +
			"prompt? a long build? an interactive pager?). The command is NOT killed -- it keeps " +
			"running in the shell. You can then: call ssh_exec again (e.g. `true`) or ssh_get_screen " +
			"to poll, send Ctrl-C with ssh_send_keys to interrupt it, or just wait. " +
			"Do NOT use ssh_exec to launch interactive full-screen programs (vim, less, htop, top, " +
			"mysql REPL) -- they never 'finish' so they will always time out; use ssh_send_command + " +
			"ssh_send_keys for those. Command must be a single line (no newlines/heredocs).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args execArgs) (*mcp.CallToolResult, any, error) {
		timeout := time.Duration(args.TimeoutMs) * time.Millisecond
		res, err := mgr.Exec(args.SessionID, args.Command, timeout)
		if err != nil {
			return errorResult("Failed to exec: %v", err), nil, nil
		}
		if !res.Completed {
			return textResult(fmt.Sprintf(
				"Command did not complete within %s and is STILL RUNNING in the shell. "+
					"It was not killed. Poll with ssh_get_screen, interrupt with ssh_send_keys "+
					"(Ctrl-C = \"\\u0003\"), or call ssh_exec again with a larger timeout_ms.\n\n"+
					"Current screen:\n%s",
				res.Duration.Round(time.Millisecond), res.Screen)), nil, nil
		}
		return textResult(fmt.Sprintf("exit_code=%d duration=%s\n%s",
			res.ExitCode, res.Duration.Round(time.Millisecond), res.Output)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolSendCommand,
		Description: "Send a command to an SSH session and return the rendered screen. " +
			"Prefer ssh_exec for commands that should finish -- it blocks, returns clean output, " +
			"and gives you an exit code. Use ssh_send_command only for INTERACTIVE programs (vim, " +
			"less, htop, mysql REPL, tmux) or fire-and-forget launches you do not want to wait for. " +
			"IMPORTANT: this call is NON-BLOCKING with respect to the remote command. " +
			"It writes the command + newline to the shell's stdin, waits a short settle " +
			"period (~3.5s) for the terminal to redraw, then returns the screen as-is " +
			"-- whether or not the command has finished. " +
			"Implications you must respect: " +
			"(1) Long-running commands (sleep, make, training jobs, package installs) " +
			"continue running on the remote host after this call returns. " +
			"(2) To 'wait' for a command, sleep on YOUR side (your own bash/timer tool, " +
			"NOT by prefixing 'sleep N;' to the SSH command) and then call ssh_get_screen. " +
			"(3) Sending another command before the previous one finishes queues input in " +
			"the remote shell's stdin and will produce interleaved/garbled output -- " +
			"wait for completion (poll ssh_get_screen for the prompt) first. " +
			"(4) For commands whose output you don't want streaming through the screen, " +
			"redirect to a file (`cmd > /tmp/out 2>&1 &`) and read it back with ssh_read_file. " +
			"This non-blocking behavior is intentional: it lets TUIs (vim, htop, tmux, " +
			"less) stay interactive across calls.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args commandArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.SendCommand(args.SessionID, args.Command)
		if err != nil {
			return errorResult("Failed to send command: %v", err), nil, nil
		}
		return textResult(screen), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolSendKeys,
		Description: "Send raw bytes to the shell's stdin with NO trailing newline. " +
			"Use this for control characters and TUI navigation that ssh_send_command (which always " +
			"appends a newline) cannot express: interrupt a running command with Ctrl-C (\"\\u0003\"), " +
			"send EOF with Ctrl-D (\"\\u0004\"), press Escape (\"\\u001b\"), arrow keys, or a bare " +
			"Enter (\"\\r\"). To execute a command line, use ssh_exec or ssh_send_command instead. " +
			"After sending keys, read the result with ssh_get_screen.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sendKeysArgs) (*mcp.CallToolResult, any, error) {
		if err := mgr.SendKeys(args.SessionID, args.Keys); err != nil {
			return errorResult("Failed to send keys: %v", err), nil, nil
		}
		return textResult("Keys sent"), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolGetScreen,
		Description: "Get the current rendered screen content of an SSH session. " +
			"Use this to poll for output produced by a previously sent command, since " +
			"ssh_send_command returns immediately after a short settle delay and does " +
			"not wait for long-running commands to finish. Typical pattern: call " +
			"ssh_send_command, sleep on YOUR side for the expected duration, then call " +
			"ssh_get_screen (repeat if needed until you see the shell prompt or the " +
			"expected output). This call also waits a short settle delay (~2.5s) before " +
			"rendering, so consecutive polls are naturally spaced.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		screen, err := mgr.GetScreen(args.SessionID)
		if err != nil {
			return errorResult("Failed to get screen: %v", err), nil, nil
		}
		return textResult(screen), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolClearScreen,
		Description: "Clear the LOCAL terminal emulator's screen and scrollback for a session. " +
			"This resets what ssh_get_screen / ssh_send_command will show, giving you a clean slate " +
			"when accumulated scrollback makes output hard to read. It does NOT send anything to the " +
			"remote shell (no `clear` command, no escape sequences to the host) and does not affect " +
			"the running shell's state -- only the local rendering buffer is reset.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args sessionArgs) (*mcp.CallToolResult, any, error) {
		if err := mgr.ClearScreen(args.SessionID); err != nil {
			return errorResult("Failed to clear screen: %v", err), nil, nil
		}
		return textResult("Screen cleared"), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        toolResize,
		Description: "Resize the terminal for an existing SSH session. Sends SIGWINCH to the remote shell so full-screen apps redraw at the new size. Width is clamped to [20, 500] columns and height to [5, 200] rows.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args resizeArgs) (*mcp.CallToolResult, any, error) {
		w, h, err := mgr.Resize(args.SessionID, args.Width, args.Height)
		if err != nil {
			return errorResult("Failed to resize: %v", err), nil, nil
		}
		return textResult(fmt.Sprintf("Resized session to %dx%d", w, h)), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolListSessions,
		Description: "List every SSH session this server knows about, along with its current state. " +
			"Each entry is rendered as `[active|inactive] <session_id> (<width>x<height>)`. " +
			"Only sessions reported as `active` accept ssh_send_command / ssh_get_screen / ssh_resize calls; " +
			"`inactive` entries are dead remotes pending automatic reap and will return ErrSessionInactive if you try to use them.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		sessions := mgr.ListSessions()
		if len(sessions) == 0 {
			return textResult("No SSH sessions"), nil, nil
		}
		lines := make([]string, 0, len(sessions))
		for _, s := range sessions {
			state := "active"
			if !s.Active {
				state = "inactive"
			}
			lines = append(lines, fmt.Sprintf("[%s] %s (%dx%d)", state, s.ID, s.Width, s.Height))
		}
		return textResult("SSH sessions:\n" + strings.Join(lines, "\n")), nil, nil
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
		Name: toolReadFile,
		Description: "Read a remote file directly over SFTP and return its content as text. " +
			"Prefer this over `cat`-ing through the shell: it returns the exact file bytes with no " +
			"prompt, ANSI, or terminal-wrapping noise, and does not disturb the interactive session. " +
			"Returns up to 256KiB; use offset/length to page through larger files. The response notes " +
			"when output was truncated.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args readFileArgs) (*mcp.CallToolResult, any, error) {
		content, truncated, err := mgr.ReadFile(args.SessionID, args.RemotePath, args.Offset, args.Length)
		if err != nil {
			return errorResult("Failed to read file: %v", err), nil, nil
		}
		if truncated {
			return textResult(content + "\n\n[truncated: more data available; re-read with a larger offset]"), nil, nil
		}
		return textResult(content), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolWriteFile,
		Description: "Write content to a remote file directly over SFTP, creating parent directories " +
			"as needed and overwriting any existing file. Prefer this over here-docs / `echo >` through " +
			"the shell: it writes exact bytes with no quoting or escaping pitfalls and does not disturb " +
			"the interactive session.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args writeFileArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.WriteFile(args.SessionID, args.RemotePath, args.Content)
		if err != nil {
			return errorResult("Failed to write file: %v", err), nil, nil
		}
		return textResult(result), nil, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: toolEditFile,
		Description: "Edit a remote file by exact find-and-replace over SFTP, with the same semantics " +
			"as a local editor: replace `old_string` with `new_string`. " +
			"Leave `old_string` empty to CREATE a new file (it must not already exist); leave " +
			"`new_string` empty to DELETE `old_string`. " +
			"`old_string` must match EXACTLY including whitespace, indentation, and line breaks, and " +
			"must be UNIQUE in the file unless `replace_all` is true -- otherwise the edit is rejected " +
			"so you can add more surrounding context. " +
			"You MUST read the file with ssh_read_file first (a full read); editing is refused " +
			"otherwise, and also if the file changed on the remote since you last read it. " +
			"Line endings are preserved. For whole-file rewrites use ssh_write_file; for renames/moves " +
			"use ssh_exec with mv.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args editFileArgs) (*mcp.CallToolResult, any, error) {
		result, err := mgr.EditFile(args.SessionID, args.RemotePath, args.OldString, args.NewString, args.ReplaceAll)
		if err != nil {
			// Match-failure and guard messages are returned verbatim (no
			// "Failed to edit:" prefix) so they read exactly like the editor.
			return errorResult("%s", err.Error()), nil, nil
		}
		return textResult(result), nil, nil
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
