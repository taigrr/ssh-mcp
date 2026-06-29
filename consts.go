package main

import "time"

const (
	defaultPort     = "22"
	defaultTermType = "xterm-256color"

	// defaultTermWidth/defaultTermHeight are the PTY/emulator dimensions used
	// when the client does not specify a size. They are deliberately larger
	// than a stock 80x24 because AI agents read the rendered screen as their
	// only window into the host: a tiny screen truncates output, causes
	// noisy wrap-induced scrollback, and pushes the model toward awkward
	// workarounds (writing to files, opening extra sessions, etc.). 200x50
	// gives ~10k chars per screen, large enough for typical `ls`, `tail`,
	// `ps`, log, and progress-bar output, while still bounded.
	defaultTermWidth  = 200
	defaultTermHeight = 50

	// minTermWidth/minTermHeight and maxTermWidth/maxTermHeight bound the
	// client-requested terminal size. The maxima keep a runaway client from
	// blowing up token cost; the minima keep the screen large enough that
	// the emulator can render anything meaningful.
	minTermWidth  = 20
	minTermHeight = 5
	maxTermWidth  = 500
	maxTermHeight = 200

	commandDelay      = 3500 * time.Millisecond
	screenDelay       = 2500 * time.Millisecond
	connectionTimeout = 30 * time.Second
	keepaliveInterval = 30 * time.Second
	keepaliveTimeout  = 15 * time.Second
	readBufferSize    = 4096
	keepaliveRequest  = "keepalive@openssh.com"
	ansiReset         = "\x1b[0m"
	version           = "2.3.0"

	// execMarkerPrefix is the fixed prefix of the begin/end-of-command
	// sentinels that ssh_exec injects into the shell stream. A per-call
	// random suffix is appended so the markers can never collide with the
	// user's own command text or output. The colon that follows the end
	// marker in its printed form is what disambiguates the real marker from
	// the shell's echo of the command line that contains the same token.
	execMarkerPrefix = "__SSHMCP"

	// defaultExecTimeout/maxExecTimeout bound how long ssh_exec waits for a
	// command to complete before returning control with the command still
	// running. They do not kill the command; they only stop waiting.
	// defaultExecTimeout mirrors Crush's bash tool, which auto-backgrounds a
	// synchronous command after 60s (DefaultAutoBackgroundAfter), so an agent
	// used to that cadence sees the same "still running, here's the state"
	// hand-off instead of a surprise.
	defaultExecTimeout = 60 * time.Second
	maxExecTimeout     = 10 * time.Minute

	// maxRemoteReadBytes caps how many bytes ssh_read_file will return in a
	// single call so a giant log file can't blow up the response. Callers can
	// page through larger files with the offset/length arguments.
	maxRemoteReadBytes = 256 * 1024
)
