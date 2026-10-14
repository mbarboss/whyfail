package render

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mbarboss/whyfail/internal/llm"
)

func TestWrapBreaksAtWordBoundaries(t *testing.T) {
	got := wrap("", "the quick brown fox jumps over the lazy dog", 15, "")

	want := "the quick brown\nfox jumps over\nthe lazy dog"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapStartsWithPrefixAndIndentsLaterLines(t *testing.T) {
	got := wrap("Cause: ", "the quick brown fox jumps", 20, "       ")

	want := "Cause: the quick\n       brown fox\n       jumps"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapKeepsShortTextOnOneLine(t *testing.T) {
	if got := wrap("> ", "short text", 80, "  "); got != "> short text" {
		t.Errorf("got %q", got)
	}
}

func TestWrapKeepsLongWordsIntact(t *testing.T) {
	path := "/usr/local/lib/node_modules/typescript/lib/tsserver.js"

	got := wrap("", "cannot open "+path+" now", 20, "")

	want := "cannot open\n" + path + "\nnow"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWrapCountsCharactersNotBytes(t *testing.T) {
	// Each word is 4 characters but 6 bytes; three fit in 14 columns.
	got := wrap("", "ação ação ação ação", 14, "")

	if got != "ação ação ação\nação" {
		t.Errorf("got %q", got)
	}
}

func TestWrapKeepsLineBreaksFromTheText(t *testing.T) {
	got := wrap("", "first line\n\nsecond line", 80, "  ")

	if got != "first line\n\n  second line" {
		t.Errorf("got %q", got)
	}
}

func TestWrapWithoutWidthDoesNotWrap(t *testing.T) {
	long := strings.Repeat("word ", 50)

	if got := wrap("", long, 0, ""); got != strings.TrimSpace(long) {
		t.Errorf("wrapped with width 0: %q", got)
	}
}

func longExplanation() llm.Explanation {
	return llm.Explanation{
		Cause:       "The global npm directory belongs to root, so your user cannot create the package folder inside it.",
		Explanation: "npm installs global packages under /usr/local/lib/node_modules by default. That directory was created by the installer with root as owner, and npm refuses to write there as your user.",
		Fixes: []llm.Fix{{
			Command:     "npm config set prefix ~/.npm-global && echo 'export PATH=~/.npm-global/bin:$PATH' >> ~/.zshrc",
			Description: "Move global installs to a directory in your home folder and add its bin directory to PATH.",
		}},
	}
}

func TestTextWrapsProseButNotCommands(t *testing.T) {
	var buf bytes.Buffer
	e := longExplanation()

	if err := Text(&buf, e, func(string) []string { return []string{"modifies files or settings in place"} }, 60); err != nil {
		t.Fatalf("Text: %v", err)
	}

	want := `Cause: The global npm directory belongs to root, so your
       user cannot create the package folder inside it.

npm installs global packages under
/usr/local/lib/node_modules by default. That directory was
created by the installer with root as owner, and npm refuses
to write there as your user.

Suggested fixes (review before running):

  1. Move global installs to a directory in your home folder
     and add its bin directory to PATH.
     ` + e.Fixes[0].Command + `
     Warning: modifies files or settings in place.
`
	if got := buf.String(); got != want {
		t.Errorf("output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextAlignsWrappedWarningsWithTheirText(t *testing.T) {
	var buf bytes.Buffer
	reason := "installs a package named in the error output; check the name and source first"

	if err := Text(&buf, longExplanation(), func(string) []string { return []string{reason} }, 60); err != nil {
		t.Fatalf("Text: %v", err)
	}

	want := "     Warning: installs a package named in the error output;\n" +
		"              check the name and source first.\n"
	if !strings.HasSuffix(buf.String(), want) {
		t.Errorf("output does not end with %q:\n%s", want, buf.String())
	}
}

func TestTextRespectsWidthExceptForCommandsAndLongWords(t *testing.T) {
	var buf bytes.Buffer
	e := longExplanation()

	if err := Text(&buf, e, nil, 40); err != nil {
		t.Fatalf("Text: %v", err)
	}

	for _, line := range strings.Split(buf.String(), "\n") {
		if utf8.RuneCountInString(line) <= 40 || strings.Contains(line, e.Fixes[0].Command) ||
			strings.Contains(line, "/usr/local/lib/node_modules") {
			continue
		}
		t.Errorf("line longer than 40 columns: %q", line)
	}
}

func TestLineWidth(t *testing.T) {
	tests := []struct {
		name       string
		size       int
		isTerminal bool
		want       int
	}{
		{"wide terminal is capped", 200, true, MaxWidth},
		{"narrow terminal is used as is", 60, true, 60},
		{"exact cap", MaxWidth, true, MaxWidth},
		{"unknown size", 0, true, DefaultWidth},
		{"not a terminal", 0, false, DefaultWidth},
		{"size reported for a non-terminal is ignored", 120, false, DefaultWidth},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LineWidth(tt.size, tt.isTerminal); got != tt.want {
				t.Errorf("LineWidth(%d, %v) = %d, want %d", tt.size, tt.isTerminal, got, tt.want)
			}
		})
	}
}
