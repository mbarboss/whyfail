//go:build integration

package ollama

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mbarboss/whyfail/internal/config"
	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/prompt"
)

// TestIntegrationExplainsRealFailure needs Ollama on the default host with
// the default model pulled.
func TestIntegrationExplainsRealFailure(t *testing.T) {
	host, err := url.Parse(config.DefaultHost)
	if err != nil {
		t.Fatal(err)
	}
	c := New(host, config.DefaultModel, &http.Client{})
	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultTimeout)
	defer cancel()
	req := prompt.Build(prompt.Failure{
		Output: "fatal: The current branch feature/x has no upstream branch.\n" +
			"To push the current branch and set the remote as upstream, use\n\n" +
			"    git push --set-upstream origin feature/x\n",
	})

	start := time.Now()
	got, err := c.Explain(ctx, req)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	t.Logf("answered in %v", time.Since(start))

	if err := llm.Validate(got); err != nil {
		t.Fatalf("answer failed validation: %v", err)
	}
	if !strings.Contains(got.Fixes[0].Command, "git push") {
		t.Errorf("first fix = %q, want a git push command", got.Fixes[0].Command)
	}
}
