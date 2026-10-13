//go:build !windows

package apply

import "os/exec"

// setCmdLine does nothing: only Windows has a raw command line, and plans
// that set one (cmd.exe) are only built for Windows.
func setCmdLine(*exec.Cmd, string) {}
