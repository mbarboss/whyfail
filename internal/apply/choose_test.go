package apply

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func choose(t *testing.T, input string, commands ...string) (int, string, error) {
	t.Helper()
	var out bytes.Buffer
	i, err := Choose(context.Background(), strings.NewReader(input), &out, commands, "bash", Warnings)
	return i, out.String(), err
}

func TestChooseSingleFixAsksOnlyForConfirmation(t *testing.T) {
	i, out, err := choose(t, "y\n", "go mod tidy")

	if err != nil || i != 0 {
		t.Fatalf("Choose = %d, %v; want 0, nil", i, err)
	}
	if strings.Contains(out, "Run which fix") {
		t.Errorf("asked to pick among a single fix: %q", out)
	}
	for _, want := range []string{"go mod tidy", "Run it with bash? [y/N]"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
}

func TestChoosePicksByNumber(t *testing.T) {
	i, out, err := choose(t, "2\nyes\n", "make", "go build ./...", "go vet ./...")

	if err != nil || i != 1 {
		t.Fatalf("Choose = %d, %v; want 1, nil", i, err)
	}
	if !strings.Contains(out, "Run which fix? [1-3, Enter to skip]") {
		t.Errorf("output lacks the fix prompt: %q", out)
	}
}

func TestChooseAsksAgainAfterAnInvalidNumber(t *testing.T) {
	i, out, err := choose(t, "7\nx\n1\ny\n", "make", "go build ./...")

	if err != nil || i != 0 {
		t.Fatalf("Choose = %d, %v; want 0, nil", i, err)
	}
	if got := strings.Count(out, "Run which fix?"); got != 3 {
		t.Errorf("fix prompt shown %d times, want 3: %q", got, out)
	}
}

func TestChooseSkip(t *testing.T) {
	for name, input := range map[string]string{"enter": "\n", "end of input": ""} {
		t.Run(name, func(t *testing.T) {
			_, _, err := choose(t, input, "make", "go build ./...")

			if !errors.Is(err, ErrSkipped) {
				t.Errorf("err = %v, want ErrSkipped", err)
			}
		})
	}
}

func TestChooseDefaultsToNo(t *testing.T) {
	for _, input := range []string{"\n", "n\n", "no\n", "Y es\n", "sure\n", ""} {
		t.Run(strings.TrimSpace(input), func(t *testing.T) {
			_, _, err := choose(t, input, "go mod tidy")

			if !errors.Is(err, ErrDeclined) {
				t.Errorf("input %q: err = %v, want ErrDeclined", input, err)
			}
		})
	}
}

func TestChooseAcceptsYesInAnyCase(t *testing.T) {
	for _, input := range []string{"y\n", "Y\n", " yes \n", "YES\r\n", "y"} {
		_, _, err := choose(t, input, "go mod tidy")
		if err != nil {
			t.Errorf("input %q: err = %v, want nil", input, err)
		}
	}
}

func TestChooseDangerousFixNeedsTheWordYes(t *testing.T) {
	_, out, err := choose(t, "y\n", "sudo apt install make")

	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined", err)
	}
	for _, want := range []string{"Warning: " + Elevated + ".", `Type "yes" to run it with bash:`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q: %q", want, out)
		}
	}
	if strings.Contains(out, "[y/N]") {
		t.Errorf("dangerous fix offered a y/N prompt: %q", out)
	}

	if _, _, err := choose(t, "Yes\n", "sudo apt install make"); err != nil {
		t.Errorf("typing yes: err = %v, want nil", err)
	}
}

func TestChooseUsesTheGivenWarnings(t *testing.T) {
	warn := WarningsFor("No module named 'foo'")
	var out bytes.Buffer

	_, err := Choose(context.Background(), strings.NewReader("y\n"), &out, []string{"pip install foo"}, "bash", warn)

	if !errors.Is(err, ErrDeclined) {
		t.Fatalf("err = %v, want ErrDeclined: a warned fix needs \"yes\"", err)
	}
	if !strings.Contains(out.String(), "Warning: "+NamedPackage) {
		t.Errorf("output lacks the warning: %q", out.String())
	}
}

func TestChooseShowsTheChosenCommandBeforeConfirming(t *testing.T) {
	_, out, _ := choose(t, "2\nn\n", "make", "rm -rf build && make")

	cmd := strings.Index(out, "rm -rf build && make")
	confirm := strings.Index(out, `Type "yes"`)
	if cmd < 0 || confirm < cmd {
		t.Errorf("command not shown before the confirmation: %q", out)
	}
	if !strings.Contains(out, "Warning: "+RecursiveDelete+".") {
		t.Errorf("warning not repeated before the confirmation: %q", out)
	}
}

func TestChooseRefusesRedactedFix(t *testing.T) {
	i, out, err := choose(t, "2\n", "make", "export TOKEN=[REDACTED:github-token]")

	if !errors.Is(err, ErrRedacted) || i != 1 {
		t.Fatalf("Choose = %d, %v; want 1, ErrRedacted", i, err)
	}
	if strings.Contains(out, "[y/N]") || strings.Contains(out, `"yes"`) {
		t.Errorf("asked to confirm a redacted fix: %q", out)
	}
}

func TestChooseRefusesFixWithPlaceholder(t *testing.T) {
	i, out, err := choose(t, "2\nyes\n", "git branch", "git push -u origin <correct-branch-name>")

	if !errors.Is(err, ErrPlaceholder) || i != 1 {
		t.Fatalf("Choose = %d, %v; want 1, ErrPlaceholder", i, err)
	}
	if strings.Contains(out, "[y/N]") || strings.Contains(out, `"yes"`) {
		t.Errorf("asked to confirm a fix with a placeholder: %q", out)
	}
}

func TestChooseStopsWhenInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in, w := io.Pipe()
	defer func() { _ = w.Close() }()

	_, err := Choose(ctx, in, io.Discard, []string{"go mod tidy"}, "bash", Warnings)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("tty gone") }

func TestChooseReportsReadErrors(t *testing.T) {
	_, err := Choose(context.Background(), failingReader{}, io.Discard, []string{"make"}, "bash", Warnings)

	if err == nil || errors.Is(err, ErrDeclined) || !strings.Contains(err.Error(), "tty gone") {
		t.Errorf("err = %v, want the read error", err)
	}
}
