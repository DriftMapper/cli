# driftmapper/cli

The Driftmapper CLI: registers a build from inside your CI so Driftmapper can track it.
The default action is write-only — it acquires a workload OIDC token, calls `POST
/v1/builds`, and writes `build-info.html`. `compare` (below) is the one other command, and it
makes zero network calls of its own — it's a browser launcher, not a read command.

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

### Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `DRIFTMAPPER_API_URL` | `https://api.driftmapper.com` | API base URL |
| `DRIFTMAPPER_OIDC_AUDIENCE` | `https://driftmapper.com` | `aud` claim requested from the CI provider |
| `DRIFTMAPPER_BUILD_INFO_FILE` | `build-info.html` | Output path (overridable per-run via `--output`) |
| `DRIFTMAPPER_DASHBOARD_URL` | — | Dashboard SPA origin `compare` opens (required for `compare`; no default) |
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
