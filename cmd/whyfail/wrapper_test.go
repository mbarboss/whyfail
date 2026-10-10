package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/llm"
)

// childEnv makes the test binary act as the wrapped command instead of
// running the tests; see TestMain.
const childEnv = "WHYFAIL_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(child(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// child runs actions such as "out:text", "err:text" or "exit:N" in order and
// returns the exit code.
func child(actions []string) int {
	for _, a := range actions {
		verb, arg, _ := strings.Cut(a, ":")
		switch verb {
		case "out":
			fmt.Println(arg)
		case "err":
			fmt.Fprintln(os.Stderr, arg)
		case "exit":
			n, _ := strconv.Atoi(arg)
			return n
		}
	}
	return 0
}

// wrap returns whyfail arguments that wrap the test binary acting as child.
func wrap(t *testing.T, flagsAndActions ...string) []string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(childEnv, "1")
	i := 0
	for i < len(flagsAndActions) && strings.HasPrefix(flagsAndActions[i], "-") {
		i++
	}
	args := append([]string{}, flagsAndActions[:i]...)
	args = append(args, "--", exe)
	return append(args, flagsAndActions[i:]...)
}

func TestWrapperSuccessSkipsTheModel(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), wrap(t, "out:all good"), d)

	if code != exitOK || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if h.stdout.String() != "all good\n" || h.stderr.Len() != 0 {
		t.Errorf("stdout = %q, stderr = %q", h.stdout.String(), h.stderr.String())
	}
}

func TestWrapperExplainsFailureAndKeepsExitCode(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), wrap(t, "err:fatal: no upstream", "exit:3"), d)

	if code != 3 || h.fake.calls != 1 {
		t.Fatalf("exit code = %d, calls = %d, stderr = %q", code, h.fake.calls, h.stderr.String())
	}
	if !strings.HasPrefix(h.stderr.String(), "fatal: no upstream\n") {
		t.Errorf("child stderr not streamed: %q", h.stderr.String())
	}
	for _, want := range []string{"Environment: Linux (Test Linux 1), arm64, shell zsh.", "<command>", "It exited with code 3.", "fatal: no upstream"} {
		if !strings.Contains(h.fake.got.User, want) {
			t.Errorf("prompt lacks %q: %q", want, h.fake.got.User)
		}
	}
	if !strings.Contains(h.stdout.String(), "Cause: No upstream branch.") {
		t.Errorf("stdout lacks the explanation: %q", h.stdout.String())
	}
}

func TestWrapperRunsEvenWhenStdinIsATerminal(t *testing.T) {
	h, d := newHarness("")
	d.stdinIsTerminal = true

	code := run(context.Background(), wrap(t, "err:boom", "exit:1"), d)

	if code != 1 || h.fake.calls != 1 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}

func TestWrapperCommandNotFound(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), []string{"--", "whyfail-test-no-such-command", "arg"}, d)

	if code != exitNotFound || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if want := "whyfail: command not found: whyfail-test-no-such-command\n"; h.stderr.String() != want {
		t.Errorf("stderr = %q, want %q", h.stderr.String(), want)
	}
}

func TestWrapperSanitizesCommandNameInMessages(t *testing.T) {
	h, d := newHarness("")

	run(context.Background(), []string{"--", "nope\x1b]0;pwned\x07\x1b[31m"}, d)

	if strings.ContainsRune(h.stderr.String(), '\x1b') || strings.ContainsRune(h.stderr.String(), '\x07') {
		t.Errorf("stderr carries control characters: %q", h.stderr.String())
	}
}

func TestWrapperFailureWithoutOutputSkipsTheModel(t *testing.T) {
	h, d := newHarness("")

	code := run(context.Background(), wrap(t, "exit:4"), d)

	if code != 4 || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if !strings.Contains(h.stderr.String(), "exited with code 4 without any output") {
		t.Errorf("stderr = %q", h.stderr.String())
	}
}

func TestWrapperInterruptedSkipsTheModel(t *testing.T) {
	h, d := newHarness("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	code := run(ctx, wrap(t, "err:^C", "exit:3"), d)

	if code != 3 || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
}

func TestWrapperKeepsExitCodeWhenExplanationFails(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"unreachable", fmt.Errorf("post: %w", llm.ErrUnreachable), "ollama serve"},
		{"interrupted", fmt.Errorf("chat: %w", context.Canceled), "interrupted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, d := newHarness("")
			h.fake.err = tt.err

			code := run(context.Background(), wrap(t, "err:boom", "exit:5"), d)

			if code != 5 {
				t.Errorf("exit code = %d, want the child's 5", code)
			}
			if !strings.Contains(h.stderr.String(), tt.want) {
				t.Errorf("stderr lacks %q: %q", tt.want, h.stderr.String())
			}
		})
	}
}

func TestWrapperChecksHostBeforeRunning(t *testing.T) {
	h, d := newHarness("")
	h.env["OLLAMA_HOST"] = "http://192.168.1.10:11434"

	code := run(context.Background(), wrap(t, "out:should not run", "exit:1"), d)

	if code != exitUsage || h.fake.calls != 0 {
		t.Errorf("exit code = %d, calls = %d", code, h.fake.calls)
	}
	if strings.Contains(h.stdout.String(), "should not run") {
		t.Error("the command ran although the host was refused")
	}
}

func TestWrapperRedactsSecretsInCommand(t *testing.T) {
	secret := "hunter2-" + strings.Repeat("x", 8)
	h, d := newHarness("")

	run(context.Background(), wrap(t, "password="+secret, "err:auth failed", "exit:1"), d)

	if h.fake.calls != 1 || strings.Contains(h.fake.got.User, secret) {
		t.Errorf("calls = %d, prompt = %q", h.fake.calls, h.fake.got.User)
	}
}

func TestFormatCommandQuotesArgumentsThatNeedIt(t *testing.T) {
	got := formatCommand([]string{"git", "commit", "-m", "fix bug", "", `say "hi"`})

	if want := `git commit -m "fix bug" "" "say \"hi\""`; got != want {
		t.Errorf("formatCommand = %s, want %s", got, want)
	}
}
