package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mbarboss/whyfail/internal/doctor"
	"github.com/mbarboss/whyfail/internal/llm"
)

// SchemaVersion is the version of the JSON output. It changes only when a
// field is removed or changes meaning.
const SchemaVersion = 1

// Report is everything a JSON run reports.
type Report struct {
	Model string
	// Command is the redacted command line in wrapper mode; nil in pipe mode.
	Command *string
	// ExitCode is the command's exit code in wrapper mode; nil in pipe mode.
	ExitCode *int
	Answer   *llm.Explanation
	Error    *ReportError
}

// ReportError is a failure with a stable, documented code.
type ReportError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type jsonFix struct {
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Warnings    []string `json:"warnings"`
}

type jsonReport struct {
	SchemaVersion int          `json:"schemaVersion"`
	Model         string       `json:"model,omitempty"`
	Command       *string      `json:"command"`
	ExitCode      *int         `json:"exitCode"`
	Cause         string       `json:"cause,omitempty"`
	Explanation   string       `json:"explanation,omitempty"`
	Fixes         []jsonFix    `json:"fixes,omitempty"`
	Error         *ReportError `json:"error,omitempty"`
}

// JSON writes r as one line of JSON. Text from the model and the command line
// is sanitized as in Text, so consumers that print fields stay safe. warn
// returns the warnings for one command; it may be nil.
func JSON(w io.Writer, r Report, warn func(command string) []string) error {
	out := jsonReport{SchemaVersion: SchemaVersion, Model: r.Model, ExitCode: r.ExitCode}
	if r.Command != nil {
		cmd := oneLine(*r.Command)
		out.Command = &cmd
	}
	if r.Error != nil {
		out.Error = &ReportError{Code: r.Error.Code, Message: strings.TrimSpace(Sanitize(r.Error.Message))}
	}
	if a := r.Answer; a != nil {
		out.Cause = oneLine(a.Cause)
		out.Explanation = Sanitize(a.Explanation)
		for _, f := range a.Fixes {
			fix := jsonFix{Command: oneLine(f.Command), Description: Sanitize(f.Description), Warnings: []string{}}
			if warn != nil {
				for _, reason := range warn(fix.Command) {
					fix.Warnings = append(fix.Warnings, oneLine(reason))
				}
			}
			out.Fixes = append(out.Fixes, fix)
		}
	}
	return writeJSON(w, out)
}

type jsonCheck struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Fix    string `json:"fix,omitempty"`
}

// DoctorJSON writes the doctor results as one line of JSON.
func DoctorJSON(w io.Writer, results []doctor.Result) error {
	checks := make([]jsonCheck, 0, len(results))
	for _, r := range results {
		checks = append(checks, jsonCheck{Status: strings.ToLower(r.Status.String()), Title: oneLine(r.Title), Fix: oneLine(r.Fix)})
	}
	return writeJSON(w, struct {
		SchemaVersion int         `json:"schemaVersion"`
		Passed        bool        `json:"passed"`
		Checks        []jsonCheck `json:"checks"`
	}{SchemaVersion, doctor.Passed(results), checks})
}

// writeJSON writes v and a newline in one write. HTML escaping is off so
// commands such as "a && b" stay readable; the output is not meant for HTML.
func writeJSON(w io.Writer, v any) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("render: encode JSON: %w", err)
	}
	_, err := w.Write(b.Bytes())
	return err
}
