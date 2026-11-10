#!/usr/bin/env node
// The `orbits` command of the npm package @useorbits/cli. The CLI itself is a Go binary: npm installs it from the one
// platform package that matches this machine (an optional dependency, picked by its os and cpu fields), and this
// runs it with the same arguments, input and output, and exits as it does.
"use strict";

const { spawnSync } = require("node:child_process");
const fs = require("node:fs");

const PLATFORMS = new Set(["darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"]);
const platform = `${process.platform}-${process.arch}`;
const INSTALL = "curl -fsSL https://orbits.africa/install | bash";

function fail(message) {
  process.stderr.write(`error: ${message}\n`);
  process.exit(1);
}

if (!PLATFORMS.has(platform)) {
  fail(`the Orbits CLI runs on macOS and Linux (arm64 and x64), not ${platform}`);
}

let bin;
try {
  bin = require.resolve(`@useorbits/cli-${platform}/bin/orbits`);
} catch {
  fail(
    `@useorbits/cli-${platform}, which holds the CLI for this machine, is not installed.\n` +
      `It is an optional dependency: reinstall without --no-optional or --omit=optional,\n` +
      `  npm install -g @useorbits/cli\n` +
      `or install the CLI without npm:\n` +
      `  ${INSTALL}`,
  );
}

// A package manager that drops the executable bit (some do, unpacking a tarball) would leave it unrunnable.
try {
  fs.accessSync(bin, fs.constants.X_OK);
} catch {
  try {
    fs.chmodSync(bin, 0o755);
  } catch {
    // Not ours to change: the spawn below says why it cannot run.
  }
}

const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" });
if (result.error) {
  fail(`could not run ${bin}: ${result.error.message}`);
}
if (result.signal) {
  // Ended by a signal (Ctrl-C): end the same way, so a shell or script sees it.
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
