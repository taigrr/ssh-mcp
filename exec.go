package main

import (
	"bytes"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// execMarkers holds the two sentinels for a single ssh_exec invocation. The
// begin marker delimits the start of the command's real output (everything
// before it is the shell's echo of the wrapper line we typed); the end marker
// carries the command's exit code.
type execMarkers struct {
	begin string // e.g. "__SSHMCP_B_1782_7"
	end   string // e.g. "__SSHMCP_E_1782_7"
}

var execCounter atomic.Uint64

// newExecMarkers returns a pair of markers guaranteed unique within this
// process. The random-ish suffix (nanos + monotonic counter) makes accidental
// collision with user output impossible in practice.
func newExecMarkers() execMarkers {
	n := execCounter.Add(1)
	ts := time.Now().UnixNano()
	return execMarkers{
		begin: fmt.Sprintf("%s_B_%d_%d", execMarkerPrefix, ts, n),
		end:   fmt.Sprintf("%s_E_%d_%d", execMarkerPrefix, ts, n),
	}
}

// wrap builds the one-liner sent to the shell. The shell echoes this whole
// line back through the PTY (so it lands in the emulator faithfully); the
// begin marker is printed on its own line *before* the command runs and the
// end marker *after*, carrying the command's exit status via $?.
//
// Crucially $? is expanded as an argument to the trailing printf, so it
// reflects the user's command, not the begin-marker printf.
func (m execMarkers) wrap(command string) string {
	return fmt.Sprintf("printf '%%s\\n' %s; %s; printf '\\n%%s:%%d\\n' %s $?\n",
		m.begin, command, m.end)
}

// execWatcher taps the raw stdout byte stream of a session while an ssh_exec
// call is in flight. It accumulates bytes, watches for the end marker, and
// signals completion with the parsed exit code. It never touches the emulator
// — the emulator sees the identical bytes independently, so the on-screen view
// stays a faithful mirror of the real terminal.
type execWatcher struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	beginPat string // begin marker token, searched with a trailing newline
	endTok   string // end marker token, searched with a trailing colon
	done     chan int
	closed   bool
}

func newExecWatcher(m execMarkers) *execWatcher {
	return &execWatcher{
		beginPat: m.begin,
		endTok:   m.end,
		done:     make(chan int, 1),
	}
}

// write feeds a chunk of raw stdout bytes into the watcher and scans for the
// completion marker. It is called from pumpOutput for every chunk while a tap
// is installed.
func (w *execWatcher) write(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.buf.Write(p)
	w.scanLocked()
}

// scanLocked looks for "<end>:" followed by digits and a newline. The colon is
// what distinguishes the printed marker from the shell's echo of the wrapper
// line (which contains the bare token followed by " $?", never "<end>:").
func (w *execWatcher) scanLocked() {
	_, after, found := bytes.Cut(w.buf.Bytes(), []byte(w.endTok+":"))
	if !found {
		return
	}
	line, _, found := bytes.Cut(after, []byte{'\n'})
	if !found {
		return // exit code line not fully arrived yet
	}
	code, err := strconv.Atoi(string(bytes.TrimSpace(line)))
	if err != nil {
		code = -1
	}
	w.closed = true
	w.done <- code
}

// output returns the command's raw output: the bytes between the begin
// marker's own line and the end marker. Returns ok=false if either marker is
// missing (e.g. on timeout). The begin marker reliably skips past the shell's
// echo of the wrapper line: the echoed token is followed by ";" whereas the
// printed marker is followed by a newline (optionally preceded by CR), so we
// only accept an occurrence that terminates a line.
func (w *execWatcher) output() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	data := w.buf.Bytes()

	start, ok := afterBeginLine(data, []byte(w.beginPat))
	if !ok {
		return nil, false
	}

	endNeedle := []byte(w.endTok + ":")
	e := bytes.Index(data[start:], endNeedle)
	if e < 0 {
		return nil, false
	}
	return data[start : start+e], true
}

// afterBeginLine returns the index just past the newline that terminates the
// begin marker's own output line. It scans for occurrences of the marker token
// and accepts only one immediately followed by "\n" or "\r\n" (the printed
// marker), skipping the shell's echo where the token is followed by other
// characters.
func afterBeginLine(data, token []byte) (int, bool) {
	from := 0
	for {
		i := bytes.Index(data[from:], token)
		if i < 0 {
			return 0, false
		}
		pos := from + i + len(token)
		rest := data[pos:]
		switch {
		case len(rest) >= 2 && rest[0] == '\r' && rest[1] == '\n':
			return pos + 2, true
		case len(rest) >= 1 && rest[0] == '\n':
			return pos + 1, true
		case len(rest) == 1 && rest[0] == '\r':
			// Trailing CR with the LF not yet arrived; wait for more.
			return 0, false
		case len(rest) == 0:
			// Token at end of buffer; the newline hasn't arrived yet.
			return 0, false
		}
		from = pos
	}
}
