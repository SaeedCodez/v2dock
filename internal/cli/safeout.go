package cli

import (
	"io"
	"strings"
	"unicode"
)

// safeWriter neutralises terminal control sequences in human-readable output.
// Server names come from subscriptions and container names from Docker
// labels; an ESC or OSC sequence in one must not reach the admin's terminal.
// JSON output is already escaped by encoding/json and does not use this.
type safeWriter struct{ w io.Writer }

func (s safeWriter) Write(p []byte) (int, error) {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			return r
		case unicode.IsControl(r), unicode.Is(unicode.Bidi_Control, r):
			return '?'
		}
		return r
	}, string(p))
	if _, err := io.WriteString(s.w, clean); err != nil {
		return 0, err
	}
	return len(p), nil
}
