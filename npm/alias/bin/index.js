#!/usr/bin/env node
'use strict';

// This package exists only so `npx driftmapper` / `npm install driftmapper`
// behave identically to @driftmapper/cli — see CLAUDE.md's "the unscoped
// alias reimplements nothing" gotcha. Requiring the real launcher runs its
// resolve()/spawn() logic in-process, unmodified: process.argv is
// untouched (only argv[0]/[1] are ever skipped, regardless of which file
// argv[1] names), and every path it resolves is relative to its own
// installed location, never this file's.
require('@driftmapper/cli/bin/index.js');
