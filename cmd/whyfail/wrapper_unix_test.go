//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrapperFileWithoutExecutePermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, d := newHarness("")

	code := run(context.Background(), []string{"--", path}, d)

	if code != exitCannotRun || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "permission denied") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}
