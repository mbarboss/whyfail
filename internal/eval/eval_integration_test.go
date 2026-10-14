//go:build integration

package eval

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"text/tabwriter"
	"time"

	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/llm/ollama"
	"github.com/mbarboss/whyfail/internal/prompt"
	"github.com/mbarboss/whyfail/internal/redact"
)

// model overrides the configured model: go test -tags=integration ./internal/eval -args -model qwen3.5:4b
var model = flag.String("model", "", "Ollama model to evaluate (default: "+config.EnvModel+" or "+config.DefaultModel+")")

type result struct {
	name       string
	violations []string
	latency    time.Duration
}

// TestEvaluationSet asks the model about every case and reports which ones
// fail. It needs Ollama running with the model pulled.
func TestEvaluationSet(t *testing.T) {
	var args []string
	if *model != "" {
		args = []string{"-model", *model}
	}
	cfg, err := config.Parse(args, os.Getenv, io.Discard)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cases, err := Load(os.DirFS("cases"))
	if err != nil {
		t.Fatal(err)
	}
	client := ollama.New(cfg.Host, cfg.Model, &http.Client{})

	results := make([]result, 0, len(cases))
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			r := run(t, client, cfg.Timeout, c)
			results = append(results, r)
			for _, v := range r.violations {
				t.Error(v)
			}
		})
	}
	report(t, cfg.Model, results)
}

func run(t *testing.T, client *ollama.Client, timeout time.Duration, c Case) result {
	t.Helper()
	f := c.Failure()
	f.Output, f.Command = redact.Redact(f.Output).Text, redact.Redact(f.Command).Text
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	answer, err := client.Explain(ctx, prompt.ForFailure(f, c.Info()))
	r := result{name: c.Name, latency: time.Since(start)}
	if err != nil {
		r.violations = []string{fmt.Sprintf("explain: %v", err)}
		return r
	}
	for i, fix := range answer.Fixes {
		t.Logf("fix %d: %s", i+1, fix.Command)
	}
	r.violations = Check(c, answer)
	return r
}

func report(t *testing.T, model string, results []result) {
	t.Helper()
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	passed := 0
	for _, r := range results {
		status := "FAIL"
		if len(r.violations) == 0 {
			status = "pass"
			passed++
		}
		fmt.Fprintf(w, "%s\t%s\t%.1fs\n", r.name, status, r.latency.Seconds())
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	t.Logf("model %s: %d/%d passed\n%s", model, passed, len(results), b.String())
}
