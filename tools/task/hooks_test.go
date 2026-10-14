package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLefthookConfigRunsThePinnedLefthook guards against the installed hook
// pointing at the temporary binary that `go tool` builds (issue #37).
func TestLefthookConfigRunsThePinnedLefthook(t *testing.T) {
	root := filepath.Join("..", "..")
	config, err := os.ReadFile(filepath.Join(root, "lefthook.yml"))
	if err != nil {
		t.Fatal(err)
	}

	const want = "lefthook: go tool -modfile=tools/lefthook/go.mod lefthook"
	if !strings.Contains(strings.ReplaceAll(string(config), "\r\n", "\n"), "\n"+want+"\n") {
		t.Errorf("lefthook.yml lacks the line %q", want)
	}
	if _, err := os.Stat(filepath.Join(root, "tools", "lefthook", "go.mod")); err != nil {
		t.Errorf("modfile named in lefthook.yml: %v", err)
	}
}
