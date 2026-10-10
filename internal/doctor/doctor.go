// Package doctor checks that whyfail can reach a usable Ollama server and says
// how to fix what is missing.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/netguard"
)

// Status is the outcome of one check.
type Status int

// Check outcomes. Only Fail makes the run fail.
const (
	OK Status = iota
	Warn
	Fail
	Skip
	Info
)

// String returns the label printed before a result. Failures are upper case so
// they stand out without color.
func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Warn:
		return "warn"
	case Fail:
		return "FAIL"
	case Skip:
		return "skip"
	case Info:
		return "info"
	}
	return "unknown"
}

const (
	// MinVersion is the first Ollama release with the "think" request field,
	// which whyfail always sends.
	MinVersion = "0.9.0"
	// CheckTimeout bounds each call to the server.
	CheckTimeout = 5 * time.Second
)

// Result is one line of the report. Title and Fix may hold text from the
// server and must be sanitized before printing.
type Result struct {
	Status Status
	Title  string
	Fix    string
}

// Server is the part of the Ollama API that doctor checks.
type Server interface {
	Version(ctx context.Context) (string, error)
	HasModel(ctx context.Context) (bool, error)
}

// Deps is everything Run looks at.
type Deps struct {
	Host        *url.URL
	Model       string
	AllowRemote bool
	// GOOS selects the install and upgrade commands.
	GOOS string
	// Environment is the detected OS, architecture and shell.
	Environment string
	// CheckHost fails unless the host resolves only to loopback addresses.
	CheckHost func(context.Context) error
	LookPath  func(string) (string, error)
	Server    Server
}

// Run performs every check in order. A check whose prerequisite failed is
// reported as skipped.
func Run(ctx context.Context, d Deps) []Result {
	host := checkHost(ctx, d)
	results := []Result{host}
	if host.Status == Fail {
		results = append(results,
			Result{Status: Skip, Title: "Ollama server check skipped: the host was refused"},
			Result{Status: Skip, Title: "Model check skipped: the host was refused"})
	} else {
		server, answered := checkServer(ctx, d)
		results = append(results, server)
		if !answered {
			results = append(results, Result{Status: Skip, Title: "Model check skipped: Ollama is not available"})
		} else {
			results = append(results, checkModel(ctx, d))
		}
	}
	return append(results, Result{Status: Info, Title: "Environment: " + d.Environment})
}

// Passed reports whether no check failed.
func Passed(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return false
		}
	}
	return true
}

func checkHost(ctx context.Context, d Deps) Result {
	if d.AllowRemote {
		return Result{Status: Warn, Title: fmt.Sprintf("Remote Ollama host allowed (%s); command output may leave this machine", d.Host)}
	}
	err := d.CheckHost(ctx)
	switch {
	case err == nil:
		return Result{Status: OK, Title: fmt.Sprintf("Ollama host is on this machine (%s)", d.Host)}
	case errors.Is(err, netguard.ErrUnresolved):
		return Result{
			Status: Fail,
			Title:  fmt.Sprintf("Ollama host %s cannot be resolved", d.Host),
			Fix:    "Use an address such as http://127.0.0.1:11434 in OLLAMA_HOST or --host.",
		}
	default:
		return Result{
			Status: Fail,
			Title:  fmt.Sprintf("Ollama host %s is not on this machine", d.Host),
			Fix:    "Point OLLAMA_HOST or --host at http://127.0.0.1:11434, or pass --allow-remote to send output to that host.",
		}
	}
}

// checkServer reports the server state and whether it answered, which the
// model check needs even when the version is too old.
func checkServer(ctx context.Context, d Deps) (Result, bool) {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	version, err := d.Server.Version(ctx)
	switch {
	case errors.Is(err, llm.ErrUnreachable):
		return unreachable(d), false
	case errors.Is(err, llm.ErrTimeout):
		return Result{
			Status: Fail,
			Title:  fmt.Sprintf("Ollama did not answer at %s within %v", d.Host, CheckTimeout),
			Fix:    "Check that Ollama is not stuck, then try again.",
		}, false
	case err != nil:
		return Result{Status: Fail, Title: "Ollama returned an error: " + err.Error(), Fix: "Check the Ollama server logs."}, false
	}

	v, ok := parseVersion(version)
	switch {
	case !ok || v == (semver{}):
		// Development builds report 0.0.0.
		return Result{Status: Warn, Title: fmt.Sprintf("Ollama %s is running; its version cannot be checked", version)}, true
	case v.less(minVersion):
		return Result{
			Status: Fail,
			Title:  fmt.Sprintf("Ollama %s is too old; whyfail needs %s or later", version, MinVersion),
			Fix:    upgradeCommand(d.GOOS),
		}, true
	}
	return Result{Status: OK, Title: fmt.Sprintf("Ollama %s is running", version)}, true
}

// unreachable tells "not installed" from "not running" so the fix fits. A
// remote host is never installed locally.
func unreachable(d Deps) Result {
	if d.AllowRemote {
		return Result{
			Status: Fail,
			Title:  fmt.Sprintf("Ollama is not reachable at %s", d.Host),
			Fix:    fmt.Sprintf("Check that Ollama runs on %s and listens on its network address (OLLAMA_HOST=0.0.0.0 on that machine).", d.Host.Hostname()),
		}
	}
	if _, err := d.LookPath("ollama"); err != nil {
		return Result{Status: Fail, Title: "Ollama is not installed", Fix: installCommand(d.GOOS)}
	}
	return Result{
		Status: Fail,
		Title:  fmt.Sprintf("Ollama is not reachable at %s", d.Host),
		Fix:    "Start it with `ollama serve`, or open the Ollama app.",
	}
}

func checkModel(ctx context.Context, d Deps) Result {
	ctx, cancel := context.WithTimeout(ctx, CheckTimeout)
	defer cancel()
	ok, err := d.Server.HasModel(ctx)
	switch {
	case err != nil:
		return Result{Status: Fail, Title: fmt.Sprintf("Could not check model %s: %v", d.Model, err), Fix: "Try again; if it persists, check the Ollama server logs."}
	case !ok:
		return Result{Status: Fail, Title: fmt.Sprintf("Model %s is not installed", d.Model), Fix: "ollama pull " + d.Model}
	}
	return Result{Status: OK, Title: fmt.Sprintf("Model %s is installed", d.Model)}
}

func installCommand(goos string) string {
	switch goos {
	case "linux":
		return "curl -fsSL https://ollama.com/install.sh | sh"
	case "darwin":
		return "brew install ollama (or download the app from https://ollama.com/download)"
	case "windows":
		return "winget install Ollama.Ollama"
	default:
		return "See https://ollama.com/download"
	}
}

func upgradeCommand(goos string) string {
	switch goos {
	case "linux":
		return "curl -fsSL https://ollama.com/install.sh | sh"
	case "darwin":
		return "brew upgrade ollama (or update the app)"
	case "windows":
		return "winget upgrade Ollama.Ollama"
	default:
		return "See https://ollama.com/download"
	}
}

// semver is the numeric part of a version. A pre-release sorts before the
// release it precedes, so 0.9.0-rc1 does not satisfy 0.9.0.
type semver struct {
	major, minor, patch int
	pre                 bool
}

var minVersion, _ = parseVersion(MinVersion)

func parseVersion(s string) (semver, bool) {
	s = strings.TrimPrefix(s, "v")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return semver{}, false
		}
		n[i] = v
	}
	return semver{major: n[0], minor: n[1], patch: n[2], pre: pre != ""}, true
}

func (a semver) less(b semver) bool {
	switch {
	case a.major != b.major:
		return a.major < b.major
	case a.minor != b.minor:
		return a.minor < b.minor
	case a.patch != b.patch:
		return a.patch < b.patch
	default:
		return a.pre && !b.pre
	}
}
