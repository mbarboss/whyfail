# whyfail

whyfail explains why a terminal command failed and suggests a fix, using a local LLM
through [Ollama](https://ollama.com). The command output never leaves your machine.

> Status: in early development. Nothing is released yet; the planned work is tracked in
> [issues](https://github.com/mbarboss/whyfail/issues).

## Usage

Pipe the output of a failed command into whyfail. Include stderr (`2>&1`), since that is
where most errors go.

```sh
npm install 2>&1 | whyfail
```

```text
Cause: The Docker socket is not accessible to your user.

Your user is not in the docker group, so the operating system denies access to the
daemon.

Suggested fixes (review before running):

  1. Add your user to the docker group.
     sudo usermod -aG docker $USER
     Warning: runs with administrator privileges.

  2. Start a shell with the new group membership.
     newgrp docker
```

whyfail never runs the suggested commands. They come from a language model, so read them
before you run them. Commands that use administrator privileges, pipe downloads into a
shell, delete recursively, force-stop processes, discard Git changes, write to disks or
edit files in place get a warning line.

Only the last 200 lines (at most 16 KiB) of the output are sent to the model, and secrets
are removed first. whyfail replaces private keys, credentials in URLs, `Authorization`
headers, GitHub, GitLab, AWS, Slack, Stripe, Google, npm and PyPI tokens, JWTs, LLM API
keys and `password=`/`secret:`/`token=` style values with placeholders such as
`[REDACTED:github-token]`, and tells you on stderr what kinds it removed:

```text
whyfail: redacted 1 secret (url-credentials) before asking the model.
```

Redaction is pattern-based. It catches common formats, not every possible secret, so avoid
piping output that you know holds unusual credentials.

Planned: a wrapper mode (`whyfail -- go build ./...`) that runs the command and explains
it only when it fails.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | The failure was explained |
| 1 | Runtime error: Ollama unreachable, model missing, timeout, unusable answer |
| 2 | Usage or configuration error, or nothing to explain |
| 130 | Interrupted |

## Requirements

- [Ollama](https://ollama.com/download) running locally, with a model pulled.

| OS | Install Ollama |
|---|---|
| Ubuntu / Debian | `curl -fsSL https://ollama.com/install.sh \| sh` |
| macOS | `brew install ollama` |
| Windows | `winget install Ollama.Ollama` |

Then pull the default model (6.6 GB download; runs well on a GPU with 8 GB of VRAM):

```sh
ollama pull gemma4:e4b
```

On machines with less memory, `qwen3.5:4b` (3.3 GB) is a lighter alternative; select it
with `--model qwen3.5:4b` or `WHYFAIL_MODEL=qwen3.5:4b`. The first request after Ollama
starts takes longer while the model loads.

## Configuration

whyfail reads its settings from flags or environment variables (flags win). See
[`.env.example`](.env.example) for the full list.

| Flag | Variable | Default | Meaning |
|---|---|---|---|
| `--model` | `WHYFAIL_MODEL` | `gemma4:e4b` | Ollama model used for explanations |
| `--host` | `OLLAMA_HOST` | `http://127.0.0.1:11434` | Ollama server; the captured output is sent here |
| `--timeout` | `WHYFAIL_TIMEOUT` | `2m` | Maximum time to wait for an answer (1s to 10m) |

`OLLAMA_HOST` accepts the same forms as the Ollama CLI, such as `127.0.0.1:11500`. whyfail
ignores `HTTP_PROXY` and `HTTPS_PROXY`, so the output only goes to that host.

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
| Run locally | `<command> 2>&1 \| go run ./cmd/whyfail` |
| List all targets | `go run ./tools/task` |

The pre-commit hook formats staged Go files, runs the linters, scans staged changes for
secrets and rejects files larger than 1 MiB.

## License

[MIT](LICENSE)
