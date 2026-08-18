# @driftmapper/cli-win32-arm64

Bundled `driftmapper` binary for Windows ARM64. Not meant to be installed
directly — it's an `optionalDependency` of
[`@driftmapper/cli`](https://www.npmjs.com/package/@driftmapper/cli),
which resolves and execs the binary in this package automatically for a
matching platform. See that package for usage.

Declares no `bin` or `exports` field on purpose — see `CLAUDE.md` in the
[source repo](https://github.com/DriftMapper/cli) for why.
