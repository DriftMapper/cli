# driftmapper

The unscoped alias for [`@driftmapper/cli`](../wrapper/README.md). This
package contains no logic of its own — `bin/index.js` requires the real
package's launcher directly, so behavior, resolution order, and error
messages are identical either way. See the top-level
[README](../../README.md) for what the CLI does and
[CLAUDE.md](../../CLAUDE.md) for how this packaging works.

## Install

```bash
npx driftmapper --version
# or
npm install --save-dev driftmapper
```

Equivalent to installing `@driftmapper/cli` directly — this package just
depends on it. No postinstall script runs either way.

## License

Apache-2.0
