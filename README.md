# SSH MCP Server

An MCP (Model Control Protocol) server that provides SSH connectivity with persistent terminal sessions and screen content capture using Charm's VT terminal emulator.

## Features

- **Persistent SSH Sessions**: Establish SSH connections that remain active across multiple commands
- **Terminal Emulation**: Full VT100 terminal emulation using Charm's VT library
- **Screen Content Capture**: Get the current terminal screen content as text
- **Multiple Sessions**: Support for multiple concurrent SSH sessions
- **Environment Configuration**: Load SSH credentials from `.env` file

## Installation

```bash
go build -o ssh-mcp .
```

## Configuration

Create a `.env` file in the same directory as the executable:

```bash
SSH_USER=your_username
SSH_PASSWORD=your_password
```

You can also provide credentials directly when connecting.

## Available Tools

### `ssh_connect`
Connect to an SSH server and establish a persistent session.

**Parameters:**
- `host` (required): SSH host (e.g., "user@hostname:port" or "hostname:port")
- `user` (optional): SSH username (if not in host or .env)
- `password` (optional): SSH password (if not from .env)

**Returns:** Session ID for use with other commands

### `ssh_send_command`
Send a command to an SSH session and get the screen output.

**Parameters:**
- `session_id` (required): SSH session ID from ssh_connect
- `command` (required): Command to send to the SSH session

**Returns:** Current terminal screen content after command execution

### `ssh_get_screen`
Get the current screen content of an SSH session without sending a command.

**Parameters:**
- `session_id` (required): SSH session ID

**Returns:** Current terminal screen content

### `ssh_list_sessions`
List all active SSH sessions.

**Returns:** List of active session IDs

### `ssh_close_session`
Close an SSH session.

**Parameters:**
- `session_id` (required): SSH session ID to close

**Returns:** Confirmation message

## Usage Example

1. **Connect to SSH server:**
   ```json
   {
     "name": "ssh_connect",
     "arguments": {
       "host": "user@example.com:22"
     }
   }
   ```

2. **Send commands:**
   ```json
   {
     "name": "ssh_send_command",
     "arguments": {
       "session_id": "user@example.com:22-1234567890",
       "command": "ls -la"
     }
   }
   ```

3. **Get current screen:**
   ```json
   {
     "name": "ssh_get_screen",
     "arguments": {
       "session_id": "user@example.com:22-1234567890"
     }
   }
   ```

## Running the Server

The server runs as an MCP server using stdio transport:

```bash
./ssh-mcp
```

## Technical Details

- Uses Charm's VT terminal emulator for accurate terminal emulation
- Maintains persistent SSH connections with proper PTY allocation
- Captures terminal screen content including ANSI formatting
- Thread-safe session management
- Automatic session cleanup on connection close

## Security Notes

- SSH connections use `InsecureIgnoreHostKey()` for simplicity - not recommended for production
- Credentials can be stored in `.env` file - ensure proper file permissions
- Sessions remain active until explicitly closed or connection is lost

## Dependencies

- `github.com/charmbracelet/x/vt` - Terminal emulator
- `github.com/charmbracelet/ultraviolet` - Terminal styling
- `github.com/modelcontextprotocol/go-sdk` - MCP protocol implementation
- `golang.org/x/crypto/ssh` - SSH client
- `github.com/joho/godotenv` - Environment file loading