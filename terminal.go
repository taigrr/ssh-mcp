package main

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// renderScreen returns the full visible screen of the emulator as a string,
// preserving ANSI styling and using a newline between rows.
func renderScreen(emulator *vt.Emulator) string {
	rows := make([]string, emulator.Height())
	for y := range emulator.Height() {
		rows[y] = renderLine(emulator, y)
	}

	return strings.Join(rows, "\n")
}

// renderLine renders a single row of the emulator, emitting style sequences
// only when the style changes between cells. The rendered line ends with an
// ANSI reset to avoid bleeding styles into the next row.
func renderLine(emulator *vt.Emulator, y int) string {
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
