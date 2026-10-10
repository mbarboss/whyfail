// Package config parses command-line flags and environment variables into a
// validated Config. It is the only package that reads the environment.
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mbarboss/whyfail/internal/sysinfo"
)

// Defaults used when neither a flag nor an environment variable is set.
const (
	DefaultModel   = "gemma4:e4b"
	DefaultHost    = "http://127.0.0.1:11434"
	DefaultTimeout = 120 * time.Second
)

// Environment variables read by Parse. OLLAMA_HOST is shared with the Ollama CLI.
const (
	EnvModel   = "WHYFAIL_MODEL"
	EnvHost    = "OLLAMA_HOST"
	EnvTimeout = "WHYFAIL_TIMEOUT"
	EnvShell   = "WHYFAIL_SHELL"
)

const (
	defaultPort  = "11434"
	maxModelLen  = 200
	minTimeout   = time.Second
	maxTimeout   = 10 * time.Minute
	usageExample = "  <command> 2>&1 | whyfail [flags]\n  whyfail [flags] -- <command> [args]\n  whyfail doctor [flags]    check that Ollama and the model are ready"
)

// ErrInvalid reports a flag or environment value that failed validation.
var ErrInvalid = errors.New("invalid configuration")

// modelPattern allows Ollama model references such as "gemma4:e4b" or
// "user/model:tag" and nothing that could be read as a path or a URL.
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*)?(:[A-Za-z0-9][A-Za-z0-9._-]*)?$`)

// Config is the validated runtime configuration.
type Config struct {
	Model       string
	Host        *url.URL
	Timeout     time.Duration
	ShowVersion bool
	// AllowRemote permits a host that is not loopback. It is a flag only, so
	// sending output off the machine is always an explicit choice.
	AllowRemote bool
	// Command is the command to run in wrapper mode: everything after "--".
	Command []string
	// Args holds positional arguments given without "--".
	Args []string
	// Shell overrides shell detection when set.
	Shell sysinfo.Shell
	// Doctor selects the health check subcommand.
	Doctor bool
}

// Parse reads flags from args, falling back to environment variables looked up
// with getenv and then to the defaults. Usage and errors are written to stderr.
// It returns flag.ErrHelp when help was requested.
func Parse(args []string, getenv func(string) string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("whyfail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { usage(fs, stderr) }

	model := fs.String("model", envOr(getenv, EnvModel, DefaultModel), "Ollama model to use (env "+EnvModel+")")
	host := fs.String("host", envOr(getenv, EnvHost, DefaultHost), "Ollama server URL (env "+EnvHost+")")
	timeout := fs.String("timeout", envOr(getenv, EnvTimeout, DefaultTimeout.String()), "maximum time to wait for an answer (env "+EnvTimeout+")")
	shell := fs.String("shell", envOr(getenv, EnvShell, ""), "shell to write fixes for: bash, zsh, fish, powershell or cmd (default: detected; env "+EnvShell+")")
	allowRemote := fs.Bool("allow-remote", false, "allow an Ollama host that is not on this machine; command output is then sent over the network")
	showVersion := fs.Bool("version", false, "print the version and exit")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	var cfg Config
	rest := fs.Args()
	// The flag package drops the "--" that ends the flags; only a command
	// after it selects wrapper mode, so that bare words stay free for
	// subcommands.
	switch i := len(args) - len(rest) - 1; {
	case i >= 0 && args[i] == "--":
		if len(rest) == 0 {
			return Config{}, fmt.Errorf("%w: no command after --", ErrInvalid)
		}
		cfg.Command = rest
	case len(rest) > 0 && rest[0] == "doctor":
		// Flags may also follow the subcommand.
		if err := fs.Parse(rest[1:]); err != nil {
			return Config{}, err
		}
		if fs.NArg() > 0 {
			return Config{}, fmt.Errorf("%w: doctor takes no arguments", ErrInvalid)
		}
		cfg.Doctor = true
	default:
		cfg.Args = rest
	}
	cfg.ShowVersion, cfg.AllowRemote = *showVersion, *allowRemote

	var err error
	if cfg.Model, err = parseModel(*model); err != nil {
		return Config{}, err
	}
	if cfg.Host, err = parseHost(*host); err != nil {
		return Config{}, err
	}
	if cfg.Timeout, err = parseTimeout(*timeout); err != nil {
		return Config{}, err
	}
	if cfg.Shell, err = parseShell(*shell); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func usage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, "Explain why a command failed, using a local model through Ollama.\n\nUsage:\n%s\n\nFlags:\n", usageExample)
	fs.PrintDefaults()
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return fallback
}

// invalid builds an ErrInvalid without echoing the rejected value, which may
// hold credentials (for example a URL with a password).
func invalid(setting, expected string) error {
	return fmt.Errorf("%w: %s must be %s", ErrInvalid, setting, expected)
}

func parseModel(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > maxModelLen || !modelPattern.MatchString(s) {
		return "", invalid("-model / "+EnvModel, "an Ollama model name such as "+DefaultModel)
	}
	return s, nil
}

// parseHost accepts the same forms as OLLAMA_HOST: scheme and port are
// optional, and a bare host gets http and port 11434.
func parseHost(s string) (*url.URL, error) {
	bad := invalid("-host / "+EnvHost, "an http or https URL with only a host and port, such as "+DefaultHost)
	s = strings.TrimSpace(s)
	if strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return nil, bad
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
		if _, _, err := net.SplitHostPort(strings.TrimPrefix(s, "http://")); err != nil {
			s += ":" + defaultPort
		}
	}

	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, bad
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, bad
		}
	}
	return &url.URL{Scheme: u.Scheme, Host: localizeUnspecified(u)}, nil
}

// localizeUnspecified maps 0.0.0.0 and :: to the loopback address of the same
// family. Users set OLLAMA_HOST=0.0.0.0 so the Ollama server listens on every
// interface; as a destination it means this machine, and Windows cannot
// connect to the unspecified address at all.
func localizeUnspecified(u *url.URL) string {
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsUnspecified() {
		return u.Host
	}
	loopback := "::1"
	if ip.Is4() {
		loopback = "127.0.0.1"
	}
	if u.Port() != "" {
		return net.JoinHostPort(loopback, u.Port())
	}
	if ip.Is4() {
		return loopback
	}
	return "[" + loopback + "]"
}

func parseTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil || d < minTimeout || d > maxTimeout {
		return 0, invalid("-timeout / "+EnvTimeout, fmt.Sprintf("a duration between %v and %v, such as 2m", minTimeout, maxTimeout))
	}
	return d, nil
}

func parseShell(s string) (sysinfo.Shell, error) {
	if strings.TrimSpace(s) == "" {
		return "", nil
	}
	shell, ok := sysinfo.LookupShell(s)
	if !ok {
		return "", invalid("-shell / "+EnvShell, "one of bash, zsh, fish, powershell (or pwsh) and cmd")
	}
	return shell, nil
}
