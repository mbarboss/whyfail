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
	"os/signal"
	"strings"
	"time"

	"github.com/mbarboss/whyfail/internal/apply"
	"github.com/mbarboss/whyfail/internal/capture"
	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/llm/ollama"
	"github.com/mbarboss/whyfail/internal/netguard"
	"github.com/mbarboss/whyfail/internal/prompt"
	"github.com/mbarboss/whyfail/internal/redact"
	"github.com/mbarboss/whyfail/internal/render"
)

// Exit codes shared by every whyfail mode.
const (
	exitOK          = 0
	exitFailure     = 1
	exitUsage       = 2
	exitInterrupted = 130
)

const (
	pipeUsage        = "  <command> 2>&1 | whyfail"
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
		newExplainer:     newOllama,
		resolver:         net.DefaultResolver,
	})
	stop()
	os.Exit(code)
}

// newOllama builds the production Explainer. Proxies are disabled so the
// captured output only ever goes to the configured host, and unless remote
// hosts are allowed every connection must reach a loopback address.
func newOllama(cfg config.Config) llm.Explainer {
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
	if len(cfg.Args) > 0 {
		fmt.Fprintf(d.stderr, "whyfail: unexpected arguments. Pipe the failed command's output instead:\n%s\n", pipeUsage)
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

	redacted := redact.Redact(tail.Text)
	reportRedaction(d.stderr, redacted)

	req := prompt.Build(prompt.Failure{Output: redacted.Text, Truncated: tail.Truncated})
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
func reportRedaction(w io.Writer, r redact.Result) {
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
