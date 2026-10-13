package apply

import (
	"os/exec"
	"syscall"
)

// setCmdLine makes cmd use line verbatim instead of quoting cmd.Args.
func setCmdLine(cmd *exec.Cmd, line string) {
	if line != "" {
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	}
}
