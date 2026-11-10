# Contributing to orbits-cli

Thanks for helping improve the [Orbits](https://orbits.africa) CLI. Bug reports, fixes and new commands are all
welcome. Please read the [Code of Conduct](CODE_OF_CONDUCT.md) first, and report security issues privately as described
in [SECURITY.md](SECURITY.md), never in a public issue.

## Development setup

You need Go 1.27 or newer.

```sh
git clone https://github.com/The-Orbits/orbits-cli
cd orbits-cli
make build      # ./orbits
make test       # gofmt, go vet, go test -race
```

### Building against an unpublished orbits-go

The CLI depends on [`orbits-go`](https://github.com/The-Orbits/orbits-go), the Orbits Go SDK. Until the version named
in `go.mod` is published, `go.sum` has no entry for it and a plain `go build` fails. In that case clone `orbits-go`
next to this repository and point Go at it with a workspace file kept **outside** this repository:

```
go 1.27.1
use /path/to/orbits-cli
use /path/to/orbits-go
replace github.com/The-Orbits/orbits-go v0.1.0 => /path/to/orbits-go
```

```sh
GOWORK=/path/outside/the/repo/go.work make test
```

CI does the same automatically. Releases are blocked until `go.sum` is complete, so the SDK must be published (and
`go get github.com/The-Orbits/orbits-go@<version> && go mod tidy` run here) before the first release. **Never commit a
`go.work` file or a `replace` directive.**

## Checks

Run these before opening a pull request; CI runs the same ones.

```sh
gofmt -l .               # must print nothing
go vet ./...
go test -race ./...
make dist VERSION=ci     # every platform still builds
bash -n install.sh
goreleaser check         # if you have GoReleaser
```

The live test runs the real binary against a real team (staging by default) only when a token is set. It makes its own
upload project, deploys a small folder, reads its log, adds and pulls a variable, and deletes the project:

```sh
ORBITS_E2E_TOKEN=orb_live_… ORBITS_E2E_TEAM=<team> make e2e
```

## Layout

```
cmd/orbits/          main: runs the command tree, maps errors to exit codes
internal/cli/        the cobra commands, one file per area (deploy.go, env.go, domains.go, ...)
internal/api/        the orbits-go client the commands use, and API errors explained for people
internal/auth/       browser sign-in through the web app (PKCE, loopback callback)
internal/config/     profiles in ~/.config/orbits/config.json; tokens in the keychain or credentials.json
internal/link/       .orbits/project.json, and keeping .orbits out of Git
internal/mcpconfig/  AI apps' MCP config files: paths per OS, JSON and TOML merges
internal/output/     tables, colour, the build view, --json / --quiet
internal/update/     newer releases on GitHub, and installing them in place
e2e/                 the live test
install.sh           what https://orbits.africa/install serves
.goreleaser.yaml     release archives, checksums and the Homebrew cask
docs/                the browser sign-in protocol
```

## Rules

- **Every API call goes through orbits-go.** If the SDK lacks something, add it there first; `Client.Do` is the
  escape hatch for a call it does not cover yet. Never write a second HTTP client for the API.
- **The API is the source of truth.** `--json` prints what the API returned, and errors show the API's own `detail`
  and request id, explained (`internal/api`), never guessed.
- **Ask before anything that deletes something or changes production** (`app.confirm`), and honour `--yes`. With no
  terminal, refuse rather than guess.
- **stdout is for results, stderr for progress and hints**, so output pipes cleanly.
- **Secrets never in argv where avoidable, logs or output.** Tokens and variable values are read from stdin, printed
  only when asked, and written to files with mode `0600`.
- **The sign-in has a second half in the web app** (`/cli/authorize`, `/api/cli/token`), specified in
  [docs/cli-auth-protocol.md](docs/cli-auth-protocol.md). A change to the exchange is a change to both, and to that
  document.
- **Tests answer with the API's own shapes.** `internal/cli/testdata/fixtures` holds orbits-go's spec-checked fixtures
  and more written to the same schemas (named `<operationId>[.label].json`).
- **A change people will notice goes in [CHANGELOG.md](CHANGELOG.md)**, under Unreleased, in the same pull request.

## Releasing

Rename **Unreleased** in CHANGELOG.md to the version and date (`## v0.2.0 — 2026-12-01`), merge, then push the tag
`v0.2.0`. The release workflow runs GoReleaser: archives for macOS and Linux (amd64, arm64) and `checksums.txt` on the
GitHub release, and the Homebrew cask in `The-Orbits/homebrew-tap`. A tag with a hyphen (`v0.2.0-rc.1`) is a pre-release.
