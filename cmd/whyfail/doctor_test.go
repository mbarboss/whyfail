package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/llm"
)

func TestDoctorAllChecksPass(t *testing.T) {
	h, d := newHarness("")
	d.stdinIsTerminal = true

	code := run(context.Background(), []string{"doctor"}, d)

	if code != exitOK || h.fake.calls != 0 {
		t.Fatalf("exit code = %d, calls = %d, stdout = %q", code, h.fake.calls, h.stdout.String())
	}
	for _, want := range []string{
		"ok    Ollama host is on this machine (http://127.0.0.1:11434)\n",
		"ok    Ollama 0.40.1 is running\n",
		"ok    Model gemma4:e4b is installed\n",
		"info  Environment: Linux (Test Linux 1), arm64, shell zsh\n",
	} {
		if !strings.Contains(h.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, h.stdout.String())
		}
	}
}

func TestDoctorUsesConfiguredModel(t *testing.T) {
	h, d := newHarness("")
	h.server.hasModel = false

	code := run(context.Background(), []string{"doctor", "-model", "qwen3.5:4b"}, d)

	if code != exitFailure {
		t.Errorf("exit code = %d, want %d", code, exitFailure)
	}
	if !strings.Contains(h.stdout.String(), "Fix: ollama pull qwen3.5:4b") {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestDoctorOllamaDown(t *testing.T) {
	h, d := newHarness("")
	h.server.err = fmt.Errorf("get: %w", llm.ErrUnreachable)

	code := run(context.Background(), []string{"doctor"}, d)

	if code != exitFailure || !strings.Contains(h.stdout.String(), "ollama serve") {
		t.Errorf("exit code = %d, stdout = %q", code, h.stdout.String())
	}
}

func TestDoctorRemoteHostFailsInsteadOfUsageError(t *testing.T) {
	h, d := newHarness("")
	h.env["OLLAMA_HOST"] = "http://192.168.1.10:11434"

	code := run(context.Background(), []string{"doctor"}, d)

	if code != exitFailure || !strings.Contains(h.stdout.String(), "--allow-remote") {
		t.Errorf("exit code = %d, stdout = %q", code, h.stdout.String())
	}
}

func TestDoctorResolvesHostNames(t *testing.T) {
	h, d := newHarness("")
	d.resolver = fakeResolver{"localhost": "127.0.0.1"}
	h.env["OLLAMA_HOST"] = "localhost"

	if code := run(context.Background(), []string{"doctor"}, d); code != exitOK {
		t.Errorf("exit code = %d, stdout = %q", code, h.stdout.String())
	}
}

func TestDoctorRejectsInvalidConfig(t *testing.T) {
	_, d := newHarness("")

	if code := run(context.Background(), []string{"doctor", "extra"}, d); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestDoctorInterrupted(t *testing.T) {
	_, d := newHarness("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if code := run(ctx, []string{"doctor"}, d); code != exitInterrupted {
		t.Errorf("exit code = %d, want %d", code, exitInterrupted)
	}
}
