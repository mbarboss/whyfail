// Package render prints explanations for humans.
package render

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/mbarboss/whyfail/internal/llm"
)

// escapeSeq matches ANSI escape sequences: CSI (colors, cursor movement),
// OSC (titles, hyperlinks, clipboard) and DCS/SOS/PM/APC strings, including
// unterminated ones at the end of the text.
var escapeSeq = regexp.MustCompile(
	`\x1b\[[0-?]*[ -/]*[@-~]?` + // CSI
		`|\x1b[\]PX^_][^\x07\x1b]*(\x07|\x1b\\)?` + // OSC, DCS, SOS, PM, APC
		`|\x1b[ -/]*[0-~]?`, // other two-character escapes and a lone ESC
)

// Sanitize removes terminal escape sequences, control characters and
// bidirectional overrides from s, keeping newlines and tabs.
func Sanitize(s string) string {
	s = escapeSeq.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		// Cc covers C0, DEL and C1; Cf covers the bidi overrides and isolates
		// used to make text display differently from what runs.
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r):
			return -1
		default:
			return r
		}
	}, s)
}

// Text writes e as plain text. warn returns the warnings for one command; it
// may be nil. Every field is sanitized because it comes from the model.
func Text(w io.Writer, e llm.Explanation, warn func(command string) []string, width int) error {
	var b strings.Builder
	b.WriteString(wrap("Cause: ", OneLine(e.Cause), width, "       ") + "\n\n")
	b.WriteString(wrap("", Sanitize(e.Explanation), width, "") + "\n\n")
	b.WriteString("Suggested fixes (review before running):\n")

	for i, f := range e.Fixes {
		num := strconv.Itoa(i+1) + ". "
		indent := strings.Repeat(" ", 2+len(num))
		cmd := OneLine(f.Command)

		b.WriteString("\n" + wrap("  "+num, Sanitize(f.Description), width, indent) + "\n")
		b.WriteString(indent + cmd + "\n")
		if warn != nil {
			for _, reason := range warn(cmd) {
				b.WriteString(wrap(indent+"Warning: ", OneLine(reason)+".", width, indent+"         ") + "\n")
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// OneLine sanitizes s and collapses its whitespace into single spaces.
// Commands are shown in this form, and -apply runs them exactly as shown.
func OneLine(s string) string {
	return strings.Join(strings.Fields(Sanitize(s)), " ")
}
