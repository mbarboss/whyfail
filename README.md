# whyfail

whyfail explains why a terminal command failed and suggests a fix, using a local LLM
through [Ollama](https://ollama.com). The command output never leaves your machine.

> Status: in early development. Nothing is released yet; the planned work is tracked in
> [issues](https://github.com/mbarboss/whyfail/issues).

## Planned usage

```sh
# Wrap a command: whyfail runs it and explains the failure, if any.
whyfail -- go build ./...

# Or pipe the output of a command that already ran.
npm install 2>&1 | whyfail
```

## Requirements

- [Ollama](https://ollama.com/download) running locally, with a model pulled.

| OS | Install Ollama |
|---|---|
| Ubuntu / Debian | `curl -fsSL https://ollama.com/install.sh \| sh` |
| macOS | `brew install ollama` |
| Windows | `winget install Ollama.Ollama` |

## Configuration

whyfail reads its settings from flags or environment variables (flags win). See
[`.env.example`](.env.example) for the full list.

| Variable | Default | Meaning |
|---|---|---|
| `WHYFAIL_MODEL` | to be defined | Ollama model used for explanations |
| `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama server; non-loopback hosts need `--allow-remote` |
| `WHYFAIL_TIMEOUT` | `60s` | Maximum time to wait for an answer |

## Development

You need Go 1.27 or newer and Git.

| OS | Install Go and Git |
|---|---|
| Ubuntu / Debian | `sudo apt install git` and Go from [go.dev/dl](https://go.dev/dl/) (the apt package is usually older than 1.27) |
| macOS | `brew install go git` |
| Windows | `winget install GoLang.Go Git.Git` |

Every development tool (golangci-lint, govulncheck, gitleaks, lefthook) is pinned in its
own module under `tools/` and runs through `go tool`, so there is nothing else to install.
All commands run from the repository root and work the same on every OS.

| Purpose | Command |
|---|---|
| Install git hooks (once) | `go run ./tools/task hooks` |
| Format code | `go run ./tools/task format` |
| Full check (tidy, format, lint, tests, vulnerabilities, secrets) | `go run ./tools/task check` |
| Integration tests (need a running Ollama) | `go run ./tools/task integration` |
| Run locally | `go run ./cmd/whyfail -version` |
| List all targets | `go run ./tools/task` |

The pre-commit hook formats staged Go files, runs the linters, scans staged changes for
secrets and rejects files larger than 1 MiB.

## License

[MIT](LICENSE)
