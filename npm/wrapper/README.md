# @driftmapper/cli

The Driftmapper CLI, distributed as a thin npm launcher plus one
`optionalDependency` package per platform (`@driftmapper/cli-<os>-<arch>`).
This package contains no logic of its own — it locates and execs the real
Go binary. See the top-level [README](../../README.md) for what the CLI
does and [CLAUDE.md](../../CLAUDE.md) for how this packaging works.

## Install

```bash
npx @driftmapper/cli --version
# or
npm install --save-dev @driftmapper/cli
```

No postinstall script runs. `npm install --ignore-scripts` works unmodified,
since nothing needs to execute to fetch a binary — it's an ordinary
`optionalDependency` resolved by npm itself.

## Resolution order

1. `DRIFTMAPPER_BINARY_PATH` — an absolute path to a binary to run instead.
2. The `@driftmapper/cli-<os>-<arch>` package matching your platform,
   installed automatically as an `optionalDependency`.
3. A `driftmapper` binary on `PATH`, but only if it identifies itself as
   this CLI (`driftmapper --version --json` reporting `"name":"driftmapper"`).
   An unrelated same-named binary on `PATH` is named and rejected, never run.

If your platform's optional dependency failed to install (npm skips a 404'd
or `--omit=optional`-excluded optional dependency silently, not as an
error), download a raw binary from this repo's
[GitHub Releases](https://github.com/DriftMapper/cli/releases) instead and
point `DRIFTMAPPER_BINARY_PATH` at it.

## License

Apache-2.0
