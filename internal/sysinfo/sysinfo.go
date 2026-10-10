// Package sysinfo describes the machine and shell a failure happened in, so
// suggested fixes match the user's environment.
package sysinfo

import (
	"bufio"
	"bytes"
	"os"
	"runtime"
	"strings"
)

// Shell is a shell family whyfail can tailor fixes to.
type Shell string

// Shells recognized by detection and accepted as an override.
const (
	Bash       Shell = "bash"
	Zsh        Shell = "zsh"
	Fish       Shell = "fish"
	PowerShell Shell = "powershell"
	Cmd        Shell = "cmd"
	Unknown    Shell = "unknown"
)

// shellNames maps bare, lower-case program names to shells.
var shellNames = map[string]Shell{
	"bash":       Bash,
	"zsh":        Zsh,
	"fish":       Fish,
	"powershell": PowerShell,
	"pwsh":       PowerShell,
	"cmd":        Cmd,
}

// String returns the shell's display name.
func (s Shell) String() string {
	if s == PowerShell {
		return "PowerShell"
	}
	return string(s)
}

// LookupShell accepts a bare shell name such as "bash" or "pwsh", ignoring
// case and surrounding spaces. It is the allowlist for user overrides.
func LookupShell(name string) (Shell, bool) {
	s, ok := shellNames[strings.ToLower(strings.TrimSpace(name))]
	return s, ok
}

// ParseShell maps a process or program name, such as "/bin/zsh", "-bash"
// (a login shell) or "pwsh.exe", to a shell.
func ParseShell(name string) (Shell, bool) {
	// Names may come from any OS, so split on both separators.
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	name = strings.TrimPrefix(name, "-")
	lower := strings.ToLower(name)
	return LookupShell(strings.TrimSuffix(lower, ".exe"))
}

// Info describes the environment of a failure.
type Info struct {
	// OS is a display name such as "Linux", "macOS" or "Windows".
	OS string
	// Distro is the Linux distribution, if known.
	Distro string
	Arch   string
	Shell  Shell
}

// String renders the info for the prompt, for example
// "Linux (Ubuntu 24.04.1 LTS), amd64, shell bash".
func (i Info) String() string {
	system := i.OS
	if i.Distro != "" {
		system += " (" + i.Distro + ")"
	}
	return system + ", " + i.Arch + ", shell " + i.Shell.String()
}

// Probe holds what Detect looks at, so tests can describe any OS.
type Probe struct {
	GOOS, GOARCH string
	Getenv       func(string) string
	// ParentName returns the name of the process that started whyfail,
	// which is normally the user's shell.
	ParentName func() (string, error)
	ReadFile   func(string) ([]byte, error)
}

// System returns the probe for this machine.
func System(getenv func(string) string) Probe {
	return Probe{
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Getenv:     getenv,
		ParentName: parentName,
		ReadFile:   os.ReadFile,
	}
}

var osNames = map[string]string{
	"linux":   "Linux",
	"darwin":  "macOS",
	"windows": "Windows",
	"freebsd": "FreeBSD",
	"openbsd": "OpenBSD",
	"netbsd":  "NetBSD",
}

// Detect describes the environment p sees. A non-empty override replaces
// shell detection.
func Detect(p Probe, override Shell) Info {
	info := Info{OS: p.GOOS, Arch: p.GOARCH, Shell: override}
	if name, ok := osNames[p.GOOS]; ok {
		info.OS = name
	}
	if p.GOOS == "linux" {
		info.Distro = distro(p.ReadFile)
	}
	if info.Shell == "" {
		info.Shell = detectShell(p)
	}
	return info
}

// detectShell prefers the parent process, which is the shell the user typed
// in. $SHELL is only the login shell, so it is the fallback for when whyfail
// was started by another program, such as make or npm. Lookup errors are not
// reported: an unknown shell only makes the fixes less specific.
func detectShell(p Probe) Shell {
	if name, err := p.ParentName(); err == nil {
		if s, ok := ParseShell(name); ok {
			return s
		}
	}
	if s, ok := ParseShell(p.Getenv("SHELL")); ok {
		return s
	}
	return Unknown
}

// maxDistroLen bounds the distribution name that goes into the prompt.
const maxDistroLen = 64

// osReleaseFiles are read in order, as os-release(5) specifies.
var osReleaseFiles = []string{"/etc/os-release", "/usr/lib/os-release"}

// distro returns PRETTY_NAME (or NAME) from os-release, restricted to
// characters that cannot carry markup or instructions into the prompt.
func distro(readFile func(string) ([]byte, error)) string {
	for _, f := range osReleaseFiles {
		b, err := readFile(f)
		if err != nil {
			continue
		}
		vars := parseOSRelease(b)
		name := vars["PRETTY_NAME"]
		if name == "" {
			name = vars["NAME"]
		}
		return clean(name)
	}
	return ""
}

func parseOSRelease(b []byte) map[string]string {
	vars := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		vars[key] = value
	}
	return vars
}

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case strings.ContainsRune(" .,_-+/()", r):
			return r
		default:
			return -1
		}
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDistroLen {
		s = strings.TrimSpace(s[:maxDistroLen])
	}
	return s
}
