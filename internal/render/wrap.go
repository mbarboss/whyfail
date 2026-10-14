package render

import (
	"strings"
	"unicode/utf8"
)

// Line widths for text output. MaxWidth keeps prose readable on wide
// terminals; DefaultWidth applies when output is not a terminal.
const (
	DefaultWidth = 80
	MaxWidth     = 100
)

// LineWidth returns the width to wrap text output at, given the terminal size
// reported for stdout.
func LineWidth(size int, isTerminal bool) int {
	if !isTerminal || size <= 0 {
		return DefaultWidth
	}
	return min(size, MaxWidth)
}

// wrap returns prefix followed by text, broken at spaces so that lines fit in
// width columns, with every later line starting with indent. Line breaks in
// text are kept. Words longer than a line are not split, so paths and URLs
// stay intact. Columns are counted in characters; a width of 0 disables
// wrapping.
func wrap(prefix, text string, width int, indent string) string {
	var b strings.Builder
	for i, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		words := strings.Fields(line)
		start := indent
		if i == 0 {
			start = prefix
		} else {
			b.WriteByte('\n')
			if len(words) == 0 {
				continue
			}
		}
		b.WriteString(start)
		col := utf8.RuneCountInString(start)
		for j, word := range words {
			n := utf8.RuneCountInString(word)
			switch {
			case j == 0:
			case width > 0 && col+1+n > width:
				b.WriteString("\n" + indent)
				col = utf8.RuneCountInString(indent)
			default:
				b.WriteByte(' ')
				col++
			}
			b.WriteString(word)
			col += n
		}
	}
	return b.String()
}
