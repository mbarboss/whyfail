package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/netguard"
)

// jsonOut checks that stdout holds exactly one JSON object and decodes it.
func jsonOut(t *testing.T, h *harness) map[string]any {
	t.Helper()
	out := h.stdout.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("stdout is not one line of JSON: %q (stderr %q)", out, h.stderr.String())
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", out, err)
	}
	return m
}

func errorCode(t *testing.T, m map[string]any) string {
	t.Helper()
	e, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error object in %v", m)
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Errorf("error without a message: %v", e)
	}
	code, _ := e["code"].(string)
	return code
}

func TestJSONPipeModeExplains(t *testing.T) {
	h, d := newHarness("fatal: no upstream\n")
	d.stderrIsTerminal = true

	code := run(context.Background(), []string{"-json"}, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
	m := jsonOut(t, h)
	if m["cause"] != "No upstream branch." || m["model"] != "gemma4:e4b" || m["command"] != nil || m["exitCode"] != nil {
		t.Errorf("report = %v", m)
	}
	if strings.Contains(h.stderr.String(), "Asking") {
		t.Errorf("progress shown in JSON mode: %q", h.stderr.String())
	}
}

func TestJSONPipeModeErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantExit int
	}{
		{"unreachable", fmt.Errorf("post: %w", llm.ErrUnreachable), "ollama_unreachable", exitFailure},
		{"model missing", fmt.Errorf("chat: %w", llm.ErrModelNotFound), "model_not_found", exitFailure},
		{"timeout", fmt.Errorf("chat: %w", llm.ErrTimeout), "timeout", exitFailure},
		{"malformed", fmt.Errorf("decode: %w", llm.ErrMalformedResponse), "malformed_answer", exitFailure},
		{"redirected", fmt.Errorf("dial: %w", netguard.ErrNotLoopback), "host_refused", exitFailure},
		{"interrupted", fmt.Errorf("chat: %w", context.Canceled), "interrupted", exitInterrupted},
		{"other", errors.New("status 500"), "internal", exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness("error\n")
			h.fake.err = tt.err

			code := run(context.Background(), []string{"-json"}, d)

			if code != tt.wantExit {
				t.Errorf("exit code = %d, want %d", code, tt.wantExit)
			}
			if got := errorCode(t, jsonOut(t, h)); got != tt.wantCode {
				t.Errorf("error.code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

func TestJSONUsageAndInputErrors(t *testing.T) {
	tests := []struct {
		name     string
		stdin    string
		tty      bool
		args     []string
		env      map[string]string
		wantCode string
	}{
		{"empty input", "\n", false, []string{"-json"}, nil, "empty_input"},
		{"interactive stdin", "", true, []string{"-json"}, nil, "usage"},
		{"bare word", "x\n", false, []string{"-json", "make"}, nil, "usage"},
		{"unknown flag", "x\n", false, []string{"-json", "-nope"}, nil, "usage"},
		{"invalid value", "x\n", false, []string{"-json"}, map[string]string{"WHYFAIL_TIMEOUT": "forever"}, "invalid_config"},
		{"remote host", "x\n", false, []string{"-json"}, map[string]string{"OLLAMA_HOST": "http://192.168.1.10:11434"}, "host_refused"},
		{"unresolvable host", "x\n", false, []string{"-json"}, map[string]string{"OLLAMA_HOST": "ollama.invalid"}, "host_unresolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness(tt.stdin)
			d.stdinIsTerminal = tt.tty
			d.resolver = fakeResolver{}
			for k, v := range tt.env {
				h.env[k] = v
			}

			code := run(context.Background(), tt.args, d)

			if code != exitUsage || h.fake.calls != 0 {
				t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
			}
			if got := errorCode(t, jsonOut(t, h)); got != tt.wantCode {
				t.Errorf("error.code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

func TestJSONWrapperSendsCommandOutputToStderr(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), wrap(t, "-json", "out:building", "err:fatal: boom", "exit:3"), d)

	if code != 3 {
		t.Fatalf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
	m := jsonOut(t, h)
	if m["exitCode"] != float64(3) || m["cause"] != "No upstream branch." {
		t.Errorf("report = %v", m)
	}
	if cmd, _ := m["command"].(string); !strings.Contains(cmd, "exit:3") {
		t.Errorf("command = %v", m["command"])
	}
	for _, want := range []string{"building\n", "fatal: boom\n"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks the command output %q: %q", want, h.stderr.String())
		}
	}
}

func TestJSONWrapperSuccess(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), wrap(t, "-json", "out:all good"), d)

	m := jsonOut(t, h)
	if code != exitOK || m["exitCode"] != float64(0) || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d, report = %v", code, h.fake.calls, m)
	}
	if _, ok := m["cause"]; ok {
		t.Error("successful command has a cause")
	}
}

func TestJSONWrapperRedactsCommand(t *testing.T) {
	secret := "hunter2-" + strings.Repeat("x", 8)
	h, d := newHarness("")

	run(context.Background(), wrap(t, "-json", "password="+secret), d)

	if cmd, _ := jsonOut(t, h)["command"].(string); !strings.Contains(cmd, "[REDACTED:") || strings.Contains(h.stdout.String(), secret) {
		t.Errorf("JSON carries the secret: %q", h.stdout.String())
	}
}

func TestJSONWrapperErrors(t *testing.T) {
	tests := []struct {
		name     string
		args     func(t *testing.T) []string
		ctx      func() context.Context
		explErr  error
		wantCode string
		wantExit int
	}{
		{"not found", func(*testing.T) []string { return []string{"-json", "--", "whyfail-test-no-such-command"} }, nil, nil, "command_not_found", exitNotFound},
		{"no output", func(t *testing.T) []string { return wrap(t, "-json", "exit:4") }, nil, nil, "no_output", 4},
		{"interrupted", func(t *testing.T) []string { return wrap(t, "-json", "err:x", "exit:3") }, canceled, nil, "interrupted", 3},
		{"explanation fails", func(t *testing.T) []string { return wrap(t, "-json", "err:x", "exit:5") }, nil, fmt.Errorf("post: %w", llm.ErrUnreachable), "ollama_unreachable", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness("")
			h.fake.err = tt.explErr
			ctx := context.Background()
			if tt.ctx != nil {
				ctx = tt.ctx()
			}

			code := run(ctx, tt.args(t), d)

			if code != tt.wantExit {
				t.Errorf("exit code = %d, want %d", code, tt.wantExit)
			}
			if got := errorCode(t, jsonOut(t, h)); got != tt.wantCode {
				t.Errorf("error.code = %q, want %q", got, tt.wantCode)
			}
		})
	}
}

func canceled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestJSONDoctor(t *testing.T) {
	h, d := newHarness("")
	h.server.hasModel = false

	code := run(context.Background(), []string{"-json", "doctor"}, d)

	m := jsonOut(t, h)
	if code != exitFailure || m["passed"] != false {
		t.Errorf("exit code = %d, report = %v", code, m)
	}
	if checks, _ := m["checks"].([]any); len(checks) != 4 {
		t.Errorf("checks = %v", m["checks"])
	}
}

func TestJSONDoctorInterrupted(t *testing.T) {
	h, d := newHarness("")

	code := run(canceled(), []string{"-json", "doctor"}, d)

	if code != exitInterrupted || errorCode(t, jsonOut(t, h)) != "interrupted" {
		t.Errorf("exit code = %d, stdout = %q", code, h.stdout.String())
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestWriteFailureExitCodes(t *testing.T) {
	tests := []struct {
		name string
		args func(t *testing.T) []string
		want int
	}{
		{"pipe text", func(*testing.T) []string { return nil }, exitFailure},
		{"pipe json", func(*testing.T) []string { return []string{"-json"} }, exitFailure},
		{"wrapper json keeps the child's code", func(t *testing.T) []string { return wrap(t, "-json", "err:x", "exit:6") }, 6},
		{"doctor", func(*testing.T) []string { return []string{"doctor"} }, exitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness("error\n")
			d.stdout = brokenWriter{}

			code := run(context.Background(), tt.args(t), d)

			if code != tt.want {
				t.Errorf("exit code = %d, want %d", code, tt.want)
			}
			if !strings.Contains(h.stderr.String(), "whyfail: write output: broken pipe") {
				t.Errorf("stderr = %q", h.stderr.String())
			}
		})
	}
}
