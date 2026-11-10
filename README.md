# orbits

[![CI](https://github.com/The-Orbits/orbits-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/The-Orbits/orbits-cli/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

The command-line tool for [Orbits](https://orbits.africa): deploy a folder or a branch and watch it build, read build
and runtime logs, manage environment variables, domains and DNS, feature flags, workflows, sandboxes and alerts, and
see usage and activity, from your terminal or from CI.

```console
$ orbits login
$ cd my-site && orbits link
$ orbits deploy
  ✓ Packed 14 files               48 KB
  ✓ Uploaded                      48 KB
  ✓ Queued                        1s
  ✓ Installing                    6s
  ✓ Building                      12s
  ✓ Going live                    2s
✓ Ready in 23s (production)
https://my-site.theorbits.app
```

## Install

macOS and Linux, amd64 and arm64:

```sh
curl -fsSL https://orbits.africa/install | bash
```

The script downloads the build for your machine from this repository's
[releases](https://github.com/The-Orbits/orbits-cli/releases), checks it against the release's `checksums.txt`, puts
it in `~/.orbits/bin` and adds that to your `PATH`. A given version: `… | bash -s -- v0.1.0`. `ORBITS_INSTALL` changes
the folder, and `ORBITS_NO_MODIFY_PATH=1` leaves your shell's startup file alone.

With Homebrew:

```sh
brew install The-Orbits/tap/orbits
```

Or download an archive from the releases page yourself. `orbits update` installs a newer release in place (checked
against its checksums too); a Homebrew install is upgraded with `brew upgrade orbits`. The CLI says when a newer
version is out, at most once a day; `ORBITS_NO_UPDATE_NOTIFIER=1` turns that off.

## Sign in

```sh
orbits login
```

opens the web app in your browser, where you choose the **team**, the **access** (full, or read only) and how long the
token lasts. The CLI receives an API token for that team and keeps it in the macOS Keychain or the Linux Secret
Service, or, on a machine without one, in `~/.config/orbits/credentials.json` (mode `0600`). The token never appears in
a URL; the protocol is in [docs/cli-auth-protocol.md](docs/cli-auth-protocol.md).

A token acts inside **one team**. To work in several, sign in once per team, each in a profile, and switch:

```sh
orbits --profile side-project login     # choose the other team in the browser
orbits teams list
orbits teams switch side-project
```

Without a browser, make a token under **Team › Settings › API tokens** and pass it on stdin:
`orbits login --with-token < token.txt`. `orbits whoami` shows who and which team the CLI acts as; `orbits logout`
forgets the token on this machine (revoke it in Settings to end it everywhere).

A **read** token can only read: deploying, changing variables and reading their values need full access. Some things
no token can do, by design (managing tokens and members, deleting the team, integrations): the CLI says so and points
to the web app.

## Link a folder

```sh
orbits link              # pick a project, or make a new one
orbits link storefront   # by slug
orbits link --create     # a new upload project named after the folder
```

writes `.orbits/project.json` (the team's and project's ids) and adds `.orbits` to `.gitignore`. Commands run in the
folder, or below it, then act on that project; `--project <slug>` (and `--team <slug>`) choose another for one command.
`orbits unlink` removes the link.

## Deploy

```sh
orbits deploy            # this folder
orbits deploy ./dist     # another folder
```

For an **upload project** the folder is zipped and sent, and Orbits builds it like any other deployment. Left out:
`node_modules`, `.git`, `.next`, `__MACOSX`, `.DS_Store`, `.orbits`, `.env.local` and `.env.*.local` (the files
`orbits env pull` writes), and the patterns in the folder's `.orbitsignore` (`.gitignore` syntax). Uploads go straight
to production; the limits are a 100 MiB zip, 500 MiB and 20,000 files unpacked.

A **Git project** builds from its repository, so pushing deploys it. From the CLI:

```sh
orbits deploy --prod                    # the production branch's newest commit
orbits deploy --branch feature/checkout # a preview of another branch
orbits deployments redeploy             # build production's deployment again
```

While it builds, the CLI shows each step, with a spinner and the time, and the build log under it (`--no-logs` for
steps only), on stderr. When it is ready it prints the address on **stdout**, so `url=$(orbits deploy)` works. A failed
build exits with code 5 and says where it failed. `--no-wait` returns once the deployment is queued.

## Commands

| Command | What it does |
| --- | --- |
| `orbits login [--with-token]` · `logout` · `whoami` | Sign in through the browser (or with a token), sign out, see who the CLI acts as |
| `orbits teams list` · `teams switch <team>` | Your signed-in teams; make one current |
| `orbits link [project]` · `unlink` | Tie the folder to a project, or stop |
| `orbits projects list` · `get` · `create <name>` · `delete <project>` · `pause` · `resume` | Projects; `create --repo owner/name` for a Git project |
| `orbits deploy [dir] [--prod] [--branch b]` | Upload a folder or build a branch, follow it, print the address |
| `orbits deployments list` · `get` · `promote` · `rollback` · `redeploy` · `cancel` | Deployments and changing what production serves |
| `orbits logs [deployment] [-f]` | A build's log, following it with `-f` |
| `orbits logs --runtime [-f] [--since 1h\|24h\|7d] [--level] [--source] [--search]` | The running project's request and function logs |
| `orbits env ls [--show-values]` · `add <KEY> [value]` · `rm <KEY>` · `pull [file]` · `restore <KEY>` | Environment variables |
| `orbits domains ls` · `add <domain>` · `rm` · `verify` · `records ls\|add\|rm` | Domains, what DNS they need, and Orbits DNS records |
| `orbits flags ls` · `get` · `create` · `set` · `rm` · `rules add\|rm` | Feature flags per environment |
| `orbits workflows ls` · `runs [run]` · `run <workflow>` · `cancel` · `retry` | Workflows and their runs |
| `orbits sandboxes ls` · `create` · `start` · `stop` · `rm` | Sandboxes |
| `orbits alerts ls` · `create` · `enable` · `disable` · `events` · `rm` | Alerts on error rate, latency and traffic |
| `orbits usage [--month YYYY-MM]` · `activity` | The team's usage by project, and what happened |
| `orbits open [--site]` | The project in the web app, or its production site |
| `orbits mcp install <app>` · `mcp status` · `mcp list-clients` | Connect an AI app to Orbits (below) |
| `orbits completion <shell>` · `version` · `update` | Shell completion, the version, a newer version |

`orbits <command> --help` has the details and examples of each.

### Environment variables

Values are never printed unless you ask. `env add` reads the value from stdin when it is not on the command line
(hidden at a terminal; `printf %s "$VALUE" | orbits env add KEY` in scripts). Variables are **sensitive** by default:
write-only, so nobody can read them back; `--plain` makes one readable. `env pull` writes an environment's readable
variables (default: development) to `.env.local`, mode `0600`, names the sensitive ones it had to leave out, and warns
if Git does not ignore the file. Reading values needs a full-access token.

## Scripts and CI

```sh
export ORBITS_TOKEN=orb_live_…    # a token from Team › Settings › API tokens
orbits --project storefront deploy ./dist --no-logs
```

- `ORBITS_TOKEN` is used instead of any stored sign-in, and nothing is stored. Its team is the one the token belongs
  to; `--team` or `ORBITS_TEAM` name it explicitly.
- `--json` prints JSON on stdout for every command, and `-q` only ids. Tables, symbols and colour are for terminals;
  piped output is plain, and `NO_COLOR` turns colour off.
- Results go to stdout, progress and hints to stderr.
- Anything that deletes something or changes production asks first. `--yes` answers for you; without a terminal and
  without `--yes`, the command refuses rather than guess.

Exit codes: `0` it worked, `1` an error, `2` bad usage, `3` not signed in or the token was refused (401) or may not do
this (403), `4` not found, `5` a build or operation finished but failed, `130` interrupted.

| Variable | What it does |
| --- | --- |
| `ORBITS_TOKEN` | The API token to use; skips stored sign-ins |
| `ORBITS_TEAM` | The team, as `--team` |
| `ORBITS_PROFILE` | The profile, as `--profile` |
| `ORBITS_API_URL` | The API, with `/v1`. Default `https://api.theorbits.app/v1`; staging is `https://stagingapi.theorbits.app/v1` |
| `ORBITS_WEB_URL` | The web app used to sign in and by `open`. Default `https://orbits.africa` |
| `ORBITS_CONFIG_DIR` | Where profiles live. Default `$XDG_CONFIG_HOME/orbits`, else `~/.config/orbits` |
| `ORBITS_NO_KEYRING` | Keep tokens in `credentials.json` instead of the system keychain |
| `ORBITS_NO_UPDATE_NOTIFIER` | Don't say when a newer version is out |

## AI apps (MCP)

`orbits mcp install <app>` adds the Orbits MCP server to an AI app, so it can list, deploy and manage your projects for
you. By default it adds the hosted server, `https://mcp.theorbits.app/mcp`: the app opens your browser, and you approve
it in the web app, choosing its team and access. With `--local`, the app runs `orbits-mcp stdio` on your machine with
your signed-in profile or `ORBITS_TOKEN` ([orbits-mcp](https://github.com/The-Orbits/orbits-mcp)).

| App | `orbits mcp install …` | How |
| --- | --- | --- |
| Claude Code | `claude-code` | `claude mcp add` (user scope), else `~/.claude.json` |
| Claude Desktop | `claude --local` | `claude_desktop_config.json`; for the hosted server, the steps to add a connector are printed |
| Cursor | `cursor` | `~/.cursor/mcp.json` |
| VS Code | `vscode` | the user `mcp.json` |
| Windsurf | `windsurf` | `~/.codeium/windsurf/mcp_config.json` |
| Codex | `codex` | `~/.codex/config.toml` (then `codex mcp login orbits`) |
| Gemini CLI | `gemini` | `gemini mcp add`, else `~/.gemini/settings.json` |
| ChatGPT | `chatgpt` | the steps to add a connector are printed |
| Grok | `grok` | the remote MCP tool for the API is printed |

Every other server and setting in the app's file is kept, and the previous file is saved as `<file>.bak`. `--print`
shows what would be added and changes nothing; `orbits mcp status` shows which apps have Orbits.

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). In short: Go 1.27, then `make build` and `make test`. The CLI is built on the
[Orbits Go SDK](https://github.com/The-Orbits/orbits-go): every API call goes through it. Until the `orbits-go`
version in `go.mod` is published, build against a checkout of it with a `go.work` file kept outside this repository
(CONTRIBUTING.md has the steps).

## License

[MIT](LICENSE)
