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

// failure is an error reported to the user. code is the stable JSON
// error.code; message is shown after "whyfail: " in text mode and must never
// hold captured output.
type failure struct {
	code, message string
	// quiet failures print nothing in text mode, because the user has already
	// seen why (an interrupt, or a flag error printed by the flag package).
	quiet bool
}

// outcome is what a run produced. finish prints it in the selected format.
type outcome struct {
	exit   int
	answer *llm.Explanation
	fail   *failure
	// command and childExit are set in wrapper mode once the command ran.
	command   *string
	childExit *int
}

// run executes the requested mode and returns the process exit code.
func run(ctx context.Context, args []string, d deps) int {
	cfg, err := config.Parse(args, d.getenv, d.stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	case errors.Is(err, config.ErrInvalid):
		return finish(cfg, d, outcome{exit: exitUsage, fail: &failure{code: "invalid_config", message: err.Error()}})
	case err != nil:
		// The flag package has already printed the error and the usage.
		return finish(cfg, d, outcome{exit: exitUsage, fail: &failure{code: "usage", message: err.Error(), quiet: true}})
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
		if f := checkHost(ctx, cfg, d); f != nil {
			return finish(cfg, d, outcome{exit: exitUsage, fail: f})
		}
		return finish(cfg, d, explainCommand(ctx, cfg, d))
	}
	if len(cfg.Args) > 0 {
		return finish(cfg, d, outcome{exit: exitUsage, fail: &failure{
			code:    "usage",
			message: fmt.Sprintf("unexpected arguments. Put the command after --, or pipe its output:\n%s\n%s", wrapUsage, pipeUsage),
		}})
	}
	if d.stdinIsTerminal {
		return finish(cfg, d, outcome{exit: exitUsage, fail: &failure{
			code:    "usage",
			message: "nothing to explain. Pipe the failed command's output into whyfail:\n" + pipeUsage,
		}})
	}

	if f := checkHost(ctx, cfg, d); f != nil {
		return finish(cfg, d, outcome{exit: exitUsage, fail: f})
	}
	return finish(cfg, d, explainPipe(ctx, cfg, d))
}

// finish prints o as text or JSON and returns the exit code.
func finish(cfg config.Config, d deps, o outcome) int {
	if cfg.JSON {
		r := render.Report{Model: cfg.Model, Command: o.command, ExitCode: o.childExit, Answer: o.answer}
		if o.fail != nil {
			r.Error = &render.ReportError{Code: o.fail.code, Message: o.fail.message}
		}
		if err := render.JSON(d.stdout, r, apply.Warnings); err != nil {
			return writeFailed(d, o, err)
		}
		return o.exit
	}

	if o.fail != nil && !o.fail.quiet {
		fmt.Fprintf(d.stderr, "whyfail: %s\n", o.fail.message)
	}
	if o.answer != nil {
		if err := render.Text(d.stdout, *o.answer, apply.Warnings); err != nil {
			return writeFailed(d, o, err)
		}
	}
	return o.exit
}

// writeFailed reports that the result could not be printed. In wrapper mode
// the command's exit code still wins.
func writeFailed(d deps, o outcome, err error) int {
	fmt.Fprintf(d.stderr, "whyfail: write output: %v\n", err)
	if o.childExit != nil {
		return o.exit
	}
	return exitFailure
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
		return finish(cfg, d, outcome{exit: exitInterrupted, fail: &failure{code: "interrupted", message: "interrupted"}})
	}

	write := render.Doctor
	if cfg.JSON {
		write = render.DoctorJSON
	}
	if err := write(d.stdout, results); err != nil {
		return writeFailed(d, outcome{}, err)
	}
	if !doctor.Passed(results) {
		return exitFailure
	}
	return exitOK
}

// checkHost refuses a host that is not on this machine unless --allow-remote
// was passed, in which case it warns on every run. It returns nil when the
// host may be used.
func checkHost(ctx context.Context, cfg config.Config, d deps) *failure {
	if cfg.AllowRemote {
		transport := ""
		if cfg.Host.Scheme == "http" {
			transport = " over unencrypted HTTP"
		}
		fmt.Fprintf(d.stderr, "whyfail: warning: --allow-remote is set; command output is sent to %s%s.\n", cfg.Host, transport)
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	err := netguard.CheckHost(ctx, d.resolver, cfg.Host.Hostname())
	switch {
	case err == nil:
		return nil
	case errors.Is(err, netguard.ErrUnresolved):
		return &failure{
			code:    "host_unresolved",
			message: fmt.Sprintf("cannot resolve %s to check that it is on this machine. Use an address such as %s, or pass --allow-remote.", cfg.Host, config.DefaultHost),
		}
	default:
		return &failure{
			code:    "host_refused",
			message: fmt.Sprintf("refusing to send output to %s: it is not a loopback address. Pass --allow-remote to allow it.", cfg.Host),
		}
	}
}

func explainPipe(ctx context.Context, cfg config.Config, d deps) outcome {
	tail, err := capture.ReadTail(d.stdin, capture.DefaultLimits)
	if errors.Is(err, capture.ErrEmpty) {
		return outcome{exit: exitUsage, fail: &failure{
			code:    "empty_input",
			message: "the input was empty. Remember to include stderr:\n" + pipeUsage,
		}}
	}
	if err != nil {
		return outcome{exit: exitFailure, fail: &failure{code: "internal", message: err.Error()}}
	}
	return explain(ctx, cfg, d, prompt.Failure{Output: tail.Text, Truncated: tail.Truncated})
}

// explainCommand runs the wrapped command and explains it only when it fails.
// Once the command has run, whyfail exits with its code even when the
// explanation fails, so wrapping a command never changes what scripts see.
// In JSON mode the command's output goes to stderr, keeping stdout for the
// JSON object.
func explainCommand(ctx context.Context, cfg config.Config, d deps) outcome {
	stdout := d.stdout
	if cfg.JSON {
		stdout = d.stderr
	}
	res, err := capture.Run(cfg.Command, d.stdin, stdout, d.stderr, capture.DefaultLimits)
	switch {
	case errors.Is(err, capture.ErrNotFound):
		return outcome{exit: exitNotFound, fail: &failure{code: "command_not_found", message: "command not found: " + render.Sanitize(cfg.Command[0])}}
	case errors.Is(err, capture.ErrCannotRun):
		return outcome{exit: exitCannotRun, fail: &failure{code: "cannot_run", message: render.Sanitize(err.Error())}}
	case err != nil:
		return outcome{exit: exitFailure, fail: &failure{code: "internal", message: render.Sanitize(err.Error())}}
	}

	command := redact.Redact(formatCommand(cfg.Command)).Text
	ran := outcome{exit: res.ExitCode, command: &command, childExit: &res.ExitCode}
	switch {
	case ctx.Err() != nil:
		// Interrupted: the user already knows why the command stopped.
		ran.fail = &failure{code: "interrupted", message: "interrupted", quiet: true}
		return ran
	case res.ExitCode == 0:
		return ran
	case res.Tail.Text == "":
		ran.fail = &failure{code: "no_output", message: fmt.Sprintf("the command exited with code %d without any output to explain.", res.ExitCode)}
		return ran
	}

	o := explain(ctx, cfg, d, prompt.Failure{
		Command:   formatCommand(cfg.Command),
		ExitCode:  res.ExitCode,
		Output:    res.Tail.Text,
		Truncated: res.Tail.Truncated,
	})
	o.exit, o.command, o.childExit = res.ExitCode, &command, &res.ExitCode
	return o
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

// explain redacts f and asks the model. The outcome holds the answer, or the
// failure with its exit code.
func explain(ctx context.Context, cfg config.Config, d deps, f prompt.Failure) outcome {
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
	if d.stderrIsTerminal && !cfg.JSON {
		stopProgress = render.StartProgress(d.stderr, "Asking "+cfg.Model, progressInterval)
	}
	answer, err := d.newExplainer(cfg).Explain(ctx, req)
	stopProgress()
	if err != nil {
		return explainFailure(cfg, err)
	}
	return outcome{exit: exitOK, answer: &answer}
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

// explainFailure maps an Explainer error to an actionable failure. Messages
// never include the captured output.
func explainFailure(cfg config.Config, err error) outcome {
	f := func(code, message string) outcome {
		return outcome{exit: exitFailure, fail: &failure{code: code, message: message}}
	}
	switch {
	case errors.Is(err, context.Canceled):
		return outcome{exit: exitInterrupted, fail: &failure{code: "interrupted", message: "interrupted"}}
	case errors.Is(err, netguard.ErrNotLoopback):
		return f("host_refused", "refused to connect to a non-loopback address; the server may have redirected the request. Pass --allow-remote to allow it.")
	case errors.Is(err, llm.ErrUnreachable):
		return f("ollama_unreachable", fmt.Sprintf("cannot reach Ollama at %s. Start it with `ollama serve` (or the Ollama app) and try again.", cfg.Host))
	case errors.Is(err, llm.ErrModelNotFound):
		return f("model_not_found", fmt.Sprintf("model %s is not installed. Install it with:\n  ollama pull %s", cfg.Model, cfg.Model))
	case errors.Is(err, llm.ErrTimeout):
		return f("timeout", fmt.Sprintf("no answer within %v. The first request can be slow while the model loads; try again or raise -timeout.", cfg.Timeout))
	case errors.Is(err, llm.ErrMalformedResponse):
		return f("malformed_answer", "the model gave an unusable answer. Try again, or pick another model with -model.")
	default:
		return f("internal", render.Sanitize(err.Error()))
	}
}
