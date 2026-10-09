// Command task is the project's cross-platform task runner.
//
// Usage, from the repository root:
//
//	go run ./tools/task <target> [args...]
//
// Each development tool is pinned in its own module under tools/<name> so that
// their dependency graphs never get merged with each other or with the app.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	// coverageFloor is the minimum total statement coverage enforced by check.
	coverageFloor = 80.0
	// maxFileBytes is the largest file the pre-commit hook accepts.
	maxFileBytes = 1 << 20
	modulePath   = "github.com/mbarboss/whyfail"
)

type target struct {
	desc string
	run  func(ctx context.Context, args []string) error
}

func targets() map[string]target {
	return map[string]target{
		"format":      {"format all Go code", noArgs(func(ctx context.Context) error { return tool(ctx, "golangci-lint", "fmt") })},
		"lint":        {"run linters", noArgs(lint)},
		"test":        {"run unit tests and enforce the coverage floor", noArgs(test)},
		"integration": {"run opt-in integration tests (needs a running Ollama)", noArgs(integration)},
		"vuln":        {"scan dependencies for known vulnerabilities", noArgs(vuln)},
		"secrets":     {"scan git history for secrets", noArgs(secrets)},
		"check":       {"tidy, format check, lint, test, vuln and secrets", noArgs(check)},
		"hooks":       {"install git hooks", noArgs(func(ctx context.Context) error { return tool(ctx, "lefthook", "install") })},
		"large-files": {"fail if any given file exceeds 1 MiB", func(_ context.Context, paths []string) error { return largeFiles(paths) }},
	}
}

func main() {
	all := targets()
	if len(os.Args) < 2 {
		usage(all)
		os.Exit(2)
	}
	name := os.Args[1]
	t, ok := all[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown target %q\n\n", name)
		usage(all)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := t.run(ctx, os.Args[2:])
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "task %s: %v\n", name, err)
		os.Exit(1)
	}
}

func usage(all map[string]target) {
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintln(os.Stderr, "usage: go run ./tools/task <target> [args...]")
	fmt.Fprintln(os.Stderr, "\ntargets:")
	for _, name := range names {
		fmt.Fprintf(os.Stderr, "  %-12s %s\n", name, all[name].desc)
	}
}

func noArgs(f func(ctx context.Context) error) func(context.Context, []string) error {
	return func(ctx context.Context, _ []string) error { return f(ctx) }
}

func lint(ctx context.Context) error { return tool(ctx, "golangci-lint", "run") }
func vuln(ctx context.Context) error { return tool(ctx, "govulncheck", "./...") }
func secrets(ctx context.Context) error {
	return tool(ctx, "gitleaks", "git", "--redact", "--no-banner")
}

func check(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{"tidy", func(ctx context.Context) error { return goCmd(ctx, "mod", "tidy", "-diff") }},
		{"format", func(ctx context.Context) error { return tool(ctx, "golangci-lint", "fmt", "--diff") }},
		{"lint", lint},
		{"test", test},
		{"vuln", vuln},
		{"secrets", secrets},
	}
	for _, s := range steps {
		fmt.Fprintf(os.Stderr, "==> %s\n", s.name)
		if err := s.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

func test(ctx context.Context) (err error) {
	pkgs, err := appPackages(ctx)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "whyfail-cover-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(dir); rmErr != nil && err == nil {
			err = fmt.Errorf("remove temp dir: %w", rmErr)
		}
	}()
	profile := filepath.Join(dir, "cover.out")

	args := append([]string{"test", "-shuffle=on", "-coverprofile=" + profile}, pkgs...)
	if err := goCmd(ctx, args...); err != nil {
		return err
	}
	total, err := totalCoverage(ctx, profile)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "total coverage: %.1f%% (floor %.1f%%)\n", total, coverageFloor)
	if total < coverageFloor {
		return fmt.Errorf("coverage %.1f%% is below the %.1f%% floor", total, coverageFloor)
	}
	return nil
}

func integration(ctx context.Context) error {
	pkgs, err := appPackages(ctx)
	if err != nil {
		return err
	}
	return goCmd(ctx, append([]string{"test", "-tags=integration", "-count=1"}, pkgs...)...)
}

// appPackages lists the application's packages, leaving out the dev tooling
// so that it does not count toward coverage.
func appPackages(ctx context.Context) ([]string, error) {
	out, err := goOutput(ctx, "list", "./...")
	if err != nil {
		return nil, err
	}
	var pkgs []string
	for _, p := range strings.Fields(out) {
		if !strings.HasPrefix(p, modulePath+"/tools/") {
			pkgs = append(pkgs, p)
		}
	}
	if len(pkgs) == 0 {
		return nil, errors.New("no application packages found")
	}
	return pkgs, nil
}

func totalCoverage(ctx context.Context, profile string) (float64, error) {
	out, err := goOutput(ctx, "tool", "cover", "-func="+profile)
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 0 && fields[0] == "total:" {
			pct := strings.TrimSuffix(fields[len(fields)-1], "%")
			v, err := strconv.ParseFloat(pct, 64)
			if err != nil {
				return 0, fmt.Errorf("parse coverage %q: %w", pct, err)
			}
			return v, nil
		}
	}
	return 0, errors.New("coverage total not found")
}

func largeFiles(paths []string) error {
	var tooBig []string
	for _, p := range paths {
		// Paths come from git's list of staged files, not from untrusted input.
		info, err := os.Stat(p) //nolint:gosec // G703: see comment above.
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
		if info.Size() > maxFileBytes {
			tooBig = append(tooBig, fmt.Sprintf("%s (%d bytes)", p, info.Size()))
		}
	}
	if len(tooBig) > 0 {
		return fmt.Errorf("files larger than %d bytes: %s", maxFileBytes, strings.Join(tooBig, ", "))
	}
	return nil
}

// tool runs a pinned dev tool through its own modfile.
func tool(ctx context.Context, name string, args ...string) error {
	modfile := filepath.Join("tools", name, "go.mod")
	return goCmd(ctx, append([]string{"tool", "-modfile=" + modfile, name}, args...)...)
}

func goCmd(ctx context.Context, args ...string) error {
	cmd, err := goCommand(ctx, args...)
	if err != nil {
		return err
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go %s: %w", args[0], err)
	}
	return nil
}

func goOutput(ctx context.Context, args ...string) (string, error) {
	cmd, err := goCommand(ctx, args...)
	if err != nil {
		return "", err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

func goCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	bin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("go toolchain not found in PATH: %w", err)
	}
	return exec.CommandContext(ctx, bin, args...), nil
}
