package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/apply"
	"github.com/mbarboss/whyfail/internal/llm"
)

// fakeTerminal records the confirmation dialog and the fix that ran.
type fakeTerminal struct {
	in      io.Reader
	out     bytes.Buffer
	closed  bool
	ran     *apply.Plan
	stdin   io.Reader
	fixExit int
	fixErr  error
}

// withTerminal gives d a terminal that answers with input, and a fix runner
// that records the plan instead of running it.
func withTerminal(d *deps, input string) *fakeTerminal {
	ft := &fakeTerminal{in: strings.NewReader(input)}
	d.openTerminal = func() (apply.Terminal, error) {
		return apply.Terminal{In: ft.in, Out: &ft.out, Close: func() error { ft.closed = true; return nil }}, nil
	}
	d.runFix = func(p apply.Plan, stdin io.Reader, _, _ io.Writer) (int, error) {
		ft.ran, ft.stdin = &p, stdin
		return ft.fixExit, ft.fixErr
	}
	return ft
}

func TestApplyRunsTheConfirmedFixThroughTheShell(t *testing.T) {
	h, d := newHarness("fatal: no upstream\n")
	ft := withTerminal(&d, "y\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr %q", code, exitOK, h.stderr.String())
	}
	if ft.ran == nil {
		t.Fatal("the fix did not run")
	}
	if want := []string{"zsh", "-c", "git push -u origin feature/x"}; !slices.Equal(ft.ran.Args, want) {
		t.Errorf("Args = %q, want %q", ft.ran.Args, want)
	}
	if ft.stdin != ft.in {
		t.Error("the fix does not read from the terminal")
	}
	if !strings.Contains(h.stdout.String(), "Cause: No upstream branch.") {
		t.Errorf("explanation not printed: %q", h.stdout.String())
	}
	if !strings.Contains(ft.out.String(), "Run it with zsh? [y/N]") || !ft.closed {
		t.Errorf("terminal = %q, closed = %v", ft.out.String(), ft.closed)
	}
}

func TestApplyPipeModeReturnsTheFixExitCode(t *testing.T) {
	h, d := newHarness("fatal: no upstream\n")
	ft := withTerminal(&d, "y\n")
	ft.fixExit = 4

	code := run(context.Background(), []string{"-apply"}, d)

	if code != 4 {
		t.Errorf("exit code = %d, want 4", code)
	}
	if !strings.Contains(h.stderr.String(), "the fix exited with code 4") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestApplyDeclinedRunsNothing(t *testing.T) {
	_, d := newHarness("fatal: no upstream\n")
	ft := withTerminal(&d, "\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitOK || ft.ran != nil {
		t.Errorf("exit code = %d, ran = %v", code, ft.ran)
	}
	if !strings.Contains(ft.out.String(), "Nothing was run.") {
		t.Errorf("terminal = %q", ft.out.String())
	}
}

func TestApplyDangerousFixNeedsYes(t *testing.T) {
	h, d := newHarness("permission denied\n")
	h.fake.answer.Fixes = []llm.Fix{{Command: "sudo make install", Description: "Install as root."}}
	ft := withTerminal(&d, "y\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitOK || ft.ran != nil {
		t.Errorf("exit code = %d, ran = %v", code, ft.ran)
	}
	if !strings.Contains(ft.out.String(), "Warning: "+apply.Elevated) {
		t.Errorf("terminal = %q", ft.out.String())
	}
}

func TestApplyRunsTheCommandAsDisplayed(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.answer.Fixes = []llm.Fix{{Command: "go  mod\ttidy \x1b[31m", Description: "Tidy."}}
	ft := withTerminal(&d, "y\n")

	run(context.Background(), []string{"-apply"}, d)

	if ft.ran == nil || ft.ran.Args[2] != "go mod tidy" {
		t.Errorf("ran = %v, want the sanitized command", ft.ran)
	}
}

func TestApplyRefusesRedactedFix(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.answer.Fixes = []llm.Fix{{Command: "export TOKEN=[REDACTED:github-token]", Description: "Set it."}}
	ft := withTerminal(&d, "y\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitFailure || ft.ran != nil {
		t.Errorf("exit code = %d, ran = %v", code, ft.ran)
	}
	if !strings.Contains(h.stderr.String(), "fix 1 contains a value that was redacted") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestApplyRefusesFixWithPlaceholder(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.answer.Fixes = []llm.Fix{{Command: "git push -u origin <correct-branch-name>", Description: "Push it."}}
	ft := withTerminal(&d, "yes\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitFailure || ft.ran != nil {
		t.Errorf("exit code = %d, ran = %v", code, ft.ran)
	}
	if !strings.Contains(h.stderr.String(), "fix 1 has a placeholder to replace") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
	if !strings.Contains(h.stdout.String(), "Warning: has a placeholder to replace before running") {
		t.Errorf("stdout lacks the placeholder warning: %q", h.stdout.String())
	}
}

func TestApplyReportsAFixThatCannotStart(t *testing.T) {
	h, d := newHarness("error\n")
	ft := withTerminal(&d, "y\n")
	ft.fixErr = errors.New("exec: permission denied")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitFailure || !strings.Contains(h.stderr.String(), "cannot run the fix") {
		t.Errorf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
}

func TestApplyInterruptedAtThePrompt(t *testing.T) {
	_, d := newHarness("error\n")
	ft := withTerminal(&d, "")
	in, w := io.Pipe()
	defer func() { _ = w.Close() }()
	ft.in = in

	code := run(canceled(), []string{"-apply"}, d)

	if code != exitInterrupted || ft.ran != nil {
		t.Errorf("exit code = %d, ran = %v", code, ft.ran)
	}
}

func TestApplyNeedsATerminal(t *testing.T) {
	h, d := newHarness("error\n")
	withTerminal(&d, "y\n")
	d.openTerminal = func() (apply.Terminal, error) { return apply.Terminal{}, errors.New("open /dev/tty: no such device") }

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, model calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "-apply needs an interactive terminal") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestApplyNeedsAKnownShell(t *testing.T) {
	h, d := newHarness("error\n")
	withTerminal(&d, "y\n")
	d.probe.ParentName = func() (string, error) { return "make", nil }

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, model calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "-shell") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestApplyNeedsTheShellInstalled(t *testing.T) {
	h, d := newHarness("error\n")
	withTerminal(&d, "y\n")
	d.lookPath = func(string) (string, error) { return "", errors.New("not found") }

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitUsage || !strings.Contains(h.stderr.String(), "cannot find zsh") {
		t.Errorf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
}

func TestApplyNotOfferedWhenTheExplanationFails(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.err = llm.ErrUnreachable
	ft := withTerminal(&d, "y\n")

	code := run(context.Background(), []string{"-apply"}, d)

	if code != exitFailure || ft.ran != nil || ft.out.Len() != 0 {
		t.Errorf("exit code = %d, ran = %v, terminal = %q", code, ft.ran, ft.out.String())
	}
}

func TestApplyWrapperKeepsTheCommandExitCode(t *testing.T) {
	_, d := newHarness("")
	ft := withTerminal(&d, "y\n")
	ft.fixExit = 0

	code := run(context.Background(), wrap(t, "-apply", "err:boom", "exit:3"), d)

	if code != 3 || ft.ran == nil {
		t.Errorf("exit code = %d, ran = %v; want 3 and the fix run", code, ft.ran)
	}
}

func TestApplyWrapperChecksTheTerminalBeforeRunning(t *testing.T) {
	h, d := newHarness("")
	d.openTerminal = func() (apply.Terminal, error) { return apply.Terminal{}, errors.New("no tty") }

	code := run(context.Background(), wrap(t, "-apply", "out:ran", "exit:3"), d)

	if code != exitUsage || strings.Contains(h.stdout.String(), "ran") {
		t.Errorf("exit code = %d, stdout = %q; the command must not run", code, h.stdout.String())
	}
}

func TestApplyWrapperSuccessAsksNothing(t *testing.T) {
	_, d := newHarness("")
	ft := withTerminal(&d, "y\n")

	code := run(context.Background(), wrap(t, "-apply", "out:fine"), d)

	if code != exitOK || ft.ran != nil || ft.out.Len() != 0 {
		t.Errorf("exit code = %d, ran = %v, terminal = %q", code, ft.ran, ft.out.String())
	}
}
