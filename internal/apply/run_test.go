package apply

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// helperEnv makes the test binary act as a fix instead of running the tests.
const helperEnv = "WHYFAIL_APPLY_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		fmt.Println("fix ran")
		fmt.Fprintln(os.Stderr, "fix stderr")
		code, _ := strconv.Atoi(os.Args[len(os.Args)-1])
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func helperPlan(t *testing.T, exitCode string) Plan {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(helperEnv, "1")
	return Plan{Path: exe, Args: []string{"fix", exitCode}}
}

func TestRunReturnsTheFixExitCode(t *testing.T) {
	for _, code := range []int{0, 3} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got, err := Run(helperPlan(t, strconv.Itoa(code)), strings.NewReader(""), &stdout, &stderr)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if got != code {
				t.Errorf("exit code = %d, want %d", got, code)
			}
			if stdout.String() != "fix ran\n" && stdout.String() != "fix ran\r\n" {
				t.Errorf("stdout = %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "fix stderr") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestRunReportsAMissingProgram(t *testing.T) {
	p := Plan{Path: filepath.Join(t.TempDir(), "missing"), Args: []string{"missing"}}

	if _, err := Run(p, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Error("Run of a missing program returned no error")
	}
}
