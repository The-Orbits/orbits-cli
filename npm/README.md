# @useorbits/cli

The command-line tool for [Orbits](https://orbits.africa): deploy a folder or a branch and watch it build, read build
and runtime logs, and manage environment variables, domains and DNS, feature flags, workflows and alerts, from your
terminal or from CI.

```sh
npm install -g @useorbits/cli
orbits login
cd my-site && orbits link
orbits deploy
```

Or without installing: `npx @useorbits/cli deploy`.

It runs on macOS and Linux, on arm64 and x64, with Node.js 18 or later. npm installs the CLI's binary for your machine
from one of its platform packages (`@useorbits/cli-darwin-arm64` and so on), so keep optional dependencies on.

To upgrade: `npm install -g @useorbits/cli@latest`. The CLI says when a newer version is out.

Other ways to install: `curl -fsSL https://orbits.africa/install | bash`, or `brew install The-Orbits/tap/orbits`.

- Docs: https://docs.orbits.africa/cli
- Source and releases: https://github.com/The-Orbits/orbits-cli
