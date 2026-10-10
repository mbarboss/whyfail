# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Project foundation: Go module, task runner, linting, pre-commit hooks, CI on Linux,
  macOS and Windows, CodeQL and Dependabot.
- Pipe mode: `<command> 2>&1 | whyfail` explains a failure with a local Ollama model and
  prints the cause, a short explanation and up to three suggested fixes.
- Configuration through `--model`, `--host` and `--timeout` or `WHYFAIL_MODEL`,
  `OLLAMA_HOST` and `WHYFAIL_TIMEOUT`, validated at startup. Default model `gemma4:e4b`.
- Warnings on suggested fixes that use administrator privileges, pipe into a shell,
  delete recursively or are otherwise risky.
- Model output is stripped of terminal escape sequences and control characters before it
  is printed, and only the tail of the command output is sent to the model.

### Security

- Require Go toolchain 1.27.2, which fixes several `net/http` and `crypto/tls`
  vulnerabilities in 1.27.1.
