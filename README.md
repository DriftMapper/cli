# driftmapper/cli

The Driftmapper CLI: registers a build so Driftmapper can track it, and writes
`build-info.html` from the response. The default action is write-only and runs in CI: it
registers the build with the CI provider's workload OIDC token, the only credential the
server accepts for registration. `compare` is a separate subcommand, a browser launcher with
zero network calls of its own, not a read command. `login`/`logout` sign you in as yourself
(see below).

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

### Recording a deployment

After the build job has registered a build, the job that deploys it records where it went:

```yaml
permissions:
  id-token: write
steps:
  - run: npx @driftmapper/cli record --environment production
```

This is a claim by your pipeline that the build is now in that environment; Driftmapper
sets the time and keeps a record per run, so a redeploy or rollback shows in the history.
It does not check what is actually running. `record` takes one module — run it once per
module in a monorepo.

Which build is recorded: by default the module's build at the commit the job runs on, which
is right when the deploy runs in the same workflow as the build. When the deploy job's commit is
not the commit being deployed — a promotion, a `workflow_dispatch`, a `workflow_run` — name it:
`--commit <sha>`, or `--build-id <id>`, the id registration printed and wrote into
`build-info.html`. Carrying it to the deploy job is your pipeline's plumbing; a job output
(`needs.build.outputs.id`) or a workflow input both work. `--url` records where the deployment
serves its build-info (https only). If the job targets a GitHub `environment:`, `--environment`
may be omitted: the environment comes from the token, and a value you pass must match it.

Only the repository that registered a module can record deployments of it.

### Comparing two builds

```bash
DRIFTMAPPER_DASHBOARD_URL=https://app.driftmapper.io \
  driftmapper compare <build-instance-id-a> <build-instance-id-b>
```

Opens the SPA compare view in your browser for an authenticated, session-aware,
field-by-field diff — read the two build-instance IDs off each target's `build-info.html`
first (they're shown directly on the page, no request required). `-a-url`/`-b-url` optionally
label each side in that view (e.g. with the deployed URL they came from). If a browser can't
be launched (headless/SSH), the URL is printed to stdout instead so it's still usable.

`compare` makes no network calls and requires `DRIFTMAPPER_DASHBOARD_URL` — there's no default
dashboard origin yet.

### Signing in as yourself

```bash
driftmapper login    # once — opens your browser to approve this device
driftmapper logout   # deletes the stored credential
```

`driftmapper login` pairs this machine with your Driftmapper account (device-code login,
same mechanism GitHub's own CLI uses) and stores the resulting credential in
`~/.config/driftmapper/credentials.json`, mode `0600`. It is how the CLI acts as *you* rather
than as a CI pipeline, for commands that read your organization's data. Registering a build is
not one of them: a build is registered from CI with the pipeline's OIDC token, and `driftmapper`
outside CI fails saying so.

### Testing against a non-production deployment

`DRIFTMAPPER_API_URL` and `DRIFTMAPPER_HUB_URL` point the CLI at any deployment, not just the
production default — this is how to validate a change (CLI or server) against staging before
it reaches prod:

```bash
export DRIFTMAPPER_API_URL=https://api-stg.driftmapper.io/api
export DRIFTMAPPER_HUB_URL=https://hub-stg.driftmapper.io
driftmapper login
```

Both must be set together — `login`'s device-code pairing and every command that uses the
credential it stores have to land on the same deployment, or the API won't honor it. There's no single "use staging" switch on purpose: it's two
independent overrides of the same zero-config mechanism every other env var here uses, not a
special case.

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `DRIFTMAPPER_API_URL` | `https://api.driftmapper.io/api` | API base URL — must include the `/api` prefix `cmd/api` mounts its routes under (an override for a non-default deployment needs it too, e.g. `https://api-stg.example.com/api`) |
| `DRIFTMAPPER_HUB_URL` | `https://hub.driftmapper.io` | Base URL `login`/`logout` talk to for device-code pairing |
| `DRIFTMAPPER_OIDC_AUDIENCE` | `https://driftmapper.io` | `aud` claim requested from the CI provider |
| `DRIFTMAPPER_BUILD_INFO_FILE` | `build-info.html` | Output path (overridable per-run via `--output`) |
| `DRIFTMAPPER_APP` | repository name | App the build belongs to (overridable per-run via `--app`) |
| `DRIFTMAPPER_MODULE` | the app name | Module within the app (overridable per-run via `--module`). A repository that builds several components runs the CLI once per module |
| `DRIFTMAPPER_ENVIRONMENT` | the CI job's environment | Environment `record` records into (overridable via `--environment`) |
| `DRIFTMAPPER_COMMIT` | the job's commit | Commit of the build `record` records as deployed (overridable via `--commit`); exclusive with `DRIFTMAPPER_BUILD_ID` |
| `DRIFTMAPPER_BUILD_ID` | — | Exact build `record` records, as printed at registration (overridable via `--build-id`) |
| `DRIFTMAPPER_DEPLOYMENT_URL` | — | https URL where the deployment serves its build-info (overridable via `--url`) |
| `DRIFTMAPPER_DASHBOARD_URL` | — | Dashboard SPA origin `compare` opens (required for `compare`; no default) |
| `DRIFTMAPPER_CHALLENGE` | — | Single-use repository-authorization value (see "Authorizing a new repository" above); never a flag |
| `DRIFTMAPPER_CONFIG_DIR` | XDG config dir | Where `login` stores its credential file |
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
