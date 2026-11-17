//go:build !windows

package main

import "net"

// dialAuthSock connects to the SSH agent socket identified by SSH_AUTH_SOCK.
// On non-Windows platforms this is a Unix domain socket.
func dialAuthSock(sock string) (net.Conn, error) {
	return net.Dial("unix", sock)
}