// Package capture collects the output of a failed command.
package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"
)

// Limits bounds how much of the output is kept. Only the tail is kept because
// the error that matters is almost always at the end.
type Limits struct {
	MaxBytes int
	MaxLines int
}

// DefaultLimits keeps the prompt small enough for a 3B to 8B model.
var DefaultLimits = Limits{MaxBytes: 16 << 10, MaxLines: 200}

// ErrEmpty reports input that holds nothing but whitespace.
var ErrEmpty = errors.New("no output to explain")

// Tail is the kept part of the output.
type Tail struct {
	Text string
	// Truncated is true when earlier output was dropped.
	Truncated bool
}

// ReadTail reads r to EOF and keeps at most the last l.MaxLines lines and
// l.MaxBytes bytes, as valid UTF-8 with LF line endings. Memory use stays
// bounded regardless of the input size.
func ReadTail(r io.Reader, l Limits) (Tail, error) {
	w, err := newTailWriter(l)
	if err != nil {
		return Tail{}, err
	}
	if _, err := io.Copy(w, r); err != nil {
		return Tail{}, fmt.Errorf("capture: read input: %w", err)
	}
	return w.Tail()
}

// tailWriter keeps the last bytes written to it. It is safe for concurrent
// use, so a command's stdout and stderr can share one.
type tailWriter struct {
	mu      sync.Mutex
	limits  Limits
	keep    int
	buf     []byte
	dropped bool
}

func newTailWriter(l Limits) (*tailWriter, error) {
	if l.MaxBytes <= 0 || l.MaxLines <= 0 {
		return nil, fmt.Errorf("capture: limits must be positive, got %+v", l)
	}
	// CRLF is normalized in Tail, so keep a little more than MaxBytes to make
	// up for the carriage returns that will be dropped.
	return &tailWriter{limits: l, keep: 2 * l.MaxBytes}, nil
}

// Write never fails, so a broken terminal never loses the captured output.
func (w *tailWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if len(p) > w.keep {
		p = p[len(p)-w.keep:]
		w.dropped = true
	}
	w.buf = append(w.buf, p...)
	if over := len(w.buf) - w.keep; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
		w.dropped = true
	}
	return n, nil
}

// Tail applies the limits to what was written. It returns ErrEmpty when that
// holds nothing but whitespace.
func (w *tailWriter) Tail() (Tail, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	buf := bytes.ReplaceAll(w.buf, []byte("\r\n"), []byte("\n"))
	text, truncated := cut(buf, w.limits)
	text = strings.ToValidUTF8(text, "�")
	if strings.TrimSpace(text) == "" {
		return Tail{}, ErrEmpty
	}
	return Tail{Text: text, Truncated: truncated || w.dropped}, nil
}

// cut applies the byte and line limits to b, starting on a line boundary when
// a line was cut in the middle and on a rune boundary otherwise.
func cut(b []byte, l Limits) (string, bool) {
	truncated := false
	if len(b) > l.MaxBytes {
		b = b[len(b)-l.MaxBytes:]
		truncated = true
		if i := bytes.IndexByte(b[:len(b)-1], '\n'); i >= 0 {
			b = b[i+1:]
		}
		for len(b) > 0 && !utf8.RuneStart(b[0]) {
			b = b[1:]
		}
	}

	// Count lines from the end, ignoring the final newline.
	body := bytes.TrimSuffix(b, []byte("\n"))
	for i, lines := len(body)-1, 1; i >= 0; i-- {
		if body[i] != '\n' {
			continue
		}
		if lines == l.MaxLines {
			return string(b[i+1:]), true
		}
		lines++
	}
	return string(b), truncated
}
