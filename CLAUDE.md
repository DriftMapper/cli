# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## What this repo is

`driftmapper/cli` — the CLI that runs inside a customer's CI to register a build with
Driftmapper (spec §5.2a). Its default action (no subcommand) is **write-only**: acquire a
workload OIDC token, `POST /v1/builds`, write `build-info.html` from the response. When
`$DRIFTMAPPER_CHALLENGE` is set (spec §4.5, DRFT-66) it first redeems that challenge — binding
the repository to an org — before registering; still a write, not a read. See "Gotcha —
challenge redemption is folded into the default action, not a separate command" below.

`compare` (spec DRFT-50, superseding DRFT-26) is a pure browser launcher for the
SPA compare view (DRFT-29, `driftmapper/static`), with zero network calls of
its own. See "Gotcha — `compare` is a browser launcher, not a read command" below.
`deploy` (DRFT-88) and `verify` (DRFT-96) are the two additional write
subcommands — CI steps that record a deploy-mark or a verify assertion on
the same OIDC identity, dispatched the same way `compare` is. All three stay
writes (or no-op hand-offs); nothing here reads from the API.

Distributed via npm (`npx @driftmapper/cli`) with per-platform binaries resolved through npm's
`os`/`cpu` fields as `optionalDependencies`, plus raw binaries on GitHub Releases as a fallback
for npm-blocked environments. `npm/wrapper` is a pure launcher — it locates and execs the Go
binary, and reimplements none of its logic.

**Public by design, not by accident** (DRFT-19): this binary executes inside customer CI, so
auditability — anyone can read the source that runs in their pipeline, and reproduce the exact
binary they installed — is the strongest form of the supply-chain posture. Everything in this
file follows from that: no postinstall script, provenance attestation, reproducible builds, and
a public dependency graph (see "The protocol dependency is public for a reason" below).

Reference implementation this packaging was modeled on: `SpandrelSystems/cli` (private template
repo, same org) — a generic Go-binary-via-npm scaffold. Every gotcha below traces back to a
mistake that template already made and fixed once; this file exists so this repo doesn't make
them again. (DRFT-19 itself was implemented once before, lost because the branch was never
pushed, and rebuilt from scratch — see the git history and Linear for the full story. If you're
reading this while rebuilding it a *third* time: check `git push` actually happened.)

## Commands

```bash
go vet ./... && go test ./...
go build -o bin/driftmapper ./cmd/driftmapper
DRIFTMAPPER_DASHBOARD_URL=https://... ./bin/driftmapper compare <build-id-a> <build-id-b>   # DRFT-50; no default dashboard origin, see internal/config
DRIFTMAPPER_CHALLENGE=... ./bin/driftmapper   # DRFT-66; redeems before registering, see internal/config.Challenge

make check      # everything CI runs: go vet+test, npm unit tests, pack-and-install e2e
make cross       # cross-compile all six release targets + reproducibility check

cd npm/wrapper
node --test          # unit tests for the resolution logic
npm run test:e2e      # pack-and-install e2e — see the gotcha below on why this exists
```

## Architecture

```
cmd/driftmapper/main.go   entry point; dispatches to `compare`, `deploy`, and
                          `verify` before the default action's own flag.Parse()
                          runs, so the subcommands never collide with the
                          default action's flags; version + name stamped/
                          declared here — name is the version-sentinel
                          contract with the npm launcher, see gotcha below
internal/
  config/                 DRIFTMAPPER_API_URL / DRIFTMAPPER_OIDC_AUDIENCE / build-info
                          path, all zero-config-by-default (spec §5.2a). DashboardURL
                          (DRIFTMAPPER_DASHBOARD_URL, DRFT-36) and Challenge
                          (DRIFTMAPPER_CHALLENGE, DRFT-66) are the two exceptions — no
                          default for either; Challenge is also never a flag (bearer secret)
  oidcclient/             acquires (never verifies) a workload OIDC token — v1 is GitHub
                          Actions only (spec §4.3/§4.4); verification is exclusively
                          server-side (spec §5.2: "must never ship in a binary running on
                          customer infrastructure")
  buildcontext/           normalizes GitHub Actions' env into the CLI-submitted half of a
                          build registration (spec §2.2a) — repo identity/ref/workflow/run
                          are deliberately NOT here; they're token-derived and the server
                          rejects them if present in the body
  buildinfo/               generates build-info.html (spec §2.3) as a pure function of one
                          server response — no server call. The one derived value is the
                          click-only sign-in link (DRFT-52): `/login?next=<resolution
                          path>` on resolution_url's own origin, not a server-provided
                          field. No read side as of DRFT-50 — see the compare/ entry below
  apiclient/               client for the CLI's public-tier operations — POST /v1/builds
                          and POST /v1/repositories/authorize (DRFT-66) — sharing one
                          {"data"}/{"error"} envelope-unwrapping helper (doJSON), per
                          driftmapper/protocol's openapi.yaml
  compare/                 `driftmapper compare`'s URL-building logic (spec DRFT-50) —
                          OpenURL builds the SPA compare view URL from two build-instance
                          IDs the caller already supplied, per that view's own URL
                          contract. No apiclient, no HTTP fetch, no parsing of anything
  browser/                 cross-platform "open a URL in the default browser" launcher used
                          by `compare` (DRFT-36/DRFT-50) — open/xdg-open/cmd dispatch on
                          GOOS, split so the dispatch itself is unit-testable
npm/
  wrapper/                the package users install: bin/index.js (launcher shim),
                          lib/resolve.js (resolution chain + PATH-fallback guard), test/
  platforms/cli-<os>-<arch>/   one npm package per target, each just os/cpu/main/files —
                          the binary is copied into bin/ at release time, never committed
```

## Gotcha — the version sentinel is a permanent compatibility contract

`cmd/driftmapper/main.go`'s `name` constant (`"driftmapper"`) is what `driftmapper --version
--json` emits, and it's the only thing `npm/wrapper/lib/resolve.js`'s PATH-fallback tier has to
decide whether a binary found on `PATH` is actually this CLI. If this constant is ever renamed
or the `--version --json` output shape changes incompatibly, a newer npm wrapper stops
recognizing an older binary it finds on `PATH`, and silently falls through to the "no binary
available" error instead. Change it only in lockstep with `resolve.js`'s `NAME` constant, and
treat a change here as a breaking release.

## Gotcha — a missing optionalDependency fails silently, not loudly

npm does not fail an install when an `optionalDependency` 404s, or is omitted via
`--omit=optional`, or hits the npm optional-dep lockfile bug class (npm/cli#4828, the
esbuild/rollup one). It warns and continues. That means a user who ends up without the matching
`@driftmapper/cli-<plat>-<arch>` package installed sees nothing wrong at install time — the
wrapper just quietly has no binary, and `resolve.js` falls through to the `PATH` tier. This is
why that tier exists at all (rather than a bare "not installed" error) and why it's the only
tier that gets verified: see the next gotcha. It's also why `release.yml`'s `publish-npm` job is
written to publish idempotently and wait for the platform packages to actually resolve before
publishing the wrapper — see the gotcha after that.

## Gotcha — the PATH fallback must verify identity, or it becomes a supply-chain hole

Given the above, `resolve.js` cannot simply exec whatever it finds named `driftmapper` on
`PATH` — that would mean any unrelated same-named binary silently runs in place of this one
whenever the real platform package failed to install. The fix: `identifiesAsDriftmapper()` runs
`<candidate> --version --json` and requires the output to parse with `name === "driftmapper"`
before ever executing it for real. A rejected candidate is *named* in the final error
(`"Ignored ... on PATH: it did not identify as this CLI"`), not silently skipped, because "but
`driftmapper` IS on my PATH" is exactly the confusion that would otherwise cause. Only the
`PATH` tier is checked this way — the `DRIFTMAPPER_BINARY_PATH` override and the bundled
platform package are both trusted by construction (an explicit operator choice, and our own
published artifact, respectively).

Also load-bearing in `resolve.js`'s `fromPath()`: it never spawns a bare command name and never
delegates to the OS/shell to find one. On Windows, `CreateProcess` searches the current working
directory *before* `PATH`, so a same-named binary dropped in `cwd` would silently win over the
real one on `PATH`. `fromPath()` scans `PATH` itself, excludes `cwd`, and only ever passes an
absolute path to `execFileSync`.

## Gotcha — platform packages must declare neither `bin` nor `exports`

Two things that look like reasonable additions to `npm/platforms/*/package.json` and are not:

- **No `bin` field.** Six platform packages declaring the same bin name (`driftmapper`) would
  race to symlink `node_modules/.bin/driftmapper`, colliding with the wrapper's own `bin` entry.
  Only the wrapper declares `bin`.
- **No `exports` field.** `resolve.js`'s bundled-package tier calls
  `require.resolve('<pkg>/package.json')` — an explicit subpath. Adding `exports` (even one that
  looks like it should permit this) blocks any subpath not explicitly listed and makes that call
  throw `ERR_PACKAGE_PATH_NOT_EXPORTED`, silently degrading every user of that platform to the
  `PATH` fallback.

## Gotcha — the exec bit is not preserved automatically

`gh release download` and GitHub's artifact upload/download both drop the executable bit. Since
platform packages declare no `bin` field (see above), npm does not `chmod +x` anything at
install time either — the mode baked into the published tarball is all an end user ever gets.
`release.yml`'s `publish-npm` job `chmod +x`s the binary explicitly after download and asserts
`755` survived `npm pack` before publishing (`tar -tvf ... | grep -q -- '-rwx'`);
`npm/wrapper/test/e2e.sh` runs the same assertion locally, before anything is ever pushed to a
registry.

## Gotcha — publish platform packages first, idempotently, then wait, then the wrapper

The wrapper pins all six platform deps to an **exact** version, not a caret range — all seven
packages release from one git tag in lockstep, so the version *is* the contract. A caret would
let wrapper `v1.2.3` execute binary `v1.9.0` with a different (possibly incompatible) command
surface, and one bad platform-package patch would retroactively poison every previously
published wrapper version with no way to roll back.

Given that, and given the "missing optionalDependency fails silently" gotcha above, `release.yml`
publishes in a specific order: all six platform packages first — each checked against `npm view`
first, so re-running the same tag after a partial failure doesn't error out on packages already
published — then polls `npm view` until all six are actually visible (registry propagation is
not instant), and only then stamps and publishes the wrapper. Publishing the wrapper into the
gap before the platform packages are visible would produce a wrapper that installs clean with no
binary for anyone who happens to install in that window.

## Gotcha — `NPM_PUBLISH` gates the whole publish job

A trusted publisher can't be configured on npmjs.com for a package that doesn't exist yet, so
without a gate, the very first tag push would red-X on `publish-npm`. The job is
`if: vars.NPM_PUBLISH == 'true'` — set that repo/environment variable to `true` only once all
seven `@driftmapper/cli*` packages have trusted publishers configured for this repo+workflow on
npmjs.com.

## Gotcha — `compare` is a browser launcher, not a read command, and why

DRFT-26's original design fetched `build-info.html` unauthenticated from two deployed targets
and parsed its `driftmapper:*` meta tags client-side to diff `build_instance_id` locally. That
was a read command in everything but name, and it directly contradicted this CLI's founding
rule (spec DRFT-21: "No read commands. Viewing a build happens by opening the build-info file
— that's the core loop"). It was also the only remaining consumer forcing the resolution page
(`/r/<id>`) to keep an unauthenticated rendering branch alive "for the CLI" — DRFT-51 later
settled that branch stays anyway (for Slack link unfurls), independent of anything here.

DRFT-50 stripped all of that: `internal/compare` performs zero HTTP fetches and parses
nothing. `driftmapper compare <build-id-a> <build-id-b>` only builds the SPA compare view URL
(`/compare?a=<id>&b=<id>[&a_url=…&b_url=…]` on `DRIFTMAPPER_DASHBOARD_URL`, DRFT-29's
contract) and opens it — the browser does the read, with whatever WorkOS session it carries.
The two build-instance IDs are the caller's responsibility to already have, read directly off
each target's `build-info.html` (DRFT-52 bakes both `build_instance_id` and `built_at` in as
visible page content precisely so this is a copy-paste, not a curl). There is deliberately no
`-open` flag (opening the browser is the only mode) and no exit-code-based diff result (the
CLI itself no longer knows whether the builds match — only the browser does).

## Gotcha — challenge redemption is folded into the default action, not a separate command

DRFT-66's decision record. `driftmapper authorize --challenge=...` as a standalone command
was the alternative and was rejected: it costs the user a two-stage onboarding (add a step,
run it, edit the workflow again to remove it), and a command whose only correct lifecycle is
"run once, then delete" is an odd thing to document and support — every extra edit is a
drop-off point in the self-serve funnel DRFT-59 restored. Folding into the default action via
`$DRIFTMAPPER_CHALLENGE` (`maybeAuthorize` in `cmd/driftmapper/main.go`, called before
`RegisterBuild`) means one CI snippet, added once, never edited.

**`maybeAuthorize` fails loud on every redemption error, on purpose — and relies on the
server making replay safe, not on guessing at the response.** `RedeemChallenge`
(`driftmapper/server`'s `internal/store/challenge.go`) deliberately collapses "never
existed", "expired", "revoked", and "over attempt cap" into one `invalid_challenge` error
(spec §4.5's anti-enumeration rule) — a first-ever broken challenge must fail loud here,
matching DRFT-66's acceptance criteria, rather than silently degrading into an unbound,
rate-limited build that mysteriously fails weeks later. That's also why this CLI never
special-cases `invalid_challenge` to swallow it: from the response alone, this code cannot
tell "genuinely broken" apart from "already redeemed by me, harmless." What makes fail-loud
safe for the harmless case too is server-side, not client-side: DRFT-74 made `RedeemChallenge`
idempotent for the exact-same-repo replay (an already-redeemed challenge presented again by
the repository it originally bound succeeds again, rather than erroring), so a
`DRIFTMAPPER_CHALLENGE` secret left in place after binding doesn't actually break later
runs — this CLI just doesn't (and shouldn't) know that from here; it relies on the server
having made it true.

## The `protocol` dependency is public for a reason

`go.mod` requires `github.com/driftmapper/protocol` — the wire contract, N-2 semver'd, shared
with the server. It was made public specifically for DRFT-19: a public `cli` that depends on a
private module would be un-auditable and un-buildable by anyone outside the org, which defeats
this whole repo's reason for existing. Do not add a dependency on a private module here without
solving this same problem for it first.

## No postinstall script, anywhere

This is a hard requirement, not a style preference, for two independent reasons: it's a security
review red flag for enterprise buyers evaluating whether to run this in their CI, and it's the
live attack vector for Shai-Hulud-style npm supply-chain worms. Consequence, and the reason it's
safe to promise: `ignore-scripts=true` environments work completely unmodified, since nothing
ever needs to execute to fetch a binary — the platform binary arrives as an ordinary
`optionalDependency` tarball, same as any other npm package.
