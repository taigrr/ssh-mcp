package main

import (
	"regexp"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// ansiSequence matches the CSI/OSC/escape sequences a shell and its programs
// emit (colors, cursor moves, etc.). ssh_exec strips these so the captured
// output is plain text the model can read without noise.
var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]|[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)

// stripANSI removes ANSI escape sequences and stray control bytes from s,
// normalizing carriage returns so the result reads as plain UTF-8 lines. It
// preserves newlines and tabs.
func stripANSI(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	// Re-allow tab and newline which the control-byte class above would eat.
	out := ansiSequence.ReplaceAllStringFunc(s, func(m string) string {
		if m == "\t" || m == "\n" {
			return m
		}
		return ""
	})
	return out
}

// renderScreen returns the full visible screen of the emulator as a string,
// preserving ANSI styling and using a newline between rows.
func renderScreen(emulator *vt.SafeEmulator) string {
	rows := make([]string, emulator.Height())
	for y := range emulator.Height() {
		rows[y] = renderLine(emulator, y)
	}

	return strings.Join(rows, "\n")
}

// renderLine renders a single row of the emulator, emitting style sequences
// only when the style changes between cells. The rendered line ends with an
// ANSI reset to avoid bleeding styles into the next row.
func renderLine(emulator *vt.SafeEmulator, y int) string {
	var builder strings.Builder
	var lastStyle uv.Style

	for x := range emulator.Width() {
		cell := emulator.CellAt(x, y)
		if cell == nil {
			builder.WriteByte(' ')
			continue
		}

		if x == 0 || !cell.Style.Equal(&lastStyle) {
			builder.WriteString(cell.Style.String())
			lastStyle = cell.Style
		}

		if cell.Content == "" {
			builder.WriteByte(' ')
		} else {
			builder.WriteString(cell.Content)
		}
	}

	if builder.Len() > 0 {
		builder.WriteString(ansiReset)
	}

	return builder.String()
}
