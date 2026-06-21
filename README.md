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

| Tool                | Description                                         |
| ------------------- | --------------------------------------------------- |
| `ssh_connect`       | Connect to an allowed host by alias                 |
| `ssh_send_command`  | Send a command to a session and get screen output   |
| `ssh_get_screen`    | Get the current terminal screen content             |
| `ssh_upload`        | Upload a local file or directory via SFTP           |
| `ssh_download`      | Download a remote file or directory via SFTP        |
| `ssh_list_sessions` | List all active sessions                            |
| `ssh_list_hosts`    | List allowed hosts with resolved connection details |
| `ssh_close_session` | Close a session                                     |

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
