# Changelog

What changed in each version of the Orbits CLI. Every pull request that changes what people see adds a line under
**Unreleased**. Releasing renames that heading to the version and its date (`## v0.2.0 — 2026-12-01`) and pushes the
tag `v0.2.0`.

## Unreleased

## v0.1.5 — 2026-11-23

### Fixed

- `brew install The-Orbits/tap/orbits` on macOS: it no longer stops at "Apple could not verify orbits". The cask
  installs shell completions from the archive instead of running the not-yet-cleared binary to make them. The
  archives now include `completions/` (bash, zsh, fish).

## v0.1.4 — 2026-11-23

### Fixed

- Homebrew: the first release to publish the cask (v0.1.3's was not pushed to the tap). No change to the CLI itself.

## v0.1.3 — 2026-11-23

### Added

- Homebrew: `brew install The-Orbits/tap/orbits` installs the CLI (the first release to publish the cask). No change to
  the CLI itself.

## v0.1.2 — 2026-11-23

### Changed

- Releases publish to npm through trusted publishing (no npm token kept in CI), with provenance, and update the
  Homebrew cask in `The-Orbits/homebrew-tap`. No change to the CLI itself.

## v0.1.1 — 2026-11-23

### Fixed

- The first published release: v0.1.0's release did not build, so `curl -fsSL https://orbits.africa/install | bash`
  found nothing to install. v0.1.1 is v0.1.0 as described below.

## v0.1.0 — 2026-11-10

### Added

- `orbits whoami` shows the token's id, access (full or read only) and the project it is limited to, from the API,
  also for `ORBITS_TOKEN`. `orbits login --with-token` records the token's id and access in the profile.
- `orbits link` and `orbits projects list` work with a token limited to one project (the API now lists that project).
- `orbits login`: browser sign-in through the web app (PKCE, a loopback callback, the token never in a URL), choosing
  the team, access and expiry there; `--with-token` reads a token from stdin. Tokens are kept in the macOS Keychain or
  the Linux Secret Service, else in `credentials.json` (mode `0600`). `logout`, `whoami`, profiles (`--profile`) and
  `teams list` / `teams switch`, one team per profile.
- `orbits link`: ties a folder to a project in `.orbits/project.json` and adds `.orbits` to `.gitignore`; `unlink`.
- `orbits deploy [dir]`: uploads a folder to an upload project (leaving out `node_modules`, `.git`, `.env*.local` and
  `.orbitsignore` patterns), or builds a Git project's branch (`--prod`, `--branch`), then follows the build with a
  live step view and its log, prints the address on stdout, and exits 5 if the build fails.
- `orbits projects`, `deployments` (list, get, promote, rollback, redeploy, cancel), `logs` (build logs, `-f` to
  follow, `--runtime` for request and function logs), `env` (ls, add, rm, pull to a `0600` dotenv file, restore),
  `domains` (with DNS records), `flags` (with rules), `workflows`, `sandboxes`, `alerts`, `usage`, `activity` and
  `open`.
- `orbits mcp install <app>`: adds the hosted Orbits MCP server (`https://mcp.theorbits.app/mcp`) or the local
  `orbits-mcp` to AI apps' settings; `mcp status`, `mcp list-clients`.
- `--json` and `--quiet` on every command, confirmations before anything that deletes or changes production (`--yes`
  to skip; never without a terminal), and exit codes for scripts.
- `orbits update` and `install.sh` (served at `https://orbits.africa/install`), both checking downloads against the
  release's `checksums.txt`; releases by GoReleaser with a Homebrew cask in `The-Orbits/homebrew-tap`
  (`brew install The-Orbits/tap/orbits`).
- `ORBITS_TOKEN`, `ORBITS_API_URL`, `ORBITS_WEB_URL`, `ORBITS_TEAM` and `ORBITS_PROFILE` for CI and staging.
- On npm: `npm install -g @useorbits/cli`, or `npx @useorbits/cli`, with a platform package per machine
  (`@useorbits/cli-darwin-arm64`, `-darwin-x64`, `-linux-arm64`, `-linux-x64`) holding the same binary as the GitHub
  release. `orbits update` and the new-version notice name the right upgrade for how the CLI was installed: `brew
  upgrade orbits`, `npm install -g @useorbits/cli@latest`, or `orbits update`.
