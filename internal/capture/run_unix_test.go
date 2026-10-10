//go:build unix

package capture

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsDeathBySignalLikeAShell(t *testing.T) {
	got, err := Run(childArgv(t, "sigint"), strings.NewReader(""), io.Discard, io.Discard, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.ExitCode != 130 {
		t.Errorf("ExitCode = %d, want 130 (128 + SIGINT)", got.ExitCode)
	}
}

func TestRunFileWithoutExecutePermission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Run([]string{path}, strings.NewReader(""), io.Discard, io.Discard, DefaultLimits)

	if !errors.Is(err, ErrCannotRun) || errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrCannotRun", err)
	}
}
