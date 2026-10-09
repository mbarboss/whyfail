package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-version"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if got, want := stdout.String(), "whyfail dev\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRunHelpExitsZeroAndListsFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-help"}, &stdout, &stderr)

	if code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr.String(), "-version") {
		t.Errorf("help output does not list -version: %q", stderr.String())
	}
}

func TestRunRejectsUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"-nope"}, &stdout, &stderr)

	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty, got %q", stdout.String())
	}
}

func TestRunWithoutModeIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run(nil, &stdout, &stderr)

	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if stderr.Len() == 0 {
		t.Error("expected a hint on stderr")
	}
}
