package config

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil, env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Model != DefaultModel {
		t.Errorf("Model = %q, want %q", cfg.Model, DefaultModel)
	}
	if got := cfg.Host.String(); got != DefaultHost {
		t.Errorf("Host = %q, want %q", got, DefaultHost)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", cfg.Timeout, DefaultTimeout)
	}
	if cfg.ShowVersion || len(cfg.Args) != 0 {
		t.Errorf("unexpected ShowVersion=%v Args=%v", cfg.ShowVersion, cfg.Args)
	}
}

func TestParseEnvironmentOverridesDefaults(t *testing.T) {
	cfg, err := Parse(nil, env(map[string]string{
		"WHYFAIL_MODEL":   "qwen3.5:4b",
		"OLLAMA_HOST":     "http://localhost:11500",
		"WHYFAIL_TIMEOUT": "30s",
	}), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Model != "qwen3.5:4b" {
		t.Errorf("Model = %q", cfg.Model)
	}
	if got := cfg.Host.String(); got != "http://localhost:11500" {
		t.Errorf("Host = %q", got)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
}

func TestParseFlagsOverrideEnvironment(t *testing.T) {
	cfg, err := Parse(
		[]string{"-model", "llama3.2:3b", "-host", "http://127.0.0.1:9999", "-timeout", "5s"},
		env(map[string]string{"WHYFAIL_MODEL": "qwen3.5:4b", "OLLAMA_HOST": "http://localhost:1", "WHYFAIL_TIMEOUT": "30s"}),
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if cfg.Model != "llama3.2:3b" || cfg.Host.String() != "http://127.0.0.1:9999" || cfg.Timeout != 5*time.Second {
		t.Errorf("flags not applied: %+v", cfg)
	}
}

func TestParseOllamaHostForms(t *testing.T) {
	// OLLAMA_HOST follows Ollama's own conventions: scheme and port are optional.
	tests := []struct {
		in, want string
	}{
		{"127.0.0.1", "http://127.0.0.1:11434"},
		{"127.0.0.1:11500", "http://127.0.0.1:11500"},
		{"localhost", "http://localhost:11434"},
		{"[::1]:11434", "http://[::1]:11434"},
		{"http://127.0.0.1:11434/", "http://127.0.0.1:11434"},
		{"https://ollama.internal", "https://ollama.internal"},
		{"  http://127.0.0.1:11434  ", "http://127.0.0.1:11434"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			cfg, err := Parse(nil, env(map[string]string{"OLLAMA_HOST": tt.in}), &bytes.Buffer{})
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := cfg.Host.String(); got != tt.want {
				t.Errorf("Host = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"model with shell metacharacters", []string{"-model", "gemma; rm -rf /"}, nil},
		{"model with path traversal", []string{"-model", "../../etc/passwd"}, nil},
		{"model with newline", nil, map[string]string{"WHYFAIL_MODEL": "gemma\nx"}},
		{"empty model flag", []string{"-model", ""}, nil},
		{"model too long", []string{"-model", strings.Repeat("a", 201)}, nil},
		{"host with unsupported scheme", []string{"-host", "file:///etc/passwd"}, nil},
		{"host with credentials", []string{"-host", "http://user:pass@127.0.0.1:11434"}, nil},
		{"host with path", []string{"-host", "http://127.0.0.1:11434/api"}, nil},
		{"host with query", []string{"-host", "http://127.0.0.1:11434?x=1"}, nil},
		{"host without hostname", []string{"-host", "http://:11434"}, nil},
		{"host with bad port", []string{"-host", "http://127.0.0.1:99999"}, nil},
		{"host with control character", nil, map[string]string{"OLLAMA_HOST": "http://127.0.0.1\x00:11434"}},
		{"timeout not a duration", []string{"-timeout", "soon"}, nil},
		{"timeout below minimum", []string{"-timeout", "500ms"}, nil},
		{"timeout above maximum", nil, map[string]string{"WHYFAIL_TIMEOUT": "11m"}},
		{"negative timeout", []string{"-timeout", "-5s"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.args, env(tt.env), &bytes.Buffer{})

			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestParseErrorDoesNotEchoValue(t *testing.T) {
	secret := "http://user:hunter2@127.0.0.1:11434" //nolint:gosec // G101: fake credential that must not be echoed

	_, err := Parse(nil, env(map[string]string{"OLLAMA_HOST": secret}), &bytes.Buffer{})

	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error must not echo the raw value: %v", err)
	}
}

func TestParseVersionFlag(t *testing.T) {
	cfg, err := Parse([]string{"-version"}, env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !cfg.ShowVersion {
		t.Error("ShowVersion = false")
	}
}

func TestParseKeepsPositionalArgs(t *testing.T) {
	cfg, err := Parse([]string{"-model", "qwen3.5:4b", "--", "make", "-j4"}, env(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := strings.Join(cfg.Args, " "); got != "make -j4" {
		t.Errorf("Args = %q", got)
	}
}

func TestParseHelpListsFlagsAndEnvironment(t *testing.T) {
	var stderr bytes.Buffer

	_, err := Parse([]string{"-help"}, env(nil), &stderr)

	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"-model", "-host", "-timeout", "-version", "WHYFAIL_MODEL", "OLLAMA_HOST", "WHYFAIL_TIMEOUT"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("help does not mention %s", want)
		}
	}
}

func TestParseUnknownFlagIsNotErrInvalid(t *testing.T) {
	_, err := Parse([]string{"-nope"}, env(nil), &bytes.Buffer{})

	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, flag.ErrHelp) {
		t.Errorf("err = %v, want a plain flag error", err)
	}
}
