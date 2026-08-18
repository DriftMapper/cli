'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const {
  platformKey,
  binName,
  packageName,
  identifiesAsDriftmapper,
  buildErrorMessage,
} = require('../lib/resolve');

test('platformKey maps supported platform/arch pairs', () => {
  assert.equal(platformKey('darwin', 'arm64'), 'darwin-arm64');
  assert.equal(platformKey('darwin', 'x64'), 'darwin-x64');
  assert.equal(platformKey('linux', 'x64'), 'linux-x64');
  assert.equal(platformKey('linux', 'arm64'), 'linux-arm64');
  assert.equal(platformKey('win32', 'x64'), 'win32-x64');
  assert.equal(platformKey('win32', 'arm64'), 'win32-arm64');
});

test('platformKey returns null for unsupported platform or arch', () => {
  assert.equal(platformKey('freebsd', 'x64'), null);
  assert.equal(platformKey('darwin', 'ia32'), null);
  assert.equal(platformKey('sunos', 'arm'), null);
});

test('binName appends .exe only on win32', () => {
  assert.equal(binName('darwin'), 'driftmapper');
  assert.equal(binName('linux'), 'driftmapper');
  assert.equal(binName('win32'), 'driftmapper.exe');
});

test('packageName is scoped and matches the platform directory naming', () => {
  assert.equal(packageName('darwin-arm64'), '@driftmapper/cli-darwin-arm64');
  assert.equal(packageName('win32-x64'), '@driftmapper/cli-win32-x64');
});

test('buildErrorMessage names the exact missing package and the env override', () => {
  const msg = buildErrorMessage('darwin-arm64', []);
  assert.match(msg, /no binary available for/);
  assert.match(msg, /@driftmapper\/cli-darwin-arm64/);
  assert.match(msg, /npm install @driftmapper\/cli-darwin-arm64/);
  assert.match(msg, /--include=optional/);
  assert.match(msg, /DRIFTMAPPER_BINARY_PATH/);
  assert.doesNotMatch(msg, /Ignored/);
});

test('buildErrorMessage names each rejected PATH candidate', () => {
  const msg = buildErrorMessage('linux-x64', ['/usr/local/bin/driftmapper', '/opt/bin/driftmapper']);
  assert.match(msg, /Ignored \/usr\/local\/bin\/driftmapper on PATH/);
  assert.match(msg, /Ignored \/opt\/bin\/driftmapper on PATH/);
});

test('buildErrorMessage handles a platform with no published package', () => {
  const msg = buildErrorMessage(null, []);
  assert.match(msg, /has no published platform package/);
  assert.doesNotMatch(msg, /npm install/);
});

test('identifiesAsDriftmapper accepts a script that emits the correct sentinel', (t) => {
  const script = makeFakeBinary(t, `console.log(JSON.stringify({ name: 'driftmapper', version: '9.9.9' }));`);
  assert.equal(identifiesAsDriftmapper(script), true);
});

test('identifiesAsDriftmapper rejects a decoy with the wrong name', (t) => {
  const script = makeFakeBinary(t, `console.log(JSON.stringify({ name: 'not-driftmapper', version: '1.0.0' }));`);
  assert.equal(identifiesAsDriftmapper(script), false);
});

test('identifiesAsDriftmapper rejects non-JSON output', (t) => {
  const script = makeFakeBinary(t, `console.log('hello');`);
  assert.equal(identifiesAsDriftmapper(script), false);
});

test('identifiesAsDriftmapper rejects a script that exits nonzero', (t) => {
  const script = makeFakeBinary(t, `process.exit(1);`);
  assert.equal(identifiesAsDriftmapper(script), false);
});

test('identifiesAsDriftmapper rejects a missing file', () => {
  assert.equal(identifiesAsDriftmapper('/nonexistent/path/to/driftmapper'), false);
});

// Writes a small executable node script as a fake "binary" this test can
// pass to identifiesAsDriftmapper(), which invokes it directly via
// execFileSync (no shell) — relies on the kernel honoring the shebang line,
// so this only works on POSIX. That's fine: this suite runs in CI on Linux,
// matching the house convention of testing the shim on one platform and
// relying on the cross-compiled build matrix (proven separately) for the
// rest. Cleaned up automatically via t.after.
function makeFakeBinary(t, body) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'driftmapper-resolve-test-'));
  const file = path.join(dir, 'fake-driftmapper.js');
  fs.writeFileSync(file, `#!/usr/bin/env node\n${body}\n`, { mode: 0o755 });
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return file;
}
