'use strict';

const fs = require('fs');
const path = require('path');
const { execFileSync } = require('child_process');

const NAME = 'driftmapper';
const SCOPE = '@driftmapper';
const PKG_BASE = 'cli';

function platformKey(platform = process.platform, arch = process.arch) {
  const archName = arch === 'x64' || arch === 'arm64' ? arch : null;
  const platformName =
    platform === 'darwin' || platform === 'linux' || platform === 'win32' ? platform : null;
  if (!platformName || !archName) return null;
  return `${platformName}-${archName}`;
}

function binName(platform = process.platform) {
  return platform === 'win32' ? `${NAME}.exe` : NAME;
}

function packageName(key) {
  return `${SCOPE}/${PKG_BASE}-${key}`;
}

// Tier 1: explicit override. Trusted by construction — an operator set this
// deliberately, so unlike the PATH tier below it is used as-is, with no
// identity check.
function fromEnvOverride() {
  const override = process.env.DRIFTMAPPER_BINARY_PATH;
  if (!override) return null;
  if (!path.isAbsolute(override)) {
    throw new Error(`DRIFTMAPPER_BINARY_PATH must be an absolute path, got: ${override}`);
  }
  if (override.split(/[\\/]+/).includes('..')) {
    throw new Error(`DRIFTMAPPER_BINARY_PATH must not contain '..' segments, got: ${override}`);
  }
  if (!fs.existsSync(override)) {
    throw new Error(`DRIFTMAPPER_BINARY_PATH points to '${override}' but the file does not exist.`);
  }
  return override;
}

// Tier 2: our own scoped platform package, installed as an
// optionalDependency. Trusted by construction — it is our own published
// artifact, located through Node's own module resolution. Resolves
// package.json, never the package root, and never touches "main" or
// "exports" — those fields are cosmetic/absent on platform packages by
// design (see npm/platforms/*/package.json).
function fromBundledPackage(key) {
  const pkg = packageName(key);
  try {
    const pkgJsonPath = require.resolve(`${pkg}/package.json`);
    const dir = path.dirname(pkgJsonPath);
    const binPath = path.join(dir, 'bin', binName());
    return fs.existsSync(binPath) ? binPath : null;
  } catch {
    return null;
  }
}

// Tier 3: PATH. The only ambient, attacker-influenceable source in this
// chain — see identifiesAsDriftmapper() below, which is why it's the only
// tier that gets verified. Deliberately does not delegate to the OS/shell to
// find the binary: on Windows, CreateProcess searches the current working
// directory before PATH, so a same-named binary dropped in cwd would
// silently win over the real one. We scan PATH ourselves, excluding cwd,
// and only ever spawn an absolute path.
function fromPath() {
  const pathEnv = process.env.PATH || process.env.Path || '';
  const dirs = pathEnv.split(path.delimiter).filter(Boolean);
  const cwd = process.cwd();
  const candidates = [];
  for (const dir of dirs) {
    const resolved = path.resolve(dir);
    if (resolved === cwd) continue; // exclude cwd — see comment above
    const candidate = path.join(resolved, binName());
    if (fs.existsSync(candidate)) candidates.push(candidate);
  }
  return candidates;
}

// Runs `<candidate> --version --json` and requires it to identify as this
// CLI (name === "driftmapper"). This sentinel is a permanent compatibility
// contract — see cmd/driftmapper/main.go's `name` constant on the Go side.
// Anything other than an exact match (nonzero exit, unparseable output,
// wrong/missing name, timeout) means "not our binary," and the candidate is
// rejected, never executed.
function identifiesAsDriftmapper(candidate) {
  try {
    const output = execFileSync(candidate, ['--version', '--json'], {
      timeout: 2000,
      encoding: 'utf8',
      windowsHide: true,
    });
    const parsed = JSON.parse(output);
    return Boolean(parsed) && parsed.name === NAME;
  } catch {
    return false;
  }
}

function buildErrorMessage(key, rejectedPathCandidates) {
  const lines = [];
  lines.push(`${NAME}: no binary available for ${process.platform}/${process.arch}.`);
  lines.push('');
  if (key) {
    const pkg = packageName(key);
    lines.push(`The platform package ${pkg} is not installed.`);
    lines.push('npm installs these as optional dependencies and skips them silently on failure.');
    lines.push('');
    lines.push(`  npm install ${pkg}`);
    lines.push('  npm install --include=optional        # if optional deps were omitted');
  } else {
    lines.push(`${process.platform}/${process.arch} has no published platform package.`);
  }
  for (const candidate of rejectedPathCandidates) {
    lines.push('');
    lines.push(`Ignored ${candidate} on PATH: it did not identify as this CLI.`);
  }
  lines.push('');
  lines.push('Set DRIFTMAPPER_BINARY_PATH to an absolute path to override.');
  return lines.join('\n');
}

/**
 * Resolve the binary to execute, in order:
 *   1. DRIFTMAPPER_BINARY_PATH (explicit override)
 *   2. The bundled platform package for this OS/arch
 *   3. A PATH candidate that identifies itself as this CLI
 * Throws with a detailed, actionable message if nothing resolves.
 */
function resolve() {
  const override = fromEnvOverride();
  if (override) return override;

  const key = platformKey();
  if (key) {
    const bundled = fromBundledPackage(key);
    if (bundled) return bundled;
  }

  const rejected = [];
  for (const candidate of fromPath()) {
    if (identifiesAsDriftmapper(candidate)) return candidate;
    rejected.push(candidate);
  }

  throw new Error(buildErrorMessage(key, rejected));
}

module.exports = {
  resolve,
  platformKey,
  binName,
  packageName,
  identifiesAsDriftmapper,
  buildErrorMessage,
  NAME,
  SCOPE,
  PKG_BASE,
};
