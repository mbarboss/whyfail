# whyfail

whyfail explains why a terminal command failed and suggests a fix, using a local LLM
through [Ollama](https://ollama.com). The command output never leaves your machine.

> Status: in early development. Nothing is released yet; the planned work is tracked in
> [issues](https://github.com/mbarboss/whyfail/issues).

## Usage

Put the command after `--`. whyfail runs it, shows its output as it arrives, and explains
it only when it fails:

```sh
whyfail -- npm install
```

Or pipe the output of a command that already failed into whyfail. Include stderr (`2>&1`),
since that is where most errors go:

```sh
npm install 2>&1 | whyfail
```

Either way the answer looks like this:

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

### Wrapping a command

- The command runs directly, without a shell, so pipes, redirections and shell built-ins
  are not available. To use them, wrap a shell yourself, for example
  `whyfail -- bash -c 'make 2>&1 | tee build.log'` or `whyfail -- pwsh -c 'Get-Item x'`.
- The command's output goes through a pipe rather than straight to the terminal, so some
  programs turn off colors or progress bars.
- Ctrl+C reaches the command as usual. An interrupted command is not explained.
- whyfail exits with the command's exit code, even when the explanation itself fails (for
  example, Ollama is not running), so wrapping a command does not change what scripts see.
  A command killed by a signal is reported as 128 plus the signal number, like a shell does.

### Exit codes

| Code | Meaning |
|---|---|
| Command's code | Wrapper mode: the command ran (explained or not) |
| 0 | Pipe mode: the failure was explained |
| 1 | Runtime error: Ollama unreachable, model missing, timeout, unusable answer (pipe mode) |
| 2 | Usage or configuration error, non-loopback host without `--allow-remote`, or empty input |
| 126 | Wrapper mode: the command was found but could not be started (for example, no execute permission) |
| 127 | Wrapper mode: command not found |
| 130 | Interrupted (pipe mode) |

## Requirements

- [Ollama](https://ollama.com/download) 0.9.0 or later running locally, with a model pulled.

| OS | Install Ollama |
|---|---|
| Ubuntu / Debian | `curl -fsSL https://ollama.com/install.sh \| sh` |
| macOS | `brew install ollama` |
| Windows | `winget install Ollama.Ollama` |

Then pull the default model (6.6 GB download; runs well on a GPU with 8 GB of VRAM):

```sh
ollama pull gemma4:e4b
```

Run `whyfail doctor` to check the setup. It checks that the Ollama host is on this
machine, that Ollama is running and recent enough, and that the model is installed, and
prints a fix for each failed check:

```text
ok    Ollama host is on this machine (http://127.0.0.1:11434)
ok    Ollama 0.40.2 is running
FAIL  Model gemma4:e4b is not installed
      Fix: ollama pull gemma4:e4b
info  Environment: Linux (Ubuntu 24.04.1 LTS), amd64, shell bash
```

It exits with 0 when every check passes and 1 otherwise. Flags such as `--model` and
`--host` apply to it too.

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
| `--allow-remote` | (none) | off | Allow a host that is not on this machine |
| `--timeout` | `WHYFAIL_TIMEOUT` | `2m` | Maximum time to wait for an answer (1s to 10m) |
| `--shell` | `WHYFAIL_SHELL` | detected | Shell to write fixes for: `bash`, `zsh`, `fish`, `powershell` (or `pwsh`), `cmd` |

Along with the output, the model is told your OS, CPU architecture and shell, plus the
distribution name from `/etc/os-release` on Linux, so fixes use the right package manager
and syntax. The shell is the program that started whyfail; when that is not a shell (for
example `make` or `npm`), whyfail falls back to `$SHELL`, and otherwise reports it as
unknown. Set `--shell` when the detection is wrong.

`OLLAMA_HOST` accepts the same forms as the Ollama CLI, such as `127.0.0.1:11500`.
`0.0.0.0`, often set so the Ollama server listens on every interface, means this machine.

whyfail only talks to Ollama on this machine. A host that is not a loopback address, or a
name that resolves to anything other than loopback, is refused with exit code 2. The check
runs again on every connection, so DNS changes and redirects cannot send the output
elsewhere. To use Ollama on another machine, pass `--allow-remote` on each run; there is
no environment variable for it, so the choice is always explicit, and whyfail prints a
warning every time. whyfail also ignores `HTTP_PROXY` and `HTTPS_PROXY`.

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
