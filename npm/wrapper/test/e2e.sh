#!/usr/bin/env bash
# Pack-and-install end-to-end check: builds the Go binary for the host
# platform, npm-packs the wrapper and the matching platform package, and
# installs them into a scratch node_modules — then exercises the launcher
# for real. This is what catches `files`/exec-bit/exports/resolution
# regressions before a release makes them immutable on the registry.
#
# node_modules is assembled directly from the packed tarballs rather than
# via `npm install <tarball>`: the wrapper's optionalDependencies name
# @driftmapper/cli-<platform> packages that aren't published to the real
# registry yet, so a real `npm install` would try to hit the network for
# them. Extracting the tarballs by hand exercises the exact same resolution
# code path (require.resolve against node_modules) without depending on
# registry state.
#
# The CLI's default action picks its producer from the environment (DRFT-129:
# the verified/OIDC producer when ACTIONS_ID_TOKEN_REQUEST_URL is set, the
# declared/credentialed producer otherwise) and needs live CI env vars or a
# stored login plus a reachable API, so it can't be driven end to end here.
# What's tested instead is the launcher/resolver contract: the version
# sentinel, pipe behavior, clean error propagation, the missing-package
# error, and PATH-decoy rejection.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

PLATFORM="$(node -e 'console.log(process.platform)')"
ARCH="$(node -e 'console.log(process.arch)')"
KEY="${PLATFORM}-${ARCH}"
BIN_NAME="driftmapper"
[ "$PLATFORM" = "win32" ] && BIN_NAME="driftmapper.exe"

PLATFORM_DIR="$ROOT/npm/platforms/cli-${KEY}"
if [ ! -d "$PLATFORM_DIR" ]; then
  echo "e2e: no platform package for $KEY on this host — skipping"
  exit 0
fi

WORK="$(mktemp -d)"
GO_BIN="$PLATFORM_DIR/bin/$BIN_NAME"
cleanup() {
  rm -f "$GO_BIN"
  rm -rf "$WORK"
}
trap cleanup EXIT

echo "==> building the Go binary for $KEY"
( cd "$ROOT" && CGO_ENABLED=0 go build -o "$GO_BIN" ./cmd/driftmapper )
chmod +x "$GO_BIN"

echo "==> npm pack wrapper + host platform package"
WRAPPER_TGZ="$(cd "$ROOT/npm/wrapper" && npm pack --silent --pack-destination "$WORK")"
PLATFORM_TGZ="$(cd "$PLATFORM_DIR" && npm pack --silent --pack-destination "$WORK")"

echo "==> asserting the exec bit survived npm pack"
tar -tvf "$WORK/$PLATFORM_TGZ" | grep "bin/$BIN_NAME" | grep -q -- '-rwx' || {
  echo "FAIL: exec bit not preserved on the packed binary"
  exit 1
}

echo "==> assembling an installed layout from the tarballs"
INSTALL="$WORK/install"
mkdir -p "$INSTALL/node_modules/@driftmapper/cli" \
         "$INSTALL/node_modules/@driftmapper/cli-${KEY}" \
         "$INSTALL/node_modules/.bin"
tar -xzf "$WORK/$WRAPPER_TGZ" -C "$INSTALL/node_modules/@driftmapper/cli" --strip-components=1
tar -xzf "$WORK/$PLATFORM_TGZ" -C "$INSTALL/node_modules/@driftmapper/cli-${KEY}" --strip-components=1
chmod +x "$INSTALL/node_modules/@driftmapper/cli-${KEY}/bin/$BIN_NAME"
chmod +x "$INSTALL/node_modules/@driftmapper/cli/bin/index.js"
ln -s "../@driftmapper/cli/bin/index.js" "$INSTALL/node_modules/.bin/driftmapper"

CLI_BIN="$INSTALL/node_modules/.bin/driftmapper"

echo "==> npx-equivalent invocation emits the version sentinel"
OUT="$("$CLI_BIN" --version --json)"
echo "$OUT" | grep -q '"name":"driftmapper"' || {
  echo "FAIL: sentinel missing from: $OUT"
  exit 1
}

echo "==> piping does not wedge"
"$CLI_BIN" --version | head -n1 >/dev/null

echo "==> a missing CI environment fails clean and nonzero, not hung or silent"
# DRFT-129: without ACTIONS_ID_TOKEN_REQUEST_URL the default action runs the
# declared (laptop) producer, which needs a prior `driftmapper login` — so a
# no-CI environment with no stored credential must fail fast with login
# guidance, not hang or silently succeed. HOME points at a scratch dir so the
# failure is the meaningful "not logged in" state, not a no-config-dir error.
if OUT="$(env -i PATH="$PATH" HOME="$WORK/home" "$CLI_BIN" 2>&1)"; then
  echo "FAIL: expected a nonzero exit with no GitHub Actions OIDC env vars set"
  exit 1
fi
echo "$OUT" | grep -qi "driftmapper login" || {
  echo "FAIL: missing-CI error was not actionable: $OUT"
  exit 1
}

echo "==> a missing platform package fails clean, not silently"
rm -rf "${INSTALL:?}/node_modules/@driftmapper/cli-${KEY}"
ERR="$("$CLI_BIN" --version --json 2>&1 >/dev/null || true)"
echo "$ERR" | grep -q "cli-${KEY} is not installed" || {
  echo "FAIL: missing-package error was not as expected: $ERR"
  exit 1
}

echo "==> a same-named decoy on PATH is rejected, not executed"
DECOY_DIR="$WORK/decoy"
mkdir -p "$DECOY_DIR"
cat > "$DECOY_DIR/$BIN_NAME" <<'DECOY'
#!/usr/bin/env bash
echo "decoy ran"
exit 0
DECOY
chmod +x "$DECOY_DIR/$BIN_NAME"

DECOY_STDOUT="$WORK/decoy.stdout"
DECOY_STDERR="$WORK/decoy.stderr"
PATH="$DECOY_DIR:$PATH" "$CLI_BIN" --version --json >"$DECOY_STDOUT" 2>"$DECOY_STDERR" || true

grep -q "did not identify as this CLI" "$DECOY_STDERR" || {
  echo "FAIL: decoy was not rejected as expected: $(cat "$DECOY_STDERR")"
  exit 1
}
if grep -q "decoy ran" "$DECOY_STDOUT" "$DECOY_STDERR" 2>/dev/null; then
  echo "FAIL: the decoy binary was actually executed"
  exit 1
fi

echo "==> all e2e checks passed for $KEY"
