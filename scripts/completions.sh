#!/bin/sh
# Writes shell completions to completions/, for the release archives. The cask
# installs these files instead of running orbits during "brew install": the
# binary is not notarized, so running it before quarantine is cleared makes
# macOS block it.
set -eu
rm -rf completions
mkdir completions
for sh in bash zsh fish; do
  go run ./cmd/orbits completion "$sh" > "completions/orbits.$sh"
done
