# driftmapper/cli

The Driftmapper CLI: registers a build from inside your CI so Driftmapper can track it.
The default action is write-only — it acquires a workload OIDC token, calls `POST
/v1/builds`, and writes `build-info.html`. Three other subcommands: `deploy` (below) records
that a registered build is now deployed to an environment; `verify` (below) checks what's
*currently deployed* to an environment against reality — it fetches the deployed
`build-info.html`, records what it found, and exits 3 on a mismatch; and `compare` (below) is
a browser launcher with zero network calls of its own, not a read command. All three use the
same workload OIDC token.

## Install and use

```bash
npx @driftmapper/cli
```

or install it as a dev dependency / in your CI image:

```bash
npm install --save-dev @driftmapper/cli
```

No install step required, no postinstall script runs, and `npm install --ignore-scripts` works
unmodified — the platform binary for your OS/architecture arrives as an ordinary
`optionalDependency`. See [`npm/wrapper/README.md`](npm/wrapper/README.md) for how resolution
works and what to do if your platform's optional dependency didn't install.

### GitHub Actions

```yaml
permissions:
  id-token: write   # required — this is how the CLI gets its workload OIDC token
steps:
  - run: npx @driftmapper/cli
```

### Authorizing a new repository

A repository must be bound to a Driftmapper org before its builds are attributed to
anyone — issue a challenge from the dashboard, add it as a `DRIFTMAPPER_CHALLENGE` repo
secret, and reference it once:

```yaml
steps:
  - run: npx @driftmapper/cli
    env:
      DRIFTMAPPER_CHALLENGE: ${{ secrets.DRIFTMAPPER_CHALLENGE }}
```

No separate command: `driftmapper` redeems the challenge before registering whenever the
env var is set, and does nothing extra when it's absent. Leaving the secret in place after
the first successful run is harmless — redeeming an already-consumed challenge from the
same repository it originally bound succeeds again rather than erroring — but there's no
reason to keep it around either, so `driftmapper` prints a reminder you can act on when it
redeems. Running without a repository binding at all works fine too (spec §2.2a) —
`DRIFTMAPPER_CHALLENGE` only needs to be set for the run that does the binding, whenever
that happens to be.

### Comparing two builds

```bash
DRIFTMAPPER_DASHBOARD_URL=https://app.driftmapper.com \
  driftmapper compare <build-instance-id-a> <build-instance-id-b>
```

Opens the SPA compare view in your browser for an authenticated, session-aware,
field-by-field diff — read the two build-instance IDs off each target's `build-info.html`
first (they're shown directly on the page, no request required). `-a-url`/`-b-url` optionally
label each side in that view (e.g. with the deployed URL they came from). If a browser can't
be launched (headless/SSH), the URL is printed to stdout instead so it's still usable.

`compare` makes no network calls and requires `DRIFTMAPPER_DASHBOARD_URL` — there's no default
dashboard origin yet.

### Marking a deploy

```yaml
permissions:
  id-token: write   # required — same OIDC token as registration
steps:
  - run: npx @driftmapper/cli deploy -env=production
```

`-commit` defaults to `$GITHUB_SHA`, so the common case needs nothing else — the server
resolves whichever registered build was produced from that commit. Set `-commit` explicitly
when the deploy workflow's own `$GITHUB_SHA` isn't the commit that was actually built — a
tag- or manually-dispatched deploy workflow is the usual case where that happens.

This matters most for a Docker/image-based deploy, where the build and deploy steps are
often two separate workflow runs and the build's opaque build-instance ID has no way to
reach the deploy job (it lives inside `build-info.html`, which is either sealed in a layer
the deploy job never unpacks or was written by a workflow run this one has no access to) —
but every deploy topology already knows the commit it's shipping:

```yaml
# .github/workflows/deploy.yml — a separate workflow from the one that builds the image
on:
  workflow_dispatch:
    inputs:
      image_tag:
        required: true
permissions:
  id-token: write
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - run: kubectl set image deployment/web web=myregistry/web:${{ inputs.image_tag }}
      - run: npx @driftmapper/cli deploy -env=production -commit=${{ inputs.image_tag }}
```

(Here `image_tag` is assumed to be the commit SHA the image was built from — adjust to
however your build tags images if that's not the case.)

Pass `-url=<target>` with the deployed `build-info.html`'s URL when you can — recording it is
what lets `verify` run later with nothing but the environment name. A deployment recorded
without a `-url` can still be verified by supplying the target there instead.

It's a second, explicit CI step on purpose — a deploy happens every time you deploy, not
once like a repository binding, so it isn't folded into the default action the way
`DRIFTMAPPER_CHALLENGE` redemption is. Rerunning the same CI run (a retried step after a
network failure) is a harmless no-op; a genuinely later run for the same build/environment —
a real roll-forward — always records as a new entry. Add `-best-effort` if you'd rather warn
and continue (exit 0) than fail the whole deploy job when Driftmapper itself is unreachable:

```yaml
  - run: npx @driftmapper/cli deploy -env=production -best-effort
```

If you'd rather not integrate this into CI at all, an org admin/owner can mark a deploy
manually from that build's resolution page in the dashboard instead — a fallback path that
doesn't require any CI changes.

### Verifying a deployment

```yaml
permissions:
  id-token: write   # required — same OIDC token as registration
steps:
  # in your deploy workflow:
  - run: npx @driftmapper/cli deploy -env=production -url=${{ steps.deploy.outputs.url }} ...
  # anywhere later — including its own scheduled workflow:
  - run: npx @driftmapper/cli verify production
```

You build an artifact, you deploy an artifact, you verify what's *currently deployed*.
`verify` takes the environment name your deploy step already used — a constant that requires
nothing captured between steps — resolves the newest claim for that repository + environment,
fetches its recorded `build-info.html`, parses the meta tags, and records **what it actually
found**:

- observed == expected → recorded as `verified`, exit 0.
- observed ≠ expected → recorded as a `mismatch` and **exit 3**. This is the drift signal,
  not an error, and never swallowed by `-best-effort`.
- unreachable or unparsable target → still recorded (`fetch_failed` / `parse_failed`; failed
  observations are assertions too), then exit 1 — or exit 0 under `-best-effort`.

Environments need no pre-registration: the name comes into existence when your first
`deploy -env=<name>` claims it, and `verify` must spell it identically — a typo'd name fails
fast with the exact string you typed in the error.

Because drift exits non-zero, CI itself is the MVP alarm surface: put `verify` on a schedule
(`schedule:` cron) and GitHub Actions notifies subscribers the moment a run goes red. A
monitoring job that should stay green while it works can set `continue-on-error: true` on the
step — verification still runs and every outcome still lands in the ledger.

Flags: `-repo=<owner/name>` verifies another repository's environment (e.g. an e2e repo
checking the deployer after its own tests) — this needs an admin/owner to establish a `verify`
binding between the two repositories from the dashboard; `-url=<target>` supplies the fetch
target only for deployments recorded without one; `-header='Name: value'` (repeatable)
authenticates against targets behind e.g. a staging gateway — values are stripped automatically
if a redirect leaves the original host; and `-best-effort` covers outages only.

Driftmapper-the-service still never fetches your environment (the fetch runs here, in your own
CI); "verified" additionally remains a read-time comparison of the latest deploy vs. verify
claims in the dashboard's assertion timeline.

Rerunning the same CI run is a harmless no-op.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `DRIFTMAPPER_API_URL` | `https://api.driftmapper.com` | API base URL |
| `DRIFTMAPPER_OIDC_AUDIENCE` | `https://driftmapper.com` | `aud` claim requested from the CI provider |
| `DRIFTMAPPER_BUILD_INFO_FILE` | `build-info.html` | Output path (overridable per-run via `--output`) |
| `DRIFTMAPPER_DASHBOARD_URL` | — | Dashboard SPA origin `compare` opens (required for `compare`; no default) |
| `DRIFTMAPPER_CHALLENGE` | — | Single-use repository-authorization value (see "Authorizing a new repository" above); never a flag |
| `DRIFTMAPPER_BINARY_PATH` | — | Absolute path to a binary the npm launcher should run instead of resolving one |

## No npm? Download a raw binary

Every [GitHub Release](https://github.com/DriftMapper/cli/releases) includes a binary and
`.sha256` checksum for each supported platform (`darwin`/`linux`/`windows` × `arm64`/`amd64`).
Download, verify, run:

```bash
curl -LO https://github.com/DriftMapper/cli/releases/download/vX.Y.Z/driftmapper-linux-x64
curl -LO https://github.com/DriftMapper/cli/releases/download/vX.Y.Z/driftmapper-linux-x64.sha256
sha256sum -c driftmapper-linux-x64.sha256
chmod +x driftmapper-linux-x64
./driftmapper-linux-x64
```

## Verifying a release

Every published binary and npm package carries [npm/GitHub provenance
attestation](https://docs.npmjs.com/generating-provenance-statements) tying it back to this
source repo and the exact workflow run that built it (`npm audit signatures`, or the
"Provenance" tab on npmjs.com).

Builds are reproducible: `-trimpath`, `CGO_ENABLED=0`, and the Go version pinned via
`go.mod`. To rebuild a given release and confirm it matches:

```bash
git checkout vX.Y.Z
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.version=X.Y.Z" -o driftmapper ./cmd/driftmapper
sha256sum driftmapper   # compare against the release's .sha256
```

## Development

See [CLAUDE.md](CLAUDE.md) for architecture and the npm-packaging gotchas, and the
[Makefile](Makefile) (`make help`) for all local build/test/lint targets.

```bash
go vet ./... && go test ./...
make check   # everything CI runs: go tests, npm wrapper unit tests, pack-and-install e2e
```

## License

Apache-2.0 — see [LICENSE](LICENSE).
