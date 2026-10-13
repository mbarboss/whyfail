package apply

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/mbarboss/whyfail/internal/capture"
)

// waitDelay bounds how long Run waits for output after the fix exits, in
// case the fix leaves a background process holding stdout.
const waitDelay = time.Second

// Run runs p with the given standard streams and returns its exit code. Like
// wrapper mode, it does not stop the fix when whyfail is interrupted: the
// terminal delivers Ctrl+C to the fix too.
func Run(p Plan, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if len(p.Args) == 0 {
		return 0, errors.New("apply: empty plan")
	}
	cmd := exec.Command(p.Path, p.Args[1:]...) //nolint:gosec,noctx // G204: the user confirmed this exact command; noctx: a context would kill it on Ctrl+C
	cmd.Args[0] = p.Args[0]
	setCmdLine(cmd, p.CmdLine)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("apply: start %s: %w", p.Args[0], err)
	}

	err := cmd.Wait()
	code := capture.ExitCode(cmd.ProcessState)
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) && !errors.Is(err, exec.ErrWaitDelay) {
		return code, fmt.Errorf("apply: copy fix output: %w", err)
	}
	return code, nil
}
