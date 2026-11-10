#!/usr/bin/env bash
# Lays out the packages npm/build.mjs made as an install would (node_modules/@useorbits/cli and this machine's
# platform package) and runs `orbits version` through the launcher, as `npm install -g` would run it.
#
#   npm/smoke.sh dist/npm
set -euo pipefail

out="${1:-dist/npm}"
case "$(uname -s)" in Darwin) os=darwin ;; Linux) os=linux ;; *) echo "no package for $(uname -s)" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64 | amd64) cpu=x64 ;; arm64 | aarch64) cpu=arm64 ;; *) echo "no package for $(uname -m)" >&2; exit 1 ;; esac

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/node_modules/@useorbits"
cp -R "$out/cli" "$tmp/node_modules/@useorbits/cli"
cp -R "$out/cli-$os-$cpu" "$tmp/node_modules/@useorbits/cli-$os-$cpu"

node "$tmp/node_modules/@useorbits/cli/bin/orbits.js" version

# Without the platform package, the launcher has to say what to do.
rm -rf "$tmp/node_modules/@useorbits/cli-$os-$cpu"
if node "$tmp/node_modules/@useorbits/cli/bin/orbits.js" version 2>"$tmp/err"; then
  echo "the launcher ran without its platform package" >&2; exit 1
fi
grep -q "is not installed" "$tmp/err"
echo "npm packages OK"
