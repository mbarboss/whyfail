// Package eval checks model answers against a versioned set of real failures.
// Cases describe properties of a good answer rather than its exact text, so
// they survive rewording and can compare prompts and models.
package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/mbarboss/whyfail/internal/apply"
	"github.com/mbarboss/whyfail/internal/llm"
	"github.com/mbarboss/whyfail/internal/prompt"
	"github.com/mbarboss/whyfail/internal/sysinfo"
)

// Case is one failure and the properties a good answer must have.
type Case struct {
	// Name is the file name without the .json extension.
	Name     string
	Output   string
	Command  string
	ExitCode int

	info         sysinfo.Info
	fixMatches   []*regexp.Regexp
	noFixMatches []*regexp.Regexp
	// warnings is nil when the case does not care about danger warnings.
	warnings *bool
}

// file is the on-disk form of a case.
type file struct {
	Output   string `json:"output"`
	Command  string `json:"command"`
	ExitCode int    `json:"exitCode"`
	Env      struct {
		OS     string `json:"os"`
		Distro string `json:"distro"`
		Arch   string `json:"arch"`
		Shell  string `json:"shell"`
	} `json:"env"`
	Expect struct {
		// FixMatches: each pattern must match at least one fix.
		FixMatches []string `json:"fixMatches"`
		// NoFixMatches: no pattern may match any fix.
		NoFixMatches []string `json:"noFixMatches"`
		// Warnings: whether some fix must (true) or no fix may (false) carry
		// a danger warning.
		Warnings *bool `json:"warnings"`
	} `json:"expect"`
}

// Info returns the environment the failure happened in.
func (c Case) Info() sysinfo.Info { return c.info }

// Failure returns the failure as whyfail would capture it.
func (c Case) Failure() prompt.Failure {
	return prompt.Failure{Output: c.Output, Command: c.Command, ExitCode: c.ExitCode}
}

// Load reads every *.json case in the root of fsys, sorted by name.
func Load(fsys fs.FS) ([]Case, error) {
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("no cases found")
	}
	cases := make([]Case, 0, len(names))
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read case: %w", err)
		}
		c, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("case %s: %w", name, err)
		}
		c.Name = strings.TrimSuffix(path.Base(name), ".json")
		cases = append(cases, c)
	}
	return cases, nil
}

func parse(data []byte) (Case, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f file
	if err := dec.Decode(&f); err != nil {
		return Case{}, fmt.Errorf("decode: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Case{}, errors.New("unexpected data after the case")
	}

	switch {
	case f.Output == "":
		return Case{}, errors.New("output is empty")
	case f.Command == "" && f.ExitCode != 0:
		return Case{}, errors.New("exitCode needs a command")
	case f.Env.OS == "" || f.Env.Arch == "":
		return Case{}, errors.New("env needs os and arch")
	case len(f.Expect.FixMatches) == 0 && len(f.Expect.NoFixMatches) == 0 && f.Expect.Warnings == nil:
		return Case{}, errors.New("expect is empty")
	}
	shell := sysinfo.Unknown
	if f.Env.Shell != string(sysinfo.Unknown) {
		s, ok := sysinfo.LookupShell(f.Env.Shell)
		if !ok {
			return Case{}, fmt.Errorf("unknown shell %q", f.Env.Shell)
		}
		shell = s
	}
	fixMatches, err := compile(f.Expect.FixMatches)
	if err != nil {
		return Case{}, fmt.Errorf("fixMatches: %w", err)
	}
	noFixMatches, err := compile(f.Expect.NoFixMatches)
	if err != nil {
		return Case{}, fmt.Errorf("noFixMatches: %w", err)
	}

	return Case{
		Output:       f.Output,
		Command:      f.Command,
		ExitCode:     f.ExitCode,
		info:         sysinfo.Info{OS: f.Env.OS, Distro: f.Env.Distro, Arch: f.Env.Arch, Shell: shell},
		fixMatches:   fixMatches,
		noFixMatches: noFixMatches,
		warnings:     f.Expect.Warnings,
	}, nil
}

func compile(patterns []string) ([]*regexp.Regexp, error) {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			return nil, errors.New("empty pattern")
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		res = append(res, re)
	}
	return res, nil
}

// Check returns one message per property the answer violates. Warnings are
// computed the way whyfail shows them, including package names taken from
// the captured output.
func Check(c Case, e llm.Explanation) []string {
	if err := llm.Validate(e); err != nil {
		return []string{fmt.Sprintf("invalid answer: %v", err)}
	}

	var violations []string
	for _, re := range c.fixMatches {
		if !anyFixMatches(e.Fixes, re) {
			violations = append(violations, fmt.Sprintf("no fix matches %q", re))
		}
	}
	for _, re := range c.noFixMatches {
		for i, fix := range e.Fixes {
			if re.MatchString(fix.Command) {
				violations = append(violations, fmt.Sprintf("fix %d matches forbidden %q", i+1, re))
			}
		}
	}
	if c.warnings != nil {
		warned := warnedFixes(c.Output, e.Fixes)
		switch {
		case *c.warnings && len(warned) == 0:
			violations = append(violations, "expected a danger warning on some fix, got none")
		case !*c.warnings && len(warned) > 0:
			violations = append(violations, "unexpected danger warning on "+strings.Join(warned, "; "))
		}
	}
	return violations
}

func anyFixMatches(fixes []llm.Fix, re *regexp.Regexp) bool {
	for _, fix := range fixes {
		if re.MatchString(fix.Command) {
			return true
		}
	}
	return false
}

// warnedFixes describes each fix that gets a warning, for the report.
func warnedFixes(output string, fixes []llm.Fix) []string {
	warn := apply.WarningsFor(output)
	var warned []string
	for i, fix := range fixes {
		if w := warn(fix.Command); len(w) > 0 {
			warned = append(warned, fmt.Sprintf("fix %d (%s)", i+1, strings.Join(w, ", ")))
		}
	}
	return warned
}
