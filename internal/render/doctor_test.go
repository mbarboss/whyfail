package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mbarboss/whyfail/internal/doctor"
)

func TestDoctorPrintsOneLinePerCheckWithFixes(t *testing.T) {
	var b bytes.Buffer
	results := []doctor.Result{
		{Status: doctor.OK, Title: "Ollama 0.40.1 is running"},
		{Status: doctor.Fail, Title: "Model gemma4:e4b is not installed", Fix: "ollama pull gemma4:e4b"},
		{Status: doctor.Skip, Title: "Model check skipped"},
		{Status: doctor.Info, Title: "Environment: Linux, amd64, shell bash"},
	}

	if err := Doctor(&b, results); err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	want := "ok    Ollama 0.40.1 is running\n" +
		"FAIL  Model gemma4:e4b is not installed\n" +
		"      Fix: ollama pull gemma4:e4b\n" +
		"skip  Model check skipped\n" +
		"info  Environment: Linux, amd64, shell bash\n"
	if b.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", b.String(), want)
	}
}

func TestDoctorSanitizesServerText(t *testing.T) {
	var b bytes.Buffer
	results := []doctor.Result{{Status: doctor.Fail, Title: "Ollama returned an error: \x1b]0;pwned\x07boom\nsecond line", Fix: "fix\x1b[31m"}}

	if err := Doctor(&b, results); err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	if strings.ContainsAny(b.String(), "\x1b\x07") || strings.Count(b.String(), "\n") != 2 {
		t.Errorf("output = %q", b.String())
	}
}

func TestDoctorReportsWriteErrors(t *testing.T) {
	if err := Doctor(failWriter{}, []doctor.Result{{Status: doctor.OK, Title: "x"}}); err == nil {
		t.Error("Doctor ignored a write error")
	}
}
