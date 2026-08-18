#!/usr/bin/env node
'use strict';

const { spawn } = require('child_process');
const os = require('os');
const { resolve } = require('../lib/resolve');

// Never crash on a closed stdout (e.g. `driftmapper --version | head`) —
// write the EPIPE away and let the child's own SIGPIPE handling take it
// from there.
process.stdout.on('error', (err) => {
  if (err.code !== 'EPIPE') throw err;
});

function main() {
  let bin;
  try {
    bin = resolve();
  } catch (err) {
    process.stderr.write(`${err.message}\n`);
    process.exitCode = 1;
    return;
  }

  // stdio: 'inherit' is load-bearing, not a convenience default: it hands
  // the child real file descriptors, giving `driftmapper ... | head` real
  // pipe semantics instead of buffered output.
  const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit' });

  // On Windows the console already delivers CTRL_C_EVENT to the whole
  // process group, so forwarding here would be redundant at best; calling
  // child.kill() maps to TerminateProcess() and would force-kill the child
  // before it can run its own cleanup handlers. So: no-op on win32.
  const forward = (signal) => {
    if (process.platform === 'win32') return;
    child.kill(signal);
  };
  process.on('SIGINT', () => forward('SIGINT'));
  process.on('SIGTERM', () => forward('SIGTERM'));

  child.on('error', (err) => {
    if (err.code === 'ENOENT') {
      process.stderr.write(`${bin}: no such file or not executable.\n`);
    } else {
      process.stderr.write(`${err.message}\n`);
    }
    process.exitCode = 1;
  });

  child.on('close', (code, signal) => {
    if (signal) {
      // Look the number up — never hardcode it, it differs per platform.
      process.exitCode = 128 + (os.constants.signals[signal] || 0);
      return;
    }
    process.exitCode = code === null ? 1 : code;
  });
}

main();
