package apply

import (
	"errors"
	"os/exec"
	"slices"
	"testing"

	"github.com/mbarboss/whyfail/internal/sysinfo"
)

// installed returns a lookPath that finds only the given programs.
func installed(programs ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(programs, name) {
			return "/opt/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestFindShellPlansPOSIXStyleShells(t *testing.T) {
	for _, s := range []sysinfo.Shell{sysinfo.Bash, sysinfo.Zsh, sysinfo.Fish} {
		t.Run(string(s), func(t *testing.T) {
			sh, err := FindShell(s, "linux", installed(string(s)))
			if err != nil {
				t.Fatalf("FindShell: %v", err)
			}

			p := sh.Command("ls | wc -l")

			if p.Path != "/opt/bin/"+string(s) {
				t.Errorf("Path = %q", p.Path)
			}
			if want := []string{string(s), "-c", "ls | wc -l"}; !slices.Equal(p.Args, want) {
				t.Errorf("Args = %q, want %q", p.Args, want)
			}
			if p.CmdLine != "" || sh.Name() != string(s) {
				t.Errorf("CmdLine = %q, Name = %q", p.CmdLine, sh.Name())
			}
		})
	}
}

func TestFindShellPrefersPwshForPowerShell(t *testing.T) {
	sh, err := FindShell(sysinfo.PowerShell, "windows", installed("pwsh", "powershell"))
	if err != nil {
		t.Fatalf("FindShell: %v", err)
	}

	p := sh.Command("Get-ChildItem | Measure-Object")

	want := []string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem | Measure-Object"}
	if p.Path != "/opt/bin/pwsh" || !slices.Equal(p.Args, want) {
		t.Errorf("Plan = %q %q, want pwsh %q", p.Path, p.Args, want)
	}
}

func TestFindShellFallsBackToWindowsPowerShell(t *testing.T) {
	sh, err := FindShell(sysinfo.PowerShell, "windows", installed("powershell"))
	if err != nil {
		t.Fatalf("FindShell: %v", err)
	}

	if p := sh.Command("dir"); p.Path != "/opt/bin/powershell" || p.Args[0] != "powershell" {
		t.Errorf("Plan = %q %q", p.Path, p.Args)
	}
}

func TestFindShellPassesCmdCommandLineVerbatim(t *testing.T) {
	sh, err := FindShell(sysinfo.Cmd, "windows", installed("cmd"))
	if err != nil {
		t.Fatalf("FindShell: %v", err)
	}

	p := sh.Command(`del "a b.txt" && echo done`)

	if want := `"/opt/bin/cmd" /d /s /c "del "a b.txt" && echo done"`; p.CmdLine != want {
		t.Errorf("CmdLine = %q, want %q", p.CmdLine, want)
	}
	if want := []string{"cmd", "/d", "/s", "/c", `del "a b.txt" && echo done`}; !slices.Equal(p.Args, want) {
		t.Errorf("Args = %q, want %q", p.Args, want)
	}
}

func TestFindShellRefusals(t *testing.T) {
	tests := []struct {
		name  string
		shell sysinfo.Shell
		goos  string
		want  error
	}{
		{"unknown shell", sysinfo.Unknown, "linux", ErrUnknownShell},
		{"no shell", "", "linux", ErrUnknownShell},
		{"cmd outside Windows", sysinfo.Cmd, "linux", ErrUnsupportedShell},
		{"shell not installed", sysinfo.Zsh, "darwin", ErrShellNotFound},
		{"no PowerShell at all", sysinfo.PowerShell, "windows", ErrShellNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FindShell(tt.shell, tt.goos, installed("cmd", "bash"))

			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
