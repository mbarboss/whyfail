package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	checksumsFile = "checksums.txt"
	// maxBinaryBytes bounds what smoke extracts from an archive.
	maxBinaryBytes = 100 << 20
	// unreachableHost is a loopback port with nothing listening, so the
	// smoke test exercises the paths where Ollama is missing.
	unreachableHost = "http://127.0.0.1:9"
)

var (
	semver    = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// linkRef matches the "[0.1.0]: https://..." references that end a
	// Keep a Changelog file.
	linkRef = regexp.MustCompile(`^\[[^\]]+\]:\s`)
)

// releaseNotesTarget writes the CHANGELOG section of a version to a file, so
// the GitHub release carries the human-written notes.
func releaseNotesTarget(_ context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: release-notes <version> <output file>")
	}
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return fmt.Errorf("read changelog: %w", err)
	}
	notes, err := releaseNotes(string(changelog), args[0])
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[1], []byte(notes), 0o600); err != nil {
		return fmt.Errorf("write release notes: %w", err)
	}
	return nil
}

// releaseNotes returns the body of the "## [version]" section of a Keep a
// Changelog file, without the heading and surrounding blank lines.
func releaseNotes(changelog, version string) (string, error) {
	version = strings.TrimPrefix(version, "v")
	if !semver.MatchString(version) {
		return "", fmt.Errorf("version %q is not MAJOR.MINOR.PATCH", version)
	}
	heading := "## [" + version + "]"
	var body []string
	found := false
	for _, line := range strings.Split(strings.ReplaceAll(changelog, "\r\n", "\n"), "\n") {
		if !found {
			found = strings.HasPrefix(line, heading)
			continue
		}
		if strings.HasPrefix(line, "## ") || linkRef.MatchString(line) {
			break
		}
		body = append(body, line)
	}
	if !found {
		return "", fmt.Errorf("CHANGELOG.md has no %s section", heading)
	}
	notes := strings.TrimSpace(strings.Join(body, "\n"))
	if notes == "" {
		return "", fmt.Errorf("CHANGELOG.md section %s is empty", heading)
	}
	return notes + "\n", nil
}

// smoke checks a published release: the downloaded archives in dir must match
// checksums.txt, and the binary for this platform must run.
func smoke(ctx context.Context, args []string) (err error) {
	if len(args) != 2 {
		return errors.New("usage: smoke <download dir> <version>")
	}
	dir, version := args[0], strings.TrimPrefix(args[1], "v")
	if err := verifyChecksums(dir); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "whyfail-smoke-")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmp); rmErr != nil && err == nil {
			err = fmt.Errorf("remove temp dir: %w", rmErr)
		}
	}()
	bin, err := extractBinary(filepath.Join(dir, archiveName(version, runtime.GOOS, runtime.GOARCH)), tmp)
	if err != nil {
		return err
	}

	out, code, runErr := runBinary(ctx, bin, "--version")
	if err := checkExit("--version", code, runErr, 0); err != nil {
		return err
	}
	if want := "whyfail " + version + "\n"; out != want {
		return fmt.Errorf("--version printed %q, want %q", out, want)
	}

	out, code, runErr = runBinary(ctx, bin, "--json", "--host", unreachableHost, "doctor")
	if err := checkExit("doctor without Ollama", code, runErr, 1); err != nil {
		return err
	}
	if !json.Valid([]byte(out)) {
		return fmt.Errorf("doctor --json printed invalid JSON: %q", out)
	}

	// The wrapped command is whyfail itself with an unknown flag: it exits
	// with 2 on every OS, and the wrapper must pass that code through even
	// though the explanation fails.
	_, code, runErr = runBinary(ctx, bin, "--host", unreachableHost, "--", bin, "--no-such-flag")
	if err := checkExit("wrapper without Ollama", code, runErr, 2); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "smoke test passed for %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

// verifyChecksums checks every archive in dir against checksums.txt.
func verifyChecksums(dir string) error {
	text, err := os.ReadFile(filepath.Join(dir, checksumsFile))
	if err != nil {
		return fmt.Errorf("read %s: %w", checksumsFile, err)
	}
	sums, err := parseChecksums(string(text))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list %s: %w", dir, err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".tar.gz") && !strings.HasSuffix(name, ".zip") {
			continue
		}
		want, ok := sums[name]
		if !ok {
			return fmt.Errorf("%s is not listed in %s", name, checksumsFile)
		}
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s: checksum mismatch", name)
		}
		checked++
	}
	if checked == 0 {
		return fmt.Errorf("no archives found in %s", dir)
	}
	return nil
}

// parseChecksums reads "<sha256>  <file name>" lines into a map. File names
// must be plain names, never paths.
func parseChecksums(text string) (map[string]string, error) {
	sums := map[string]string{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s line %d: want \"<sha256>  <file>\"", checksumsFile, i+1)
		}
		sum, name := strings.ToLower(fields[0]), fields[1]
		if !sha256Hex.MatchString(sum) {
			return nil, fmt.Errorf("%s line %d: checksum is not a SHA-256", checksumsFile, i+1)
		}
		if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
			return nil, fmt.Errorf("%s line %d: file name is a path", checksumsFile, i+1)
		}
		if _, dup := sums[name]; dup {
			return nil, fmt.Errorf("%s line %d: %s is listed twice", checksumsFile, i+1, name)
		}
		sums[name] = sum
	}
	return sums, nil
}

// archiveName follows the name_template in .goreleaser.yaml.
func archiveName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("whyfail_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "whyfail.exe"
	}
	return "whyfail"
}

// extractBinary copies the whyfail binary at the root of archive into dir.
// Only that one entry is read, so no archive path ever reaches the file system.
func extractBinary(archive, dir string) (string, error) {
	data, err := os.ReadFile(archive)
	if err != nil {
		return "", fmt.Errorf("read archive: %w", err)
	}
	want := binaryName()
	var bin []byte
	if strings.HasSuffix(archive, ".zip") {
		bin, err = fromZip(data, want)
	} else {
		bin, err = fromTarGz(data, want)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", archive, err)
	}

	path := filepath.Join(dir, want)
	// The smoke test runs this file, so it needs the execute bit.
	if err := os.WriteFile(path, bin, 0o700); err != nil { //nolint:gosec // G306: see comment above.
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func fromZip(data []byte, name string) (_ []byte, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}
		defer func() { err = errors.Join(err, rc.Close()) }()
		return readLimited(rc, name)
	}
	return nil, fmt.Errorf("no %s at the root", name)
}

func fromTarGz(data []byte, name string) (_ []byte, err error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer func() { err = errors.Join(err, gz.Close()) }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("no %s at the root", name)
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if h.Name == name && h.Typeflag == tar.TypeReg {
			return readLimited(tr, name)
		}
	}
}

// readLimited reads at most maxBinaryBytes, so a crafted archive cannot fill
// the disk or memory.
func readLimited(r io.Reader, name string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBinaryBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(b) > maxBinaryBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, maxBinaryBytes)
	}
	return b, nil
}

func fileSHA256(path string) (_ string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runBinary runs bin and returns its stdout and exit code; stderr passes
// through to the log. The code is -1 when bin could not run at all.
func runBinary(ctx context.Context, bin string, args ...string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if cmd.ProcessState == nil {
		return "", -1, err
	}
	return stdout.String(), cmd.ProcessState.ExitCode(), err
}

// checkExit reports a step whose exit code is not want, or that did not run.
func checkExit(step string, code int, err error, want int) error {
	if code < 0 {
		return fmt.Errorf("%s: cannot run: %w", step, err)
	}
	if code != want {
		return fmt.Errorf("%s: exit code %d, want %d", step, code, want)
	}
	return nil
}
