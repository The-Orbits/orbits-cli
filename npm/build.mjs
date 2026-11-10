#!/usr/bin/env node
// Builds the npm packages from a release's archives:
//
//   node npm/build.mjs --version v0.1.0 --dist dist --out dist/npm
//
// --dist holds orbits_<os>_<arch>.tar.gz and checksums.txt, as GoReleaser and `make dist` name them (and the GitHub
// release has them). Each archive is checked against checksums.txt, its binary goes into a platform package
// (@useorbits/cli-<os>-<cpu>, with os and cpu set so npm installs only the one that matches), and @useorbits/cli,
// the package people install, depends on all four as optional dependencies and runs the right one (npm/orbits.js).
//
// It prints the package folders, platform packages first: publish them in that order, so the main package never
// points at versions that are not there yet.

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.dirname(here);

const { values } = parseArgs({
  options: {
    version: { type: "string" },
    dist: { type: "string", default: "dist" },
    out: { type: "string", default: "dist/npm" },
  },
});

const tag = values.version ?? "";
const version = tag.replace(/^v/, "");
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  console.error(`--version: "${tag}" is not a version like v0.1.0 or v0.2.0-rc.1`);
  process.exit(1);
}

const NAME = "@useorbits/cli";
const REPOSITORY = { type: "git", url: "git+https://github.com/The-Orbits/orbits-cli.git" };
const COMMON = {
  version,
  license: "MIT",
  homepage: "https://docs.orbits.africa/cli",
  repository: REPOSITORY,
  bugs: { url: "https://github.com/The-Orbits/orbits-cli/issues" },
};

// Go's names for the platforms, and Node's (process.platform, process.arch), which npm's os and cpu fields use.
const PLATFORMS = [
  { goos: "darwin", goarch: "arm64", os: "darwin", cpu: "arm64" },
  { goos: "darwin", goarch: "amd64", os: "darwin", cpu: "x64" },
  { goos: "linux", goarch: "arm64", os: "linux", cpu: "arm64" },
  { goos: "linux", goarch: "amd64", os: "linux", cpu: "x64" },
];

const dist = path.resolve(values.dist);
const out = path.resolve(values.out);
fs.rmSync(out, { recursive: true, force: true });
fs.mkdirSync(out, { recursive: true });

const sums = new Map(
  fs
    .readFileSync(path.join(dist, "checksums.txt"), "utf8")
    .split("\n")
    .filter(Boolean)
    .map((line) => {
      const [sum, file] = line.trim().split(/\s+/);
      return [file, sum];
    }),
);

const writeJSON = (file, data) => fs.writeFileSync(file, `${JSON.stringify(data, null, 2)}\n`);
const license = path.join(root, "LICENSE");
const folders = [];

for (const p of PLATFORMS) {
  const archive = `orbits_${p.goos}_${p.goarch}.tar.gz`;
  const file = path.join(dist, archive);
  const data = fs.readFileSync(file);
  const sum = createHash("sha256").update(data).digest("hex");
  if (sums.get(archive) !== sum) {
    console.error(`${archive}: its sha256 is not the one in checksums.txt`);
    process.exit(1);
  }

  const name = `${NAME}-${p.os}-${p.cpu}`;
  const dir = path.join(out, `cli-${p.os}-${p.cpu}`);
  fs.mkdirSync(path.join(dir, "bin"), { recursive: true });

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "orbits-npm-"));
  execFileSync("tar", ["-xzf", file, "-C", tmp, "orbits"]);
  fs.copyFileSync(path.join(tmp, "orbits"), path.join(dir, "bin", "orbits"));
  fs.chmodSync(path.join(dir, "bin", "orbits"), 0o755);
  fs.rmSync(tmp, { recursive: true, force: true });

  writeJSON(path.join(dir, "package.json"), {
    name,
    description: `The Orbits CLI for ${p.os === "darwin" ? "macOS" : "Linux"} on ${p.cpu}. Install ${NAME}, which picks this for you.`,
    ...COMMON,
    os: [p.os],
    cpu: [p.cpu],
    files: ["bin/orbits"],
    preferUnplugged: true,
  });
  fs.writeFileSync(
    path.join(dir, "README.md"),
    `# ${name}\n\nThe [Orbits CLI](https://docs.orbits.africa/cli) binary for ${p.os} ${p.cpu}. Don't install this directly: install [\`${NAME}\`](https://www.npmjs.com/package/${NAME}), which installs the one for your machine.\n`,
  );
  fs.copyFileSync(license, path.join(dir, "LICENSE"));
  folders.push(dir);
}

const main = path.join(out, "cli");
fs.mkdirSync(path.join(main, "bin"), { recursive: true });
fs.copyFileSync(path.join(here, "orbits.js"), path.join(main, "bin", "orbits.js"));
fs.chmodSync(path.join(main, "bin", "orbits.js"), 0o755);
fs.copyFileSync(path.join(here, "README.md"), path.join(main, "README.md"));
fs.copyFileSync(license, path.join(main, "LICENSE"));
writeJSON(path.join(main, "package.json"), {
  name: NAME,
  description: "The Orbits CLI: deploy a folder or a branch, read logs, and manage projects, variables, domains and more from your terminal.",
  ...COMMON,
  keywords: ["orbits", "cli", "deploy", "hosting"],
  bin: { orbits: "bin/orbits.js" },
  files: ["bin/orbits.js"],
  engines: { node: ">=18" },
  os: ["darwin", "linux"],
  optionalDependencies: Object.fromEntries(PLATFORMS.map((p) => [`${NAME}-${p.os}-${p.cpu}`, version])),
});
folders.push(main);

console.log(folders.join("\n"));
