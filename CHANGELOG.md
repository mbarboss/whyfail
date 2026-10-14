# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Evaluation set of real failures with expected answer properties, run against the local
  model with `go run ./tools/task eval [model]` to compare prompts and models.
- Text output wraps to the terminal width (at most 100 columns, 80 when redirected).
  Suggested commands are never wrapped and long paths or URLs are not split.

### Fixed

- Suggested fixes fit the environment better: only package managers that exist on the
  detected system, no fix that needs the missing program itself, no downloaded script
  piped into a shell, a PATH fix when a program is installed but not found, and no
  `sudo` for per-user tools such as `npm -g`, `pip` and `gem`. With the default model the
  evaluation set went from 11 to 16 of 16 cases.

## [0.1.0] - 2026-10-13

### Added

- Project foundation: Go module, task runner, linting, pre-commit hooks, CI on Linux,
  macOS and Windows, CodeQL and Dependabot.
- Pipe mode: `<command> 2>&1 | whyfail` explains a failure with a local Ollama model and
  prints the cause, a short explanation and up to three suggested fixes.
- Configuration through `--model`, `--host` and `--timeout` or `WHYFAIL_MODEL`,
  `OLLAMA_HOST` and `WHYFAIL_TIMEOUT`, validated at startup. Default model `gemma4:e4b`.
- Warnings on suggested fixes that use administrator privileges, run downloaded code
  (piped into a shell, through `$(curl ...)` or `eval`, or downloaded and then run), delete
  recursively or are otherwise risky.
- Secret redaction before prompting: private keys, URL credentials, authorization
  headers, common token formats and `password=`-style values are replaced by typed
  placeholders, with a notice on stderr listing the kinds removed.
- Ollama hosts that are not on this machine are refused unless `--allow-remote` is passed;
  every connection is checked, and `--allow-remote` prints a warning on each run.
  `OLLAMA_HOST=0.0.0.0` is treated as this machine.
- Wrapper mode: `whyfail -- <command> [args]` runs the command without a shell, streams
  its output and explains it only when it fails. whyfail exits with the command's exit
  code (127 when the command is not found, 126 when it cannot be started), and an
  interrupted command is not explained. The command line and exit code are part of the
  prompt, with secrets in the command line redacted.
- The prompt names the OS, CPU architecture, Linux distribution and shell, and asks for
  fixes in that shell's syntax. The shell is detected from the parent process (falling
  back to `$SHELL`) and can be set with `--shell` or `WHYFAIL_SHELL`.
- `whyfail doctor` checks that the Ollama host is on this machine, that Ollama 0.9.0 or
  later is running and that the model is installed, with an install, start, upgrade or
  `ollama pull` fix for each failed check. Exit code 0 when all checks pass, 1 otherwise.
- `--json` prints one JSON object (cause, explanation, fixes with danger warnings, model,
  command and exit code) or an error with a stable `error.code`, for pipe mode, wrapper
  mode and `doctor`. In wrapper mode the command's output then goes to stderr.
- `--apply` offers to run one suggested fix after the explanation, asking on the terminal
  which fix to run and for confirmation (default no; fixes with a danger warning need
  `yes`). The fix runs exactly as shown through the detected shell. It is refused without a
  terminal, with `--json`, with an unknown shell and for fixes that contain a redacted value
  or a placeholder such as `<branch-name>` (which also gets a warning).
- Fixes that install a package named in the error output (pip, uv, npm, pnpm, yarn, gem,
  cargo, go) get a warning, so `--apply` asks for `yes` before running them.
- `--version` prints the release version, or the module version for builds made with
  `go install` or `go build`.
- Release archives for Linux, macOS and Windows (amd64 and arm64), with SHA-256 checksums
  and build provenance attestations.
- Model output is stripped of terminal escape sequences and control characters before it
  is printed, and only the tail of the command output is sent to the model.

### Security

- Require Go toolchain 1.27.2, which fixes several `net/http` and `crypto/tls`
  vulnerabilities in 1.27.1.

[Unreleased]: https://github.com/mbarboss/whyfail/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/mbarboss/whyfail/releases/tag/v0.1.0
