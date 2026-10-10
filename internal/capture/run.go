package capture

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Errors returned by Run when the command could not be started. They map to
// the shell exit codes 127 and 126.
var (
	ErrNotFound  = errors.New("command not found")
	ErrCannotRun = errors.New("cannot run command")
)

// waitDelay bounds how long Run waits for output after the command exits. A
// background process started by the command can hold stdout open for good.
const waitDelay = time.Second

// Result describes a command that ran to completion.
type Result struct {
	// ExitCode is the command's exit code, or 128 plus the signal number when
	// a signal killed it, as shells report it.
	ExitCode int
	// Tail is the end of the combined stdout and stderr. Its Text is empty
	// when the command printed nothing but whitespace.
	Tail Tail
}

// Run runs argv without a shell. The command's output is copied to stdout and
// stderr as it arrives, and its tail is kept within l. Run does not stop the
// command when whyfail is interrupted: the terminal delivers Ctrl+C to the
// command too, and the command decides how to react.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer, l Limits) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("capture: no command to run")
	}
	tail, err := newTailWriter(l)
	if err != nil {
		return Result{}, err
	}

	path, err := exec.LookPath(argv[0])
	if err != nil {
		return Result{}, startError(err)
	}
	cmd := exec.Command(path, argv[1:]...) //nolint:gosec,noctx // G204: running the user's command is the point of wrapper mode (argv, no shell); noctx: a context would kill it on Ctrl+C
	cmd.Args[0] = argv[0]
	cmd.Stdin = stdin
	// The tail comes first so it still sees the output if the terminal fails.
	cmd.Stdout = io.MultiWriter(tail, stdout)
	cmd.Stderr = io.MultiWriter(tail, stderr)
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return Result{}, startError(err)
	}

	err = cmd.Wait()
	res := Result{ExitCode: exitCode(cmd.ProcessState)}
	if t, tailErr := tail.Tail(); tailErr == nil {
		res.Tail = t
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) && !errors.Is(err, exec.ErrWaitDelay) {
		return res, fmt.Errorf("capture: copy command output: %w", err)
	}
	return res, nil
}

func startError(err error) error {
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return fmt.Errorf("%w: %w", ErrCannotRun, err)
}

func exitCode(s *os.ProcessState) int {
	// syscall.WaitStatus exists on every supported OS; Signaled is always
	// false on Windows.
	if ws, ok := s.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return s.ExitCode()
}
