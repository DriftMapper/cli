# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this
repository.

## What this repo is

`driftmapper/cli` — the CLI that registers a build with Driftmapper and writes
`build-info.html` from the response. Its default action (no subcommand) is **write-only** and
picks its producer automatically (DRFT-124/DRFT-129, spec §5.2a's "smart enough" principle):

- **Verified** — inside a customer's CI: acquire a workload OIDC token, `POST /v1/builds`,
  where repository identity is confirmed against the token's own verified claims. When
  `$DRIFTMAPPER_CHALLENGE` is set (spec §4.5, DRFT-66) it first redeems that challenge —
  binding the repository to an org — before registering; still a write, not a read. See
  "Gotcha — challenge redemption is folded into the default action, not a separate command"
  below.
- **Declared** — a laptop with no CI at all, after `driftmapper login` (DRFT-30): read build
  identity straight from the local git checkout (`internal/gitcontext`), `POST
  /v1/orgs/{slug}/builds`, self-reported and org-attributed rather than token-confirmed. See
  "Gotcha — two producers, one output shape" below.

`compare` (spec DRFT-50, superseding DRFT-26) is a pure browser launcher for
the SPA compare view (DRFT-29, `driftmapper/static`), with zero network calls of
its own. See "Gotcha — `compare` is a browser launcher, not a read command" below.

**DRFT-124 (2026-08-26) removed `deploy` and `verify`** (deploy-marking/verification,
DRFT-88/96/97/98) — that domain was scope creep away from the actual product, which is the
build-info file itself, not tracking what's deployed where. `internal/buildinfo`'s `Parse` is
kept: it's the format's reference implementation and now core product, not verify's leftover.
`internal/sitefetch` (verify's fetcher) was deleted outright; see DRFT-131 for where its
functionality would resurface if a `probe <url>` command is ever built.

**DRFT-129 (2026-08-27) inverted spec §2.2a's "token-derived, never from the body" rule to
"confirm, don't derive"** and added the declared producer above — see `internal/gitcontext`'s
and `internal/deviceauth`'s package docs, and "Gotcha — two producers, one output shape" below.

Distributed via npm (`npx @driftmapper/cli`, or the unscoped `npx driftmapper` alias — DRFT-134)
with per-platform binaries resolved through npm's `os`/`cpu` fields as `optionalDependencies`,
plus raw binaries on GitHub Releases as a fallback for npm-blocked environments. `npm/wrapper` is
a pure launcher — it locates and execs the Go binary, and reimplements none of its logic.
`npm/alias` is a second pure launcher one level up: it reimplements none of the wrapper's logic
either, see "Gotcha — the unscoped alias reimplements nothing" below.

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
./bin/driftmapper login && ./bin/driftmapper   # DRFT-30/DRFT-129; declared producer, no CI at all — see internal/deviceauth

make check      # everything CI runs: go vet+test, npm unit tests, pack-and-install e2e
make cross       # cross-compile all six release targets + reproducibility check

cd npm/wrapper
node --test          # unit tests for the resolution logic
npm run test:e2e      # pack-and-install e2e — see the gotcha below on why this exists

cd npm/alias
npm run test:e2e      # pack-and-install e2e proving the alias behaves identically to the wrapper
```

## Architecture

```
cmd/driftmapper/main.go   entry point; dispatches to `compare` before the default
                          action's own flag.Parse() runs, so the subcommand never
                          collides with the default action's flags; version + name
                          stamped/declared here — name is the version-sentinel
                          contract with the npm launcher, see gotcha below
internal/
  config/                 DRIFTMAPPER_API_URL / DRIFTMAPPER_HUB_URL /
                          DRIFTMAPPER_OIDC_AUDIENCE / build-info path, all
                          zero-config-by-default (spec §5.2a). DashboardURL
                          (DRIFTMAPPER_DASHBOARD_URL, DRFT-36), Challenge
                          (DRIFTMAPPER_CHALLENGE, DRFT-66), and Org (DRIFTMAPPER_ORG,
                          DRFT-129) have no default — Challenge is also never a flag
                          (bearer secret); Org is required only for a declared
                          registration when the signed-in human belongs to >1 org
  oidcclient/             acquires (never verifies) a workload OIDC token — v1 is GitHub
                          Actions only (spec §4.3/§4.4); verification is exclusively
                          server-side (spec §5.2: "must never ship in a binary running on
                          customer infrastructure")
  gitcontext/              reads build identity directly from the local git checkout —
                           repository/ref/commit/author/committer (DRFT-129) — via `git`
                           subprocess calls, not a Go git library (stdlib-only posture).
                           The base layer both producers build on; see "Gotcha — two
                           producers, one output shape" below
  deviceauth/              `driftmapper login`/`logout` (DRFT-30): hub-brokered
                           device-code pairing, credential storage
                           (~/.config/driftmapper/credentials.json, mode 0600, no OS
                           keyring), and AccessToken's refresh-on-every-call — the
                           declared producer's entire auth story
  buildcontext/           normalizes a build's context into the CLI-submitted half of a
                          build registration (spec §2.2a, inverted by DRFT-129).
                          FromGit (gitcontext-backed) is the base layer both producers
                          share; FromGitHubActions overlays CI-only facts (trigger
                          event; GITHUB_SHA as the authoritative commit) on top.
                          repository_id/visibility/workflow/run_id/run_attempt remain
                          exclusively token-derived — the server rejects them if
                          present in the body
  buildinfo/               generates build-info.html (spec §2.3) as a pure function of one
                           server response — no server call. The one derived value is the
                           click-only sign-in link (DRFT-52): `/login?next=<resolution
                           path>` on resolution_url's own origin, not a server-provided
                           field. Parse reads the same format back — the format's
                           first-party reference parser, stdlib-only, and now core
                           product (DRFT-124), not a verification-feature byproduct;
                           producer and parser live in one package so they cannot drift
  apiclient/               client for the CLI's public-tier operations — POST /v1/builds,
                           POST /v1/orgs/{slug}/builds (DRFT-129), POST
                           /v1/repositories/authorize (DRFT-66), GET /v1/orgs
                           (dashboard-tier, used only to resolve DRIFTMAPPER_ORG at
                           `login` time) — sharing one {"data"}/{"error"}
                           envelope-unwrapping helper (doJSON/do), per
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
  alias/                  the unscoped `driftmapper` package (DRFT-134): bin/index.js is a
                          single `require('@driftmapper/cli/bin/index.js')`, nothing else —
                          see "Gotcha — the unscoped alias reimplements nothing" below
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

The same trap now also applies to `npm/wrapper/package.json` itself: `npm/alias/bin/index.js`
does `require('@driftmapper/cli/bin/index.js')`, an explicit subpath outside `main`. Adding
`exports` to the wrapper without listing that subpath would break the unscoped `driftmapper`
alias the same way it breaks the platform-package tier above.

## Gotcha — the unscoped alias reimplements nothing

`npm/alias` (published as `driftmapper`, DRFT-134) exists purely to squat the unscoped name so
`npx driftmapper` and `npx @driftmapper/cli` behave identically — see "Repo naming/npm
distribution" context in DRFT-134. Its entire `bin/index.js` is
`require('@driftmapper/cli/bin/index.js')`. This works, and stays correct with zero maintenance,
for two reasons:

- The real `bin/index.js`'s `main()` runs unconditionally at module load — it is not gated
  behind `require.main === module` — so `require()`-ing it from a different file executes the
  exact same `resolve()`/`spawn()` call, in-process, as running it directly.
- Every path `resolve.js` computes (`__dirname`-relative lookups, `require.resolve` of sibling
  platform packages) resolves relative to *where that file actually lives on disk* — inside
  `@driftmapper/cli`'s own `node_modules` entry — never relative to the alias's location or to
  `process.argv[1]`. `process.argv.slice(2)` is likewise unaffected by which file is `argv[1]`.

Do not "simplify" this into a `spawn('node', [...])` re-exec, and do not copy any of
`resolve.js`'s logic into the alias — either would be a second implementation of the launcher
that can silently drift from the first. If the wrapper's launcher ever needs a behavior change,
the alias picks it up for free by construction; a re-exec or copy would not.

The alias's dependency on `@driftmapper/cli` is pinned to an **exact** version, published only
after the wrapper is confirmed visible on the registry — see the next gotcha, which this extends
by one more package.

## Gotcha — the exec bit is not preserved automatically

`gh release download` and GitHub's artifact upload/download both drop the executable bit. Since
platform packages declare no `bin` field (see above), npm does not `chmod +x` anything at
install time either — the mode baked into the published tarball is all an end user ever gets.
`release.yml`'s `publish-npm` job `chmod +x`s the binary explicitly after download and asserts
`755` survived `npm pack` before publishing (`tar -tvf ... | grep -q -- '-rwx'`);
`npm/wrapper/test/e2e.sh` runs the same assertion locally, before anything is ever pushed to a
registry.

## Gotcha — publish platform packages first, idempotently, then wait, then the wrapper, then the alias

The wrapper pins all six platform deps to an **exact** version, not a caret range, and the alias
pins its `@driftmapper/cli` dependency the same way — all eight packages release from one git tag
in lockstep, so the version *is* the contract. A caret would let wrapper `v1.2.3` execute binary
`v1.9.0` (or alias `v1.2.3` execute wrapper `v1.9.0`) with a different, possibly incompatible
command surface, and one bad patch would retroactively poison every previously published version
downstream of it with no way to roll back.

Given that, and given the "missing optionalDependency fails silently" gotcha above, `release.yml`
publishes in a specific order: all six platform packages first — each checked against `npm view`
first, so re-running the same tag after a partial failure doesn't error out on packages already
published — then polls `npm view` until all six are actually visible (registry propagation is
not instant), then stamps and publishes the wrapper, waits for *it* to become visible the same
way, and only then stamps and publishes the alias. Publishing either downstream package into the
gap before its dependency is visible would produce an install that resolves clean with no working
binary for anyone who happens to install in that window.

## Gotcha — `NPM_PUBLISH` gates the whole publish job

A trusted publisher can't be configured on npmjs.com for a package that doesn't exist yet, so
without a gate, the very first tag push would red-X on `publish-npm`. The job is
`if: vars.NPM_PUBLISH == 'true'` — set that repo/environment variable to `true` only once all
eight packages — the seven `@driftmapper/cli*` packages plus the unscoped `driftmapper` alias —
have trusted publishers configured for this repo+workflow on npmjs.com. Reserving the unscoped
name itself (a first, manual, token-authenticated `npm publish` from a maintainer's machine,
since there's nothing yet for a trusted publisher to attach to) is a prerequisite this gate
can't do for you — same bootstrap chicken-and-egg the original seven packages went through.

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

## Gotcha — two producers, one output shape

`run` (`cmd/driftmapper/main.go`) picks between `runVerified` and `runDeclared` on exactly
one signal: whether `ACTIONS_ID_TOKEN_REQUEST_URL` is set. That env var is only ever present
inside a job that requested `permissions: id-token: write`, so its presence is a reliable
"we're in CI" fact, not a heuristic — this is spec §5.2a's "the CLI should be smart enough to
tell it's in a git repo" principle applied to producer selection specifically, settled during
DRFT-124's re-scope.

Both producers end at the same call: `writeBuildInfo` takes whatever `protocol.Build` either
one got back and runs it through `buildinfo.Generate` unchanged — attribution (`verified` vs.
`declared`) is a field on that response, not a branch in this repo's own logic. Don't
special-case declared builds in `buildinfo`/`internal/buildinfo` to "look different" — the
whole point of DRFT-129 is that both producers are the same product, confirmed one way or
self-reported the other.

`runDeclared` has no equivalent to `maybeAuthorize`/`DRIFTMAPPER_CHALLENGE` — there's nothing
to bind. A declared registration's authorization *is* org membership, established once at
`driftmapper login` time and re-verified server-side (`OrgScoped`) on every call; `resolveOrg`
picks which org from `DRIFTMAPPER_ORG` or, failing that, the signed-in human's sole org — see
its own doc comment for why an ambiguous multi-org case is always an error, never a default.

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

## Principle — the build-info file is the product, not a byproduct

DRFT-124 cut deploy-marking and verification (formerly `deploy`/`verify`, DRFT-88/96/97/98)
as scope creep: DriftMapper's one opinionated thing is `build-info.html` — what it is, how
it's structured, how it points back to the backend. Where it's hosted, how it's kept fresh,
and what's done with it are the customer's problem. Don't reintroduce a "what's currently
true about a deployment" ledger, environment-keyed state, or a second API read to support one
without a fresh product decision — this repo went down that path once already and backed out
of it. A read-only `probe <url>` command (fetch + parse a deployed build-info file, print it)
remains a plausible, much smaller future addition — see DRFT-131 — but it records nothing
server-side.
