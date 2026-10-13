package apply

import (
	"errors"
	"fmt"

	"github.com/mbarboss/whyfail/internal/sysinfo"
)

// Errors returned by FindShell.
var (
	ErrUnknownShell     = errors.New("unknown shell")
	ErrUnsupportedShell = errors.New("shell not available on this OS")
	ErrShellNotFound    = errors.New("shell not found")
)

// Plan is a resolved program and arguments that run one fix.
type Plan struct {
	Path string
	// Args holds the program name followed by its arguments.
	Args []string
	// CmdLine, when set, is the exact Windows command line. cmd.exe does not
	// parse its arguments the way Go quotes them.
	CmdLine string
}

// Shell runs fixes through the user's shell, since fixes are written in its
// syntax.
type Shell struct {
	path, name string
	flags      []string
	cmd        bool
}

// FindShell resolves the program for shell s on goos with lookPath. For
// PowerShell it prefers pwsh and falls back to Windows PowerShell.
func FindShell(s sysinfo.Shell, goos string, lookPath func(string) (string, error)) (Shell, error) {
	var names, flags []string
	switch s {
	case sysinfo.Bash, sysinfo.Zsh, sysinfo.Fish:
		names, flags = []string{string(s)}, []string{"-c"}
	case sysinfo.PowerShell:
		// No profile: it can print text or change commands, and it is not
		// what the model wrote the fix for.
		names, flags = []string{"pwsh", "powershell"}, []string{"-NoProfile", "-Command"}
	case sysinfo.Cmd:
		if goos != "windows" {
			return Shell{}, fmt.Errorf("%w: cmd on %s", ErrUnsupportedShell, goos)
		}
		// /d skips AutoRun commands from the registry; /s keeps the quotes
		// inside the command as typed.
		names, flags = []string{"cmd"}, []string{"/d", "/s", "/c"}
	case sysinfo.Unknown:
		return Shell{}, ErrUnknownShell
	default:
		return Shell{}, fmt.Errorf("%w: %q", ErrUnknownShell, s)
	}

	for _, name := range names {
		if path, err := lookPath(name); err == nil {
			return Shell{path: path, name: name, flags: flags, cmd: s == sysinfo.Cmd}, nil
		}
	}
	return Shell{}, fmt.Errorf("%w: %s", ErrShellNotFound, names[0])
}

// Name is the shell program's name, such as "bash" or "pwsh".
func (s Shell) Name() string { return s.name }

// Command returns the plan that runs fix in s.
func (s Shell) Command(fix string) Plan {
	args := append(append([]string{s.name}, s.flags...), fix)
	p := Plan{Path: s.path, Args: args}
	if s.cmd {
		p.CmdLine = `"` + s.path + `" /d /s /c "` + fix + `"`
	}
	return p
}
