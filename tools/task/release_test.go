package main

import (
	"errors"
	"strings"
	"testing"
)

const changelog = `# Changelog

Intro text.

## [Unreleased]

## [0.2.0] - 2026-11-01

### Added

- Something new.

## [0.1.0] - 2026-10-14

### Added

- First release.
[Not a link reference] in a paragraph.

### Security

- Loopback only.

[0.2.0]: https://github.com/mbarboss/whyfail/compare/v0.1.0...v0.2.0
`

func TestReleaseNotesReturnsTheVersionSection(t *testing.T) {
	for _, version := range []string{"0.1.0", "v0.1.0"} {
		got, err := releaseNotes(changelog, version)
		if err != nil {
			t.Fatalf("releaseNotes(%q): %v", version, err)
		}
		want := "### Added\n\n- First release.\n[Not a link reference] in a paragraph.\n\n### Security\n\n- Loopback only.\n"
		if got != want {
			t.Errorf("releaseNotes(%q) = %q, want %q", version, got, want)
		}
	}
}

func TestReleaseNotesStopsAtTheNextVersion(t *testing.T) {
	got, err := releaseNotes(changelog, "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "### Added\n\n- Something new.\n" {
		t.Errorf("notes = %q", got)
	}
}

func TestReleaseNotesRejectsMissingOrEmptySections(t *testing.T) {
	for _, version := range []string{"9.9.9", "Unreleased", "", "0.1", "0.1.0.0"} {
		if _, err := releaseNotes(changelog, version); err == nil {
			t.Errorf("releaseNotes(%q) succeeded, want an error", version)
		}
	}
}

var (
	sumA = strings.Repeat("ab", 32)
	sumB = strings.Repeat("CD", 32)
)

func TestParseChecksums(t *testing.T) {
	sums, err := parseChecksums(sumA + "  whyfail_0.1.0_linux_amd64.tar.gz\n" + sumB + "  whyfail_0.1.0_windows_arm64.zip\n\n")
	if err != nil {
		t.Fatal(err)
	}
	if sums["whyfail_0.1.0_linux_amd64.tar.gz"] != sumA || sums["whyfail_0.1.0_windows_arm64.zip"] != strings.ToLower(sumB) {
		t.Errorf("sums = %v", sums)
	}
}

func TestParseChecksumsRejectsMalformedLines(t *testing.T) {
	for _, in := range []string{
		sumA, sumA + "  a b", "abc123  short.zip", strings.Repeat("z", 64) + "  file.zip",
		sumA + "  ../evil.zip", sumA + `  dir\evil.zip`, sumA + "  a.zip\n" + sumA + "  a.zip",
	} {
		if _, err := parseChecksums(in); err == nil {
			t.Errorf("parseChecksums(%q) succeeded, want an error", in)
		}
	}
}

func TestArchiveName(t *testing.T) {
	tests := []struct{ version, goos, goarch, want string }{
		{"0.1.0", "linux", "amd64", "whyfail_0.1.0_linux_amd64.tar.gz"},
		{"v0.1.0", "darwin", "arm64", "whyfail_0.1.0_darwin_arm64.tar.gz"},
		{"0.1.0", "windows", "amd64", "whyfail_0.1.0_windows_amd64.zip"},
	}
	for _, tt := range tests {
		if got := archiveName(tt.version, tt.goos, tt.goarch); got != tt.want {
			t.Errorf("archiveName(%q, %q, %q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}

func TestCheckExitCode(t *testing.T) {
	if err := checkExit("doctor", 1, nil, 1); err != nil {
		t.Errorf("matching code: %v", err)
	}
	if err := checkExit("doctor", 0, nil, 1); err == nil || !strings.Contains(err.Error(), "doctor") {
		t.Errorf("wrong code: err = %v", err)
	}
	if err := checkExit("doctor", -1, errors.New("exec: not found"), 1); err == nil {
		t.Error("start failure: err = nil")
	}
}
