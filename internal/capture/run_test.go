package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// childEnv makes the test binary act as the child command instead of running
// the tests; see TestMain.
const childEnv = "WHYFAIL_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(child(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// child runs a list of actions such as "out:text", "err:text", "lines:N",
// "stdin", "spawn:duration", "sigint" or "exit:N", in order, and returns the
// exit code.
func child(actions []string) int {
	for _, a := range actions {
		verb, arg, _ := strings.Cut(a, ":")
		switch verb {
		case "out":
			fmt.Println(arg)
		case "err":
			fmt.Fprintln(os.Stderr, arg)
		case "lines":
			n, _ := strconv.Atoi(arg)
			for i := 1; i <= n; i++ {
				fmt.Printf("line %d\n", i)
			}
		case "stdin":
			_, _ = io.Copy(os.Stdout, os.Stdin)
		case "sigint":
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Signal(os.Interrupt)
			time.Sleep(5 * time.Second)
		case "spawn":
			// Leave a grandchild that holds stdout open, like a daemon.
			exe, _ := os.Executable()
			c := exec.Command(exe, "sleep:"+arg) //nolint:gosec,noctx // the test binary re-running itself; it must outlive this process
			c.Stdout = os.Stdout
			_ = c.Start()
		case "sleep":
			d, _ := time.ParseDuration(arg)
			time.Sleep(d)
		case "exit":
			n, _ := strconv.Atoi(arg)
			return n
		}
	}
	return 0
}

// childArgv returns the argv that runs child with the given actions.
func childArgv(t *testing.T, actions ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(childEnv, "1")
	return append([]string{exe}, actions...)
}

func TestRunStreamsOutputAndCapturesTail(t *testing.T) {
	var stdout, stderr bytes.Buffer
	argv := childArgv(t, "out:compiling", "err:main.go:3: undefined: x", "exit:2")

	got, err := Run(argv, strings.NewReader(""), &stdout, &stderr, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2", got.ExitCode)
	}
	if stdout.String() != "compiling\n" || stderr.String() != "main.go:3: undefined: x\n" {
		t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
	for _, want := range []string{"compiling\n", "main.go:3: undefined: x\n"} {
		if !strings.Contains(got.Tail.Text, want) {
			t.Errorf("Tail lacks %q: %q", want, got.Tail.Text)
		}
	}
}

func TestRunReportsSuccess(t *testing.T) {
	var stdout bytes.Buffer

	got, err := Run(childArgv(t, "out:ok"), strings.NewReader(""), &stdout, io.Discard, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.ExitCode != 0 || stdout.String() != "ok\n" {
		t.Errorf("ExitCode = %d, stdout = %q", got.ExitCode, stdout.String())
	}
}

func TestRunStreamsEverythingButKeepsOnlyTheTail(t *testing.T) {
	var stdout bytes.Buffer

	got, err := Run(childArgv(t, "lines:500", "exit:1"), strings.NewReader(""), &stdout, io.Discard, Limits{MaxBytes: 1 << 10, MaxLines: 3})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := strings.Count(stdout.String(), "\n"); n != 500 {
		t.Errorf("streamed %d lines, want 500", n)
	}
	if got.Tail.Text != "line 498\nline 499\nline 500\n" || !got.Tail.Truncated {
		t.Errorf("Tail = %+v", got.Tail)
	}
}

func TestRunGivesStdinToTheChild(t *testing.T) {
	var stdout bytes.Buffer

	_, err := Run(childArgv(t, "stdin"), strings.NewReader("y\n"), &stdout, io.Discard, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stdout.String() != "y\n" {
		t.Errorf("stdout = %q, want the child to echo its stdin", stdout.String())
	}
}

func TestRunLeavesTailEmptyWithoutOutput(t *testing.T) {
	got, err := Run(childArgv(t, "exit:1"), strings.NewReader(""), io.Discard, io.Discard, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got.ExitCode != 1 || got.Tail.Text != "" {
		t.Errorf("got %+v", got)
	}
}

func TestRunDoesNotWaitForGrandchildrenHoldingOutput(t *testing.T) {
	start := time.Now()

	got, err := Run(childArgv(t, "out:started", "spawn:6s", "exit:1"), strings.NewReader(""), io.Discard, io.Discard, DefaultLimits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("Run took %v, want it to return soon after the child exits", elapsed)
	}
	if got.ExitCode != 1 || got.Tail.Text != "started\n" {
		t.Errorf("got %+v", got)
	}
}

func TestRunCommandNotFound(t *testing.T) {
	_, err := Run([]string{"whyfail-test-no-such-command"}, strings.NewReader(""), io.Discard, io.Discard, DefaultLimits)

	if !errors.Is(err, ErrNotFound) || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		l    Limits
	}{
		{"no command", nil, DefaultLimits},
		{"zero limits", []string{"go"}, Limits{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Run(tt.argv, strings.NewReader(""), io.Discard, io.Discard, tt.l)

			if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrCannotRun) {
				t.Errorf("err = %v, want an argument error", err)
			}
		})
	}
}
