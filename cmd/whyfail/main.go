// Command whyfail explains why a terminal command failed and suggests a fix,
// using a local LLM through Ollama.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/mbarboss/whyfail/internal/apply"
	"github.com/mbarboss/whyfail/internal/capture"
	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/doctor"
	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/llm/ollama"
	"github.com/mbarboss/whyfail/internal/netguard"
	"github.com/mbarboss/whyfail/internal/prompt"
	"github.com/mbarboss/whyfail/internal/redact"
	"github.com/mbarboss/whyfail/internal/render"
	"github.com/mbarboss/whyfail/internal/sysinfo"
)

// Exit codes shared by every whyfail mode. In wrapper mode whyfail exits with
// the command's own code once the command has run.
const (
	exitOK          = 0
	exitFailure     = 1
	exitUsage       = 2
	exitCannotRun   = 126
	exitNotFound    = 127
	exitInterrupted = 130
)

const (
	pipeUsage        = "  <command> 2>&1 | whyfail"
	wrapUsage        = "  whyfail -- <command> [args]"
	progressInterval = 100 * time.Millisecond
	// resolveTimeout bounds the startup check that the host is loopback.
	resolveTimeout = 2 * time.Second
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// deps holds everything run touches outside its arguments, so tests can fake it.
type deps struct {
	stdin            io.Reader
	stdout, stderr   io.Writer
	getenv           func(string) string
	stdinIsTerminal  bool
	stderrIsTerminal bool
	newExplainer     func(config.Config) llm.Explainer
	resolver         netguard.Resolver
	probe            sysinfo.Probe
	newServer        func(config.Config) doctor.Server
	lookPath         func(string) (string, error)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], deps{
		stdin:            os.Stdin,
		stdout:           os.Stdout,
		stderr:           os.Stderr,
		getenv:           os.Getenv,
		stdinIsTerminal:  isTerminal(os.Stdin),
		stderrIsTerminal: isTerminal(os.Stderr),
		newExplainer:     func(cfg config.Config) llm.Explainer { return newOllama(cfg) },
		newServer:        func(cfg config.Config) doctor.Server { return newOllama(cfg) },
		lookPath:         exec.LookPath,
		resolver:         net.DefaultResolver,
		probe:            sysinfo.System(os.Getenv),
	})
	stop()
	os.Exit(code)
}

// newOllama builds the production Explainer. Proxies are disabled so the
// captured output only ever goes to the configured host, and unless remote
// hosts are allowed every connection must reach a loopback address.
func newOllama(cfg config.Config) *ollama.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	if !cfg.AllowRemote {
		dialer.Control = netguard.Control
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext
	return ollama.New(cfg.Host, cfg.Model, &http.Client{Transport: transport})
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// run executes the requested mode and returns the process exit code.
func run(ctx context.Context, args []string, d deps) int {
	cfg, err := config.Parse(args, d.getenv, d.stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, config.ErrInvalid):
		fmt.Fprintf(d.stderr, "whyfail: %v\n", err)
		return exitUsage
	case err != nil:
		// The flag package has already printed the error and the usage.
		return exitUsage
	}

	if cfg.ShowVersion {
		fmt.Fprintf(d.stdout, "whyfail %s\n", version)
		return exitOK
	}
	if cfg.Doctor {
		return runDoctor(ctx, cfg, d)
	}
	if len(cfg.Command) > 0 {
		// Check the host first so a refused host never costs a full run.
		if code := checkHost(ctx, cfg, d); code != exitOK {
			return code
		}
		return explainCommand(ctx, cfg, d)
	}
	if len(cfg.Args) > 0 {
		fmt.Fprintf(d.stderr, "whyfail: unexpected arguments. Put the command after --, or pipe its output:\n%s\n%s\n", wrapUsage, pipeUsage)
		return exitUsage
	}
	if d.stdinIsTerminal {
		fmt.Fprintf(d.stderr, "whyfail: nothing to explain. Pipe the failed command's output into whyfail:\n%s\n", pipeUsage)
		return exitUsage
	}

	if code := checkHost(ctx, cfg, d); code != exitOK {
		return code
	}
	return explainPipe(ctx, cfg, d)
}

// runDoctor checks that Ollama and the model are ready. A refused host is a
// failed check here, not a usage error.
func runDoctor(ctx context.Context, cfg config.Config, d deps) int {
	results := doctor.Run(ctx, doctor.Deps{
		Host:        cfg.Host,
		Model:       cfg.Model,
		AllowRemote: cfg.AllowRemote,
		GOOS:        d.probe.GOOS,
		Environment: sysinfo.Detect(d.probe, cfg.Shell).String(),
		CheckHost: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
			defer cancel()
			return netguard.CheckHost(ctx, d.resolver, cfg.Host.Hostname())
		},
		LookPath: d.lookPath,
		Server:   d.newServer(cfg),
	})
	if ctx.Err() != nil {
		fmt.Fprintln(d.stderr, "whyfail: interrupted")
		return exitInterrupted
	}
	if err := render.Doctor(d.stdout, results); err != nil {
		fmt.Fprintf(d.stderr, "whyfail: write output: %v\n", err)
		return exitFailure
	}
	if !doctor.Passed(results) {
		return exitFailure
	}
	return exitOK
}

// checkHost refuses a host that is not on this machine unless --allow-remote
// was passed, in which case it warns on every run.
func checkHost(ctx context.Context, cfg config.Config, d deps) int {
	if cfg.AllowRemote {
		transport := ""
		if cfg.Host.Scheme == "http" {
			transport = " over unencrypted HTTP"
		}
		fmt.Fprintf(d.stderr, "whyfail: warning: --allow-remote is set; command output is sent to %s%s.\n", cfg.Host, transport)
		return exitOK
	}

	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	err := netguard.CheckHost(ctx, d.resolver, cfg.Host.Hostname())
	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, netguard.ErrUnresolved):
		fmt.Fprintf(d.stderr, "whyfail: cannot resolve %s to check that it is on this machine. Use an address such as %s, or pass --allow-remote.\n", cfg.Host, config.DefaultHost)
	default:
		fmt.Fprintf(d.stderr, "whyfail: refusing to send output to %s: it is not a loopback address. Pass --allow-remote to allow it.\n", cfg.Host)
	}
	return exitUsage
}

func explainPipe(ctx context.Context, cfg config.Config, d deps) int {
	tail, err := capture.ReadTail(d.stdin, capture.DefaultLimits)
	if errors.Is(err, capture.ErrEmpty) {
		fmt.Fprintf(d.stderr, "whyfail: the input was empty. Remember to include stderr:\n%s\n", pipeUsage)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintf(d.stderr, "whyfail: %v\n", err)
		return exitFailure
	}
	return explain(ctx, cfg, d, prompt.Failure{Output: tail.Text, Truncated: tail.Truncated})
}

// explainCommand runs the wrapped command and explains it only when it fails.
// Once the command has run, whyfail exits with its code even when the
// explanation fails, so wrapping a command never changes what scripts see.
func explainCommand(ctx context.Context, cfg config.Config, d deps) int {
	res, err := capture.Run(cfg.Command, d.stdin, d.stdout, d.stderr, capture.DefaultLimits)
	switch {
	case errors.Is(err, capture.ErrNotFound):
		fmt.Fprintf(d.stderr, "whyfail: command not found: %s\n", render.Sanitize(cfg.Command[0]))
		return exitNotFound
	case err != nil:
		fmt.Fprintf(d.stderr, "whyfail: %s\n", render.Sanitize(err.Error()))
		if errors.Is(err, capture.ErrCannotRun) {
			return exitCannotRun
		}
		return exitFailure
	}

	switch {
	case ctx.Err() != nil:
		// Interrupted: the user already knows why the command stopped.
		return res.ExitCode
	case res.ExitCode == 0:
		return exitOK
	case res.Tail.Text == "":
		fmt.Fprintf(d.stderr, "whyfail: the command exited with code %d without any output to explain.\n", res.ExitCode)
		return res.ExitCode
	}

	explain(ctx, cfg, d, prompt.Failure{
		Command:   formatCommand(cfg.Command),
		ExitCode:  res.ExitCode,
		Output:    res.Tail.Text,
		Truncated: res.Tail.Truncated,
	})
	return res.ExitCode
}

// formatCommand joins argv for display, quoting arguments that would not
// survive a plain space-separated join.
func formatCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\") {
			a = strconv.Quote(a)
		}
		parts[i] = a
	}
	return strings.Join(parts, " ")
}

// explain redacts f, asks the model and prints the answer. It returns exitOK,
// or the code for the error it reported.
func explain(ctx context.Context, cfg config.Config, d deps, f prompt.Failure) int {
	command, output := redact.Redact(f.Command), redact.Redact(f.Output)
	reportRedaction(d.stderr, command, output)
	f.Command, f.Output = command.Text, output.Text
	info := sysinfo.Detect(d.probe, cfg.Shell)
	f.Environment = info.String()
	if info.Shell != sysinfo.Unknown {
		f.Shell = info.Shell.String()
	}

	req := prompt.Build(f)
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	stopProgress := func() {}
	if d.stderrIsTerminal {
		stopProgress = render.StartProgress(d.stderr, "Asking "+cfg.Model, progressInterval)
	}
	answer, err := d.newExplainer(cfg).Explain(ctx, req)
	stopProgress()
	if err != nil {
		return reportExplainError(d.stderr, cfg, err)
	}

	if err := render.Text(d.stdout, answer, apply.Warnings); err != nil {
		fmt.Fprintf(d.stderr, "whyfail: write output: %v\n", err)
		return exitFailure
	}
	return exitOK
}

// reportRedaction tells the user that secrets were removed, naming only their
// kinds.
func reportRedaction(w io.Writer, results ...redact.Result) {
	r := redact.Result{Counts: map[redact.Kind]int{}}
	for _, res := range results {
		for k, c := range res.Counts {
			r.Counts[k] += c
		}
	}
	n := r.Total()
	if n == 0 {
		return
	}
	noun := "secrets"
	if n == 1 {
		noun = "secret"
	}
	kinds := make([]string, 0, len(r.Counts))
	for _, k := range r.Kinds() {
		kinds = append(kinds, string(k))
	}
	fmt.Fprintf(w, "whyfail: redacted %d %s (%s) before asking the model.\n", n, noun, strings.Join(kinds, ", "))
}

// reportExplainError prints an actionable message for err. Messages never
// include the captured output.
func reportExplainError(w io.Writer, cfg config.Config, err error) int {
	switch {
	case errors.Is(err, context.Canceled):
		fmt.Fprintln(w, "whyfail: interrupted")
		return exitInterrupted
	case errors.Is(err, netguard.ErrNotLoopback):
		fmt.Fprintln(w, "whyfail: refused to connect to a non-loopback address; the server may have redirected the request. Pass --allow-remote to allow it.")
	case errors.Is(err, llm.ErrUnreachable):
		fmt.Fprintf(w, "whyfail: cannot reach Ollama at %s. Start it with `ollama serve` (or the Ollama app) and try again.\n", cfg.Host)
	case errors.Is(err, llm.ErrModelNotFound):
		fmt.Fprintf(w, "whyfail: model %s is not installed. Install it with:\n  ollama pull %s\n", cfg.Model, cfg.Model)
	case errors.Is(err, llm.ErrTimeout):
		fmt.Fprintf(w, "whyfail: no answer within %v. The first request can be slow while the model loads; try again or raise -timeout.\n", cfg.Timeout)
	case errors.Is(err, llm.ErrMalformedResponse):
		fmt.Fprintln(w, "whyfail: the model gave an unusable answer. Try again, or pick another model with -model.")
	default:
		fmt.Fprintf(w, "whyfail: %s\n", render.Sanitize(err.Error()))
	}
	return exitFailure
}
