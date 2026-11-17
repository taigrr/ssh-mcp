//go:build windows

package main

import (
	"context"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
)

// isNamedPipePath reports whether sock refers to a Win32 named pipe in the
// DOS device namespace (for example `\\.\pipe\openssh-ssh-agent`). Such paths
// are not AF_UNIX socket files, so attempting a Unix dial against them is
// always wasted work.
func isNamedPipePath(sock string) bool {
	s := strings.ToLower(sock)
	return strings.HasPrefix(s, `\\.\pipe\`) ||
		(strings.HasPrefix(s, `\\`) && strings.Contains(s, `\pipe\`))
}

// dialAuthSock connects to the SSH agent socket identified by SSH_AUTH_SOCK.
// On Windows the value may be a named pipe (the transport used by the built-in
// OpenSSH agent and most third-party agents) or a Unix domain socket
// (Cygwin/MSYS), so both transports are attempted.
func dialAuthSock(sock string) (net.Conn, error) {
	// Named pipe paths cannot be reached via AF_UNIX, so dial them directly.
	if isNamedPipePath(sock) {
		return winio.DialPipeContext(context.Background(), sock)
	}

	// Attempt Unix sockets first for environments such as Cygwin/MSYS.
	if conn, err := net.Dial("unix", sock); err == nil {
		return conn, nil
	}

	return winio.DialPipeContext(context.Background(), sock)
}
