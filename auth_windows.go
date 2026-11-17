//go:build windows

package main

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// dialAuthSock connects to the SSH agent socket identified by SSH_AUTH_SOCK.
// On Windows the value may be a Unix domain socket (Cygwin/MSYS) or a named
// pipe, so both transports are attempted.
func dialAuthSock(sock string) (net.Conn, error) {
	// Attempt Unix sockets first for environments such as Cygwin/MSYS.
	if conn, err := net.Dial("unix", sock); err == nil {
		return conn, nil
	}

	return winio.DialPipeContext(context.Background(), sock)
}