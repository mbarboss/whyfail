# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems.

Report it privately through GitHub: open the repository's **Security** tab and choose
**Report a vulnerability**. Include the whyfail version, your OS, the steps to reproduce
and the impact you expect.

You should get a first reply within 7 days. Once a fix is released, the advisory is
published with credit to the reporter unless you prefer to stay anonymous.

## Supported versions

Only the latest release receives security fixes.

## Scope

Of particular interest:

- Ways to make whyfail send data to a host other than the configured local Ollama.
- Secrets in command output that survive redaction.
- Model output that can inject terminal escape sequences or run a command without the
  user's confirmation.
