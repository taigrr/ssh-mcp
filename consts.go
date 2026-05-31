package main

import "time"

const (
	defaultPort       = "22"
	defaultTermType   = "xterm-256color"
	termWidth         = 80
	termHeight        = 24
	commandDelay      = 3500 * time.Millisecond
	screenDelay       = 2500 * time.Millisecond
	connectionTimeout = 30 * time.Second
	keepaliveInterval = 30 * time.Second
	readBufferSize    = 4096
	keepaliveRequest  = "keepalive@openssh.com"
	ansiReset         = "\x1b[0m"
	version           = "2.0.0"
)
