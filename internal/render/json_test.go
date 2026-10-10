package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/doctor"
	"github.com/mbarboss/whyfail/internal/llm"
)

// decodeOne checks that out is exactly one JSON object on one line.
func decodeOne(t *testing.T, out string) map[string]any {
	t.Helper()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("want one line of JSON, got %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	return m
}

func ptr[T any](v T) *T { return &v }

func TestJSONAnswer(t *testing.T) {
	var b bytes.Buffer
	answer := llm.Explanation{
		Cause:       "Missing \x1b[31mheaders.",
		Explanation: "OpenSSL headers are not installed.",
		Fixes: []llm.Fix{
			{Command: "sudo apt install libssl-dev", Description: "Install the headers."},
			{Command: "pkg-config --cflags openssl", Description: "Check them."},
		},
	}

	err := JSON(&b, Report{Model: "gemma4:e4b", Command: ptr("make -j4"), ExitCode: ptr(2), Answer: &answer}, warnSudo)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	m := decodeOne(t, b.String())
	if m["schemaVersion"] != float64(1) || m["model"] != "gemma4:e4b" || m["command"] != "make -j4" || m["exitCode"] != float64(2) {
		t.Errorf("header fields = %v", m)
	}
	if m["cause"] != "Missing headers." || m["explanation"] != "OpenSSL headers are not installed." {
		t.Errorf("cause = %q, explanation = %q", m["cause"], m["explanation"])
	}
	if _, ok := m["error"]; ok {
		t.Error("answer carries an error key")
	}
	fixes := m["fixes"].([]any)
	first, second := fixes[0].(map[string]any), fixes[1].(map[string]any)
	if first["command"] != "sudo apt install libssl-dev" || first["description"] != "Install the headers." {
		t.Errorf("first fix = %v", first)
	}
	if w := first["warnings"].([]any); len(w) != 1 || w[0] != "runs with administrator privileges" {
		t.Errorf("first warnings = %v", w)
	}
	if w, ok := second["warnings"].([]any); !ok || len(w) != 0 {
		t.Errorf("second warnings = %#v, want an empty array", second["warnings"])
	}
}

func TestJSONPipeModeHasNullCommandAndExitCode(t *testing.T) {
	var b bytes.Buffer
	answer := llm.Explanation{Cause: "c", Explanation: "e", Fixes: []llm.Fix{{Command: "ls", Description: "d"}}}

	if err := JSON(&b, Report{Model: "m", Answer: &answer}, nil); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	m := decodeOne(t, b.String())
	for _, k := range []string{"command", "exitCode"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", k, v, ok)
		}
	}
}

func TestJSONError(t *testing.T) {
	var b bytes.Buffer

	err := JSON(&b, Report{Model: "m", Error: &ReportError{Code: "ollama_unreachable", Message: "cannot reach\x1b]0;x\x07 Ollama"}}, nil)
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	m := decodeOne(t, b.String())
	e := m["error"].(map[string]any)
	if e["code"] != "ollama_unreachable" || e["message"] != "cannot reach Ollama" {
		t.Errorf("error = %v", e)
	}
	for _, k := range []string{"cause", "explanation", "fixes"} {
		if _, ok := m[k]; ok {
			t.Errorf("error report has %q", k)
		}
	}
}

func TestJSONSuccessfulCommandHasNoAnswer(t *testing.T) {
	var b bytes.Buffer

	if err := JSON(&b, Report{Model: "m", Command: ptr("go version"), ExitCode: ptr(0)}, nil); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	m := decodeOne(t, b.String())
	if m["exitCode"] != float64(0) || m["command"] != "go version" {
		t.Errorf("report = %v", m)
	}
	if _, ok := m["cause"]; ok {
		t.Error("successful command has a cause")
	}
}

func TestJSONSanitizesCommandAndKeepsItOnOneLine(t *testing.T) {
	var b bytes.Buffer

	if err := JSON(&b, Report{Command: ptr("echo \x1b[2Jhi\nrm -rf /"), ExitCode: ptr(1)}, nil); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	if got := decodeOne(t, b.String())["command"]; got != "echo hi rm -rf /" {
		t.Errorf("command = %q", got)
	}
}

func TestJSONKeepsShellOperatorsReadable(t *testing.T) {
	var b bytes.Buffer

	if err := JSON(&b, Report{Command: ptr("make && ./run < in > out"), ExitCode: ptr(1)}, nil); err != nil {
		t.Fatalf("JSON: %v", err)
	}

	if !strings.Contains(b.String(), `"make && ./run < in > out"`) {
		t.Errorf("output = %s", b.String())
	}
}

func TestJSONReportsWriteErrors(t *testing.T) {
	if err := JSON(failWriter{}, Report{}, nil); err == nil {
		t.Error("JSON ignored a write error")
	}
}

func TestDoctorJSON(t *testing.T) {
	var b bytes.Buffer
	results := []doctor.Result{
		{Status: doctor.OK, Title: "Ollama 0.40.2 is running"},
		{Status: doctor.Fail, Title: "Model m is not installed", Fix: "ollama pull m"},
	}

	if err := DoctorJSON(&b, results); err != nil {
		t.Fatalf("DoctorJSON: %v", err)
	}

	m := decodeOne(t, b.String())
	if m["schemaVersion"] != float64(1) || m["passed"] != false {
		t.Errorf("report = %v", m)
	}
	checks := m["checks"].([]any)
	ok, fail := checks[0].(map[string]any), checks[1].(map[string]any)
	if ok["status"] != "ok" || ok["title"] != "Ollama 0.40.2 is running" {
		t.Errorf("first check = %v", ok)
	}
	if _, has := ok["fix"]; has {
		t.Error("passing check has a fix key")
	}
	if fail["status"] != "fail" || fail["fix"] != "ollama pull m" {
		t.Errorf("second check = %v", fail)
	}
}

func TestDoctorJSONPassed(t *testing.T) {
	var b bytes.Buffer

	if err := DoctorJSON(&b, []doctor.Result{{Status: doctor.Warn, Title: "w"}}); err != nil {
		t.Fatalf("DoctorJSON: %v", err)
	}

	if decodeOne(t, b.String())["passed"] != true {
		t.Errorf("passed should be true with only a warning: %s", b.String())
	}
}
