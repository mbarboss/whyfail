// Package apply decides how suggested fixes are presented and run.
package apply

import "regexp"

// Warning reasons, phrased to follow "Warning: ".
const (
	Elevated        = "runs with administrator privileges"
	PipeToShell     = "runs downloaded or generated code in a shell"
	RecursiveDelete = "deletes files recursively"
	WorldWritable   = "gives every user write access"
	ForceKill       = "force-stops processes"
	DiscardsChanges = "can discard work that is not saved elsewhere"
	DiskWrite       = "writes directly to a disk"
	EditsInPlace    = "modifies files or settings in place"
)

type rule struct {
	reason   string
	patterns []*regexp.Regexp
}

// args matches the rest of a simple command, stopping at a command separator.
const args = `[^;&|\n]*`

// rules are checked in order. They are a safety net for suggestions that
// come from an untrusted model, not a sandbox: they aim to catch the common
// spellings across POSIX shells, PowerShell and cmd.
var rules = []rule{
	{Elevated, compile(
		`(^|[\s;&|(])(sudo|doas|pkexec)\b`,
		`(^|[\s;&|(])su(\s|$)`,
		`\brunas\b`,
	)},
	{PipeToShell, compile(
		`\|\s*(sudo\s+(-\S+\s+)*)?(ba|z|da|k|c|tc|fi)?sh\b`,
		`\|\s*(sudo\s+)?(python3?|perl|ruby|node)\b`,
		`\|\s*(iex|invoke-expression)\b`,
		`\b(iex|invoke-expression)\b`,
		`<\(\s*(curl|wget|iwr|invoke-webrequest)\b`,
	)},
	{RecursiveDelete, compile(
		`\brm\b`+args+`\s(-[a-z]*r[a-z]*|--recursive)(\s|$)`,
		`\b(remove-item|ri|rm|del|rmdir|rd)\b`+args+`\s-recurse\b`,
		`\b(rd|rmdir|del)\b`+args+`\s/s\b`,
	)},
	{WorldWritable, compile(
		`\bchmod\b` + args + `\s(0?777|0?666|[ugo]*a[ugo]*\+[rwx]*w[rwx]*|o\+[rwx]*w[rwx]*)(\s|$)`,
	)},
	{ForceKill, compile(
		`\bkill\s+(-9|-kill|-s\s+(kill|9))(\s|$)`,
		`\b(killall|pkill)\b`,
		`\btaskkill\b`+args+`\s/f\b`,
		`\bstop-process\b`+args+`\s-force\b`,
	)},
	{DiscardsChanges, compile(
		`\bgit\s+reset\b`+args+`\s--hard\b`,
		`\bgit\s+push\b`+args+`\s(--force|-f)(\s|$)`,
		`\bgit\s+clean\b`+args+`\s-[a-z]*f`,
		`\bgit\s+(checkout|restore)\s+(--\s+)?\.(\s|$)`,
		`\bgit\s+stash\s+(drop|clear)\b`,
	)},
	{DiskWrite, compile(
		`\bdd\b`+args+`\bof=/dev/`,
		`\bmkfs(\.\w+)?\b`,
		`\bformat\s+[a-z]:`,
		`\bdiskpart\b`,
		`>\s*/dev/(sd|hd|nvme|disk|mmcblk)`,
	)},
	{EditsInPlace, compile(
		`\bsed\b`+args+`\s-[a-z]*i`,
		`\bperl\b`+args+`\s-[a-z]*i`,
		`>>?\s*\S*(\.bashrc|\.zshrc|\.profile|\.bash_profile|\.zprofile|config\.fish|profile\.ps1)\b`,
		`\bsetx\b`,
		`\[environment\]::setenvironmentvariable\b`,
	)},
}

func compile(exprs ...string) []*regexp.Regexp {
	res := make([]*regexp.Regexp, len(exprs))
	for i, e := range exprs {
		res[i] = regexp.MustCompile(`(?i)` + e)
	}
	return res
}

// Warnings returns a short reason for every risky pattern found in command,
// in a stable order. It returns nil for commands with no known risk.
func Warnings(command string) []string {
	var found []string
	for _, r := range rules {
		for _, p := range r.patterns {
			if p.MatchString(command) {
				found = append(found, r.reason)
				break
			}
		}
	}
	return found
}
