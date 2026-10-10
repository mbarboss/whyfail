package render

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mbarboss/whyfail/internal/llm"
)

func TestSanitizeRemovesEscapeAndControlSequences(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain text", "hello world", "hello world"},
		{"keeps newline and tab", "a\n\tb", "a\n\tb"},
		{"SGR color", "\x1b[31mred\x1b[0m", "red"},
		{"cursor movement", "a\x1b[2Jb\x1b[1;1Hc", "abc"},
		{"OSC title with BEL", "\x1b]0;pwned\x07text", "text"},
		{"OSC 8 hyperlink with ST", "\x1b]8;;http://evil.test\x1b\\click\x1b]8;;\x1b\\", "click"},
		{"OSC 52 clipboard", "\x1b]52;c;cm0gLXJmIH4=\x07ok", "ok"},
		{"DCS sequence", "\x1bP1$r0m\x1b\\ok", "ok"},
		{"lone ESC at end", "ab\x1b", "ab"},
		{"terminal reset", "a\x1bcb", "ab"},
		{"C1 CSI", "a\u009b31mb", "a31mb"},
		{"carriage return overwrite", "safe\rrm -rf ~", "saferm -rf ~"},
		{"backspace", "rm\b\bls", "rmls"},
		{"bell and null", "a\x07\x00b", "ab"},
		{"DEL", "a\x7fb", "ab"},
		{"bidi override", "ls \u202edm.txt", "ls dm.txt"},
		{"bidi isolate", "a\u2066b\u2069c", "abc"},
		{"keeps unicode", "café ✓ 日本", "café ✓ 日本"},
		{"unterminated CSI", "ok\x1b[31", "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sanitize(tt.in); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func explanation() llm.Explanation {
	return llm.Explanation{
		Cause:       "The Docker socket is not accessible.",
		Explanation: "Your user is not in the docker group.",
		Fixes: []llm.Fix{
			{Command: "sudo usermod -aG docker $USER", Description: "Add your user to the docker group."},
			{Command: "newgrp docker", Description: "Apply the group change."},
		},
	}
}

func warnSudo(cmd string) []string {
	if strings.HasPrefix(cmd, "sudo") {
		return []string{"runs with administrator privileges"}
	}
	return nil
}

func TestTextLayout(t *testing.T) {
	var buf bytes.Buffer

	if err := Text(&buf, explanation(), warnSudo); err != nil {
		t.Fatalf("Text: %v", err)
	}

	want := `Cause: The Docker socket is not accessible.

Your user is not in the docker group.

Suggested fixes (review before running):

  1. Add your user to the docker group.
     sudo usermod -aG docker $USER
     Warning: runs with administrator privileges.

  2. Apply the group change.
     newgrp docker
`
	if got := buf.String(); got != want {
		t.Errorf("output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextSanitizesEveryField(t *testing.T) {
	e := llm.Explanation{
		Cause:       "\x1b]0;title\x07cause",
		Explanation: "line\x1b[2J",
		Fixes:       []llm.Fix{{Command: "ls\u202e", Description: "\x1b[31mlist"}},
	}
	var buf bytes.Buffer

	if err := Text(&buf, e, func(string) []string { return []string{"\x1b[1mbad"} }); err != nil {
		t.Fatalf("Text: %v", err)
	}

	if out := buf.String(); strings.ContainsAny(out, "\x1b\x07\u202e") {
		t.Errorf("output contains control characters: %q", out)
	}
}

func TestTextWarnsOnSanitizedCommand(t *testing.T) {
	var seen string
	e := explanation()
	e.Fixes = []llm.Fix{{Command: "su\x1b[0mdo rm x", Description: "d"}}

	_ = Text(&bytes.Buffer{}, e, func(cmd string) []string { seen = cmd; return nil })

	if seen != "sudo rm x" {
		t.Errorf("warn got %q, want the sanitized command", seen)
	}
}

func TestTextIndentsMultiLineExplanation(t *testing.T) {
	e := explanation()
	e.Fixes[0].Description = "first\nsecond"
	var buf bytes.Buffer

	_ = Text(&buf, e, nil)

	if !strings.Contains(buf.String(), "  1. first\n     second\n") {
		t.Errorf("multi-line description not indented:\n%s", buf.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestTextReturnsWriteError(t *testing.T) {
	if err := Text(failWriter{}, explanation(), nil); err == nil {
		t.Error("want write error")
	}
}

// syncBuffer is a bytes.Buffer safe for the spinner goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestProgressDrawsAndErases(t *testing.T) {
	var buf syncBuffer

	stop := StartProgress(&buf, "Asking gemma4:e4b", time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	stop()
	out := buf.String()

	if !strings.Contains(out, "Asking gemma4:e4b") {
		t.Errorf("label not drawn: %q", out)
	}
	if !strings.HasSuffix(out, "\r") || strings.Contains(out, "\x1b") {
		t.Errorf("line not erased with plain carriage returns: %q", out)
	}
}

func TestProgressStopIsIdempotentAndQuiet(t *testing.T) {
	var buf syncBuffer
	stop := StartProgress(&buf, "x", time.Hour)
	stop()
	n := len(buf.String())

	stop()
	time.Sleep(5 * time.Millisecond)

	if len(buf.String()) != n {
		t.Error("output after stop")
	}
}
