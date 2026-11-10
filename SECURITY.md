# Security policy

## Reporting a vulnerability

Please report security vulnerabilities privately, through GitHub's
[private vulnerability reporting](https://github.com/The-Orbits/orbits-cli/security/advisories/new) for this
repository. **Do not open a public issue, discussion or pull request** for a suspected vulnerability.

Include as much of the following as you can:

- the output of `orbits version`, and your operating system;
- a description of the issue and its impact;
- steps to reproduce it.

We will acknowledge your report within three business days, keep you informed while we work on a fix, and credit you
in the release notes if you would like. Please give us a reasonable amount of time to release a fix before any public
disclosure.

Never include a real API token, environment variable values, the contents of `.env` files or other people's data in a
report. If a token may have been exposed, revoke it under Team › Settings › API tokens straight away.

## How the CLI handles secrets

- Tokens are stored in the macOS Keychain or the Linux Secret Service, or, where there is none, in
  `~/.config/orbits/credentials.json` with mode `0600`. `ORBITS_TOKEN` is read from the environment and never stored.
- The browser sign-in never puts the token in a URL: the web app hands the CLI a two-minute, single-use code bound to a
  PKCE challenge, which only this CLI's verifier can trade ([docs/cli-auth-protocol.md](docs/cli-auth-protocol.md)).
  The loopback server listens on `127.0.0.1` only and refuses a callback whose `state` is not its own.
- The token is sent only to the API, in the `Authorization` header. Uploads go to storage on a presigned URL without it.
- Variable values are printed only when asked (`env ls --show-values`), and `env pull` writes files with mode `0600`,
  refusing to write through a symbolic link.
- `orbits update` and `install.sh` check each download against the release's `checksums.txt` before installing it.

## Supported versions

Security fixes are released for the latest minor version of the CLI.

| Version | Supported |
| ------- | --------- |
| v0.1.x (latest) | Yes |
| Older | No |
