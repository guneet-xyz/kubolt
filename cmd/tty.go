package cmd

import (
	"io"
	"os"

	"golang.org/x/term"
)

// isInteractive reports whether w is a real terminal (TTY).
// Non-file writers (e.g. bytes.Buffer in tests) are treated as non-TTY.
func isInteractive(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
