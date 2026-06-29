# ssh-mcp

MCP server providing persistent SSH sessions for AI coding agents via the [Model Context Protocol](https://modelcontextprotocol.io).

## The Problem

AI coding agents can't interact with remote machines. You need to manually SSH in, run commands, and paste output back. For fleet management, server debugging, or remote development, this kills the feedback loop.

## The Solution

**ssh-mcp** gives your AI agent direct SSH access to an allowed set of hosts. It resolves connections from `~/.ssh/config`, authenticates via ssh-agent/gpg-agent, and exposes a virtual terminal so the agent sees exactly what you'd see.

```bash
# Start with allowed hosts
ssh-mcp --allowed-hosts="web1,db1,staging"
```

## Features

- **SSH config integration** — Resolves hosts from `~/.ssh/config` (HostName, Port, User, IdentityFile)
- **Agent forwarding** — Authenticates via `ssh-agent` or `gpg-agent` (SSH_AUTH_SOCK)
- **Virtual terminal** — Full PTY emulation with ANSI rendering via [charmbracelet/x/vt](https://github.com/charmbracelet/x)
- **File transfer** — Bidirectional file and directory transfers via SFTP (recursive)
- **Allowlist-only access** — Only explicitly permitted hosts can be connected to
- **Session management** — Multiple concurrent sessions with independent state
- **Zero config** — Works out of the box if your `~/.ssh/config` is set up

## Installation

```bash
go install github.com/taigrr/ssh-mcp@latest
```

## Requirements

- Go >= **1.24** (for installation)
- `~/.ssh/config` entries for your hosts
- One of:
  - `ssh-agent` or `gpg-agent` running (recommended)
  - SSH private key at `~/.ssh/id_ed25519` or `~/.ssh/id_rsa`

## Quick Start

1. Ensure your hosts are in `~/.ssh/config`:

```
Host my-desktop
    HostName 10.0.1.50
    User tai
```

2. Run with allowed hosts:

```bash
ssh-mcp --allowed-hosts="my-desktop"
```

3. Or use a config file (`config.json` in working directory):

```json
{
  "hosts": ["my-desktop", "web1", "db1"]
}
```

## 🔌 MCP Tools

| Tool                | Description                                                       |
| ------------------- | ----------------------------------------------------------------- |
| `ssh_connect`       | Connect to an allowed host by alias (optional `width`, `height`)  |
| `ssh_exec`          | Run a command and **block** until it finishes or times out        |
| `ssh_send_command`  | Send a command (non-blocking); for TUIs / fire-and-forget         |
| `ssh_send_keys`     | Send raw bytes with no newline (Ctrl-C, Esc, arrows, TUI keys)    |
| `ssh_get_screen`    | Get the current terminal screen content                           |
| `ssh_clear_screen`  | Reset the local emulator screen + scrollback                      |
| `ssh_resize`        | Resize a session's terminal (sends SIGWINCH to the remote shell)  |
| `ssh_read_file`     | Read a remote file over SFTP (clean bytes, no shell)              |
| `ssh_write_file`    | Write a remote file over SFTP (no quoting pitfalls)               |
| `ssh_edit_file`     | Exact find-and-replace edit of a remote file (read it first)      |
| `ssh_upload`        | Upload a local file or directory via SFTP                         |
| `ssh_download`      | Download a remote file or directory via SFTP                      |
| `ssh_list_sessions` | List all sessions with active state and size                      |
| `ssh_list_hosts`    | List allowed hosts with resolved connection details               |
| `ssh_close_session` | Close a session                                                   |

### Choosing between `ssh_exec` and `ssh_send_command`

Both run in the **same persistent shell** (cwd and environment carry over), but
they have opposite blocking semantics:

- **`ssh_exec`** is what you want for almost everything that should *finish*:
  builds, `git`, installs, queries, scripts. It wraps the command with internal
  sentinels, **blocks until it completes or `timeout_ms` elapses**, and returns
  clean output (ANSI/prompt stripped) plus the exit code. On timeout the command
  is **not killed** — it keeps running, and you get a screen snapshot so you can
  see what it is stuck on, then poll (`ssh_get_screen`), interrupt
  (`ssh_send_keys` Ctrl-C), or wait longer.
- **`ssh_send_command`** is non-blocking and exists for **interactive TUIs**
  (vim, less, htop, tmux, REPLs) and fire-and-forget launches. It returns the
  screen after a short settle delay regardless of completion.

The emulator backing every session is a real virtual terminal, so even when an
`ssh_exec` command times out or a TUI is open, the session stays responsive and
its state is always inspectable via `ssh_get_screen` — nothing can hang the
server.

`ssh_exec`'s `timeout_ms` defaults to **60s** (matching Crush's bash tool, which
auto-backgrounds a synchronous command after 60s) and is capped at 10m. A
timeout is **not an error**: the tool returns a normal result noting the command
is still running, plus the current screen, so the model just keeps going with
`ssh_get_screen` / `ssh_send_keys`.

### One shell per session: concurrency

Each session is a single interactive shell with one stdin and one stdout. The
server keeps that consistent under concurrent tool calls:

- **Byte level** — all writes to the shell go through one serialized path, so
  bytes from different calls never interleave mid-sequence. Output is captured
  by a concurrency-safe terminal emulator.
- **Two `ssh_exec` at once (same session)** — rejected. Only one `ssh_exec` may
  be waiting in a session's shell at a time; the loser gets an
  "already executing" error. This prevents two wrapped commands from racing into
  the same shell and scrambling each other's captured output.
- **`ssh_send_command` during an `ssh_exec`** — rejected for the same reason
  (it would inject a second command line mid-exec).
- **`ssh_send_keys` during an `ssh_exec`** — **allowed on purpose.** This is how
  you send Ctrl-C to interrupt a command that timed out and is still running.
- **Different sessions** — fully independent; run them in parallel freely.

If you fire several commands at the same session simultaneously, expect all but
one to come back with an "already executing" error rather than corrupting the
shell. Sequence them, or use separate sessions.

### Terminal size

Sessions default to a **200x50** virtual terminal, large enough for typical
`ls`, `tail`, `ps`, log, and progress-bar output without triggering line wraps
that pollute scrollback. Clients can override the initial size via `width` /
`height` on `ssh_connect`, or resize a live session with `ssh_resize`. Values
are clamped to `[20, 500]` columns x `[5, 200]` rows.

### Session lifecycle

`ssh_list_sessions` returns every session the server knows about along with
its current state (`[active]` or `[inactive]`) and current terminal size.
Background goroutines watch each session's stdout and keepalive channel; when
they detect that the remote end has gone away (EOF, keepalive timeout) the
session is automatically marked inactive **and removed from the manager**, so
a follow-up `ssh_list_sessions` won't keep advertising a dead entry. Only
sessions reported as `[active]` accept `ssh_send_command` / `ssh_get_screen`
/ `ssh_resize`; attempts on inactive entries return `ErrSessionInactive`.

### `ssh_send_command` is non-blocking

`ssh_send_command` writes the command + newline to the remote shell's stdin,
waits a short settle delay (~3.5s) for the terminal to redraw, then returns
the screen as-is. It does **not** wait for the command to finish. This is
intentional: it keeps TUIs (vim, htop, tmux, less) interactive across calls.

Practical consequences for clients (especially LLM agents):

- A long-running command (`sleep 60`, `make`, training jobs, package installs)
  keeps running on the remote host after the call returns.
- To wait for completion, sleep on **your** side (your own timer / bash tool
  outside this MCP), then call `ssh_get_screen` to read fresh output. Do
  **not** prefix `sleep N;` to the SSH command expecting it to block here.
- Sending a second command before the first finishes queues bytes in the
  remote shell's stdin and produces garbled, interleaved output. Poll with
  `ssh_get_screen` until you see the prompt first.
- For output you'd rather not stream through the screen, redirect to a file
  (`cmd > /tmp/out 2>&1 &`) and read it back with `cat`.

## ⚙️ Crush Configuration

Add to your project's `crush.json` or `.crush.json`:

```json
{
  "mcp": {
    "ssh": {
      "command": "ssh-mcp",
      "args": ["--allowed-hosts=my-desktop,staging"],
      "type": "stdio"
    }
  }
}
```

## 🤖 Claude Configuration

### Claude Code

Register the server with the Claude Code CLI (writes to your MCP config):

```bash
claude mcp add ssh -- ssh-mcp --allowed-hosts=my-desktop,staging
```

Or add it manually to `~/.claude.json` (global) or `.mcp.json` (project):

```json
{
  "mcpServers": {
    "ssh": {
      "command": "ssh-mcp",
      "args": ["--allowed-hosts=my-desktop,staging"],
      "type": "stdio"
    }
  }
}
```

### Claude Desktop

Add to your `claude_desktop_config.json`:

- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "ssh": {
      "command": "ssh-mcp",
      "args": ["--allowed-hosts=my-desktop,staging"]
    }
  }
}
```

If `ssh-mcp` is not on your `PATH`, use the absolute path (e.g. the output of `which ssh-mcp`, typically `~/go/bin/ssh-mcp`) as the `command`. Because the server authenticates via `ssh-agent`, ensure `SSH_AUTH_SOCK` is available to the Claude process; you can pass it explicitly:

```json
{
  "mcpServers": {
    "ssh": {
      "command": "/Users/you/go/bin/ssh-mcp",
      "args": ["--allowed-hosts=my-desktop,staging"],
      "env": {
        "SSH_AUTH_SOCK": "/path/to/ssh-agent.sock"
      }
    }
  }
}
```

Restart Claude Desktop after editing the config.

## Flags

| Flag              | Description                                      |
| ----------------- | ------------------------------------------------ |
| `--allowed-hosts` | Comma-separated list of allowed SSH host aliases |

If `--allowed-hosts` is not provided, falls back to `config.json` in the working directory.

## How It Works

1. **Resolve** — Looks up host alias in `~/.ssh/config` for connection details
2. **Authenticate** — Tries ssh-agent → key file → password (in order)
3. **Connect** — Opens a PTY session with `xterm-256color` terminal
4. **Emulate** — Captures output in a virtual terminal emulator
5. **Expose** — Returns rendered screen content (with ANSI) to the MCP client

## Architecture

```
┌──────────────────────────────────────────────────┐
│                   ssh-mcp                        │
│                                                  │
│  ~/.ssh/config ──► Host Resolution               │
│                                                  │
│  SSH_AUTH_SOCK ──► Authentication                │
│                                                  │
│  Sessions:                                       │
│  ├─ tai@10.0.1.50:22 (PTY + VT)                  │
│  ├─ admin@web1.example.com:22  (PTY + VT)        │
│  └─ ...                                          │
│                                                  │
│  MCP Tools ◄──► stdio transport ◄──► AI Agent    │
└──────────────────────────────────────────────────┘
```

## Development

```bash
# Build
go build -o ssh-mcp .

# Test
go test ./...

# Lint
staticcheck ./...
```

## Related Projects

- [Crush](https://github.com/charmbracelet/crush) — Charm's AI coding agent
- [charmbracelet/x/vt](https://github.com/charmbracelet/x) — Virtual terminal emulator
- [neocrush](https://github.com/taigrr/neocrush) — LSP/MCP bridge for Crush + Neovim

## License

[0BSD](LICENSE) © [Tai Groot](https://github.com/taigrr)
