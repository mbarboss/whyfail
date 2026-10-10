package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/netguard"
)

type fakeServer struct {
	version    string
	versionErr error
	hasModel   bool
	modelErr   error
	modelCalls int
}

func (f *fakeServer) Version(context.Context) (string, error) { return f.version, f.versionErr }

func (f *fakeServer) HasModel(context.Context) (bool, error) {
	f.modelCalls++
	return f.hasModel, f.modelErr
}

// healthy returns deps for a machine where every check passes.
func healthy() (Deps, *fakeServer) {
	srv := &fakeServer{version: "0.40.1", hasModel: true}
	host, _ := url.Parse("http://127.0.0.1:11434")
	return Deps{
		Host:        host,
		Model:       "gemma4:e4b",
		GOOS:        "linux",
		Environment: "Linux (Ubuntu 24.04.1 LTS), amd64, shell bash",
		CheckHost:   func(context.Context) error { return nil },
		LookPath:    func(string) (string, error) { return "/usr/local/bin/ollama", nil },
		Server:      srv,
	}, srv
}

func find(t *testing.T, results []Result, prefix string) Result {
	t.Helper()
	for _, r := range results {
		if strings.HasPrefix(r.Title, prefix) {
			return r
		}
	}
	t.Fatalf("no result starting with %q in %+v", prefix, results)
	return Result{}
}

func TestRunAllChecksPass(t *testing.T) {
	d, _ := healthy()

	results := Run(context.Background(), d)

	if !Passed(results) {
		t.Fatalf("Passed = false: %+v", results)
	}
	want := []Result{
		{Status: OK, Title: "Ollama host is on this machine (http://127.0.0.1:11434)"},
		{Status: OK, Title: "Ollama 0.40.1 is running"},
		{Status: OK, Title: "Model gemma4:e4b is installed"},
		{Status: Info, Title: "Environment: Linux (Ubuntu 24.04.1 LTS), amd64, shell bash"},
	}
	if fmt.Sprint(results) != fmt.Sprint(want) {
		t.Errorf("results =\n%+v\nwant\n%+v", results, want)
	}
}

func TestRunRemoteHostRefused(t *testing.T) {
	d, srv := healthy()
	d.Host, _ = url.Parse("http://192.168.1.10:11434")
	d.CheckHost = func(context.Context) error { return fmt.Errorf("check: %w", netguard.ErrNotLoopback) }

	results := Run(context.Background(), d)

	if Passed(results) {
		t.Error("Passed = true with a refused host")
	}
	host := find(t, results, "Ollama host")
	if host.Status != Fail || !strings.Contains(host.Fix, "--allow-remote") || !strings.Contains(host.Fix, "127.0.0.1") {
		t.Errorf("host result = %+v", host)
	}
	if srv.modelCalls != 0 {
		t.Error("contacted a refused host")
	}
	for _, r := range results[1:3] {
		if r.Status != Skip {
			t.Errorf("dependent check not skipped: %+v", r)
		}
	}
}

func TestRunUnresolvableHost(t *testing.T) {
	d, _ := healthy()
	d.CheckHost = func(context.Context) error { return fmt.Errorf("check: %w", netguard.ErrUnresolved) }

	host := find(t, Run(context.Background(), d), "Ollama host")

	if host.Status != Fail || !strings.Contains(host.Title, "cannot be resolved") {
		t.Errorf("host result = %+v", host)
	}
}

func TestRunRemoteHostAllowedWarns(t *testing.T) {
	d, _ := healthy()
	d.AllowRemote = true
	d.CheckHost = func(context.Context) error {
		t.Error("loopback check ran despite --allow-remote")
		return nil
	}

	results := Run(context.Background(), d)

	if host := find(t, results, "Remote Ollama host"); host.Status != Warn {
		t.Errorf("host result = %+v", host)
	}
	if !Passed(results) {
		t.Error("a warning should not fail the run")
	}
}

func TestRunOllamaNotRunning(t *testing.T) {
	d, srv := healthy()
	srv.versionErr = fmt.Errorf("get: %w", llm.ErrUnreachable)

	results := Run(context.Background(), d)

	r := find(t, results, "Ollama is not reachable")
	if r.Status != Fail || !strings.Contains(r.Fix, "ollama serve") {
		t.Errorf("result = %+v", r)
	}
	if model := find(t, results, "Model"); model.Status != Skip {
		t.Errorf("model check not skipped: %+v", model)
	}
	if srv.modelCalls != 0 {
		t.Error("model checked on an unreachable server")
	}
}

func TestRunOllamaNotInstalledShowsInstallForEachOS(t *testing.T) {
	tests := map[string]string{
		"linux":   "curl -fsSL https://ollama.com/install.sh | sh",
		"darwin":  "brew install ollama",
		"windows": "winget install Ollama.Ollama",
		"freebsd": "https://ollama.com/download",
	}
	for goos, want := range tests {
		t.Run(goos, func(t *testing.T) {
			d, srv := healthy()
			d.GOOS = goos
			srv.versionErr = fmt.Errorf("get: %w", llm.ErrUnreachable)
			d.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }

			r := find(t, Run(context.Background(), d), "Ollama is not installed")

			if r.Status != Fail || !strings.Contains(r.Fix, want) {
				t.Errorf("result = %+v, want fix with %q", r, want)
			}
		})
	}
}

func TestRunRemoteUnreachableDoesNotSuggestInstall(t *testing.T) {
	d, srv := healthy()
	d.AllowRemote = true
	d.Host, _ = url.Parse("http://192.168.1.10:11434")
	srv.versionErr = fmt.Errorf("get: %w", llm.ErrUnreachable)
	d.LookPath = func(string) (string, error) {
		t.Error("looked for a local install while using a remote host")
		return "", exec.ErrNotFound
	}

	r := find(t, Run(context.Background(), d), "Ollama is not reachable")

	if r.Status != Fail || !strings.Contains(r.Fix, "192.168.1.10") {
		t.Errorf("result = %+v", r)
	}
}

func TestRunOllamaTimeout(t *testing.T) {
	d, srv := healthy()
	srv.versionErr = fmt.Errorf("get: %w", llm.ErrTimeout)

	r := find(t, Run(context.Background(), d), "Ollama did not answer")

	if r.Status != Fail {
		t.Errorf("result = %+v", r)
	}
}

func TestRunOllamaOtherError(t *testing.T) {
	d, srv := healthy()
	srv.versionErr = errors.New("ollama: server returned status 500: boom")

	r := find(t, Run(context.Background(), d), "Ollama returned an error")

	if r.Status != Fail || !strings.Contains(r.Title, "status 500: boom") {
		t.Errorf("result = %+v", r)
	}
}

func TestRunVersionTooOld(t *testing.T) {
	tests := map[string]string{
		"linux":   "curl -fsSL https://ollama.com/install.sh | sh",
		"darwin":  "brew upgrade ollama",
		"windows": "winget upgrade Ollama.Ollama",
	}
	for goos, want := range tests {
		t.Run(goos, func(t *testing.T) {
			d, srv := healthy()
			d.GOOS = goos
			srv.version = "0.6.8"

			results := Run(context.Background(), d)

			r := find(t, results, "Ollama 0.6.8 is too old")
			if r.Status != Fail || !strings.Contains(r.Title, MinVersion) || !strings.Contains(r.Fix, want) {
				t.Errorf("result = %+v", r)
			}
			if find(t, results, "Model").Status != OK {
				t.Error("model check should still run on an old server")
			}
		})
	}
}

func TestRunVersionComparison(t *testing.T) {
	tests := []struct {
		version string
		want    Status
	}{
		{"0.9.0", OK},
		{"0.10.0", OK},
		{"1.0.0", OK},
		{"0.40.1-rc2", OK},
		{"v0.12.3", OK},
		{"0.8.9", Fail},
		{"0.9.0-rc1", Fail},
		{"0.0.0", Warn},
		{"banana", Warn},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			d, srv := healthy()
			srv.version = tt.version

			if got := Run(context.Background(), d)[1].Status; got != tt.want {
				t.Errorf("status = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunModelMissing(t *testing.T) {
	d, srv := healthy()
	srv.hasModel = false

	results := Run(context.Background(), d)

	r := find(t, results, "Model gemma4:e4b is not installed")
	if r.Status != Fail || r.Fix != "ollama pull gemma4:e4b" {
		t.Errorf("result = %+v", r)
	}
	if Passed(results) {
		t.Error("Passed = true with a missing model")
	}
}

func TestRunModelCheckError(t *testing.T) {
	d, srv := healthy()
	srv.modelErr = fmt.Errorf("show: %w", llm.ErrTimeout)

	if r := find(t, Run(context.Background(), d), "Could not check model"); r.Status != Fail {
		t.Errorf("result = %+v", r)
	}
}

func TestRunBoundsEachServerCall(t *testing.T) {
	d, _ := healthy()
	var deadlines []time.Duration
	d.Server = deadlineServer{record: func(ctx context.Context) {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Error("server call without a deadline")
			return
		}
		deadlines = append(deadlines, time.Until(dl))
	}}

	Run(context.Background(), d)

	if len(deadlines) != 2 {
		t.Fatalf("server calls = %d, want 2", len(deadlines))
	}
	for _, dl := range deadlines {
		if dl > CheckTimeout {
			t.Errorf("deadline %v exceeds %v", dl, CheckTimeout)
		}
	}
}

type deadlineServer struct{ record func(context.Context) }

func (s deadlineServer) Version(ctx context.Context) (string, error) {
	s.record(ctx)
	return "0.40.1", nil
}

func (s deadlineServer) HasModel(ctx context.Context) (bool, error) {
	s.record(ctx)
	return true, nil
}

func TestStatusString(t *testing.T) {
	for s, want := range map[Status]string{OK: "ok", Warn: "warn", Fail: "FAIL", Skip: "skip", Info: "info", Status(99): "unknown"} {
		if got := s.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", s, got, want)
		}
	}
}
