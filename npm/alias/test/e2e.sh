#!/usr/bin/env bash
# Pack-and-install end-to-end check for the unscoped `driftmapper` alias.
# This package has no logic of its own to unit-test — bin/index.js just
# requires @driftmapper/cli's launcher — so what needs proving is that
# going through the alias produces byte-identical behavior to the wrapper
# itself: the version sentinel, pipe behavior, and PATH-decoy rejection
# (see npm/wrapper/test/e2e.sh, which this mirrors and builds on).
#
# node_modules is assembled directly from packed tarballs, not `npm
# install`, for the same reason as the wrapper's e2e: the alias's
# dependency on @driftmapper/cli (and that package's own optionalDependency
# platform packages) aren't on the real registry yet at this version.
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

echo "==> npm pack alias + wrapper + host platform package"
ALIAS_TGZ="$(cd "$ROOT/npm/alias" && npm pack --silent --pack-destination "$WORK")"
WRAPPER_TGZ="$(cd "$ROOT/npm/wrapper" && npm pack --silent --pack-destination "$WORK")"
PLATFORM_TGZ="$(cd "$PLATFORM_DIR" && npm pack --silent --pack-destination "$WORK")"

echo "==> assembling an installed layout from the tarballs"
INSTALL="$WORK/install"
mkdir -p "$INSTALL/node_modules/driftmapper" \
         "$INSTALL/node_modules/@driftmapper/cli" \
         "$INSTALL/node_modules/@driftmapper/cli-${KEY}" \
         "$INSTALL/node_modules/.bin"
tar -xzf "$WORK/$ALIAS_TGZ" -C "$INSTALL/node_modules/driftmapper" --strip-components=1
tar -xzf "$WORK/$WRAPPER_TGZ" -C "$INSTALL/node_modules/@driftmapper/cli" --strip-components=1
tar -xzf "$WORK/$PLATFORM_TGZ" -C "$INSTALL/node_modules/@driftmapper/cli-${KEY}" --strip-components=1
chmod +x "$INSTALL/node_modules/@driftmapper/cli-${KEY}/bin/$BIN_NAME"
chmod +x "$INSTALL/node_modules/@driftmapper/cli/bin/index.js"
chmod +x "$INSTALL/node_modules/driftmapper/bin/index.js"
# The alias's `bin` entry is what a real install would symlink — point it
# at the alias itself, not the wrapper, so this actually exercises the
# require()-delegation path in bin/index.js rather than bypassing it.
ln -s "../driftmapper/bin/index.js" "$INSTALL/node_modules/.bin/driftmapper"

CLI_BIN="$INSTALL/node_modules/.bin/driftmapper"

echo "==> npx-equivalent invocation via the alias emits the version sentinel"
OUT="$("$CLI_BIN" --version --json)"
echo "$OUT" | grep -q '"name":"driftmapper"' || {
  echo "FAIL: sentinel missing from: $OUT"
  exit 1
}

echo "==> piping through the alias does not wedge"
"$CLI_BIN" --version | head -n1 >/dev/null

echo "==> a same-named decoy on PATH is rejected through the alias too"
DECOY_DIR="$WORK/decoy"
mkdir -p "$DECOY_DIR"
cat > "$DECOY_DIR/$BIN_NAME" <<'DECOY'
#!/usr/bin/env bash
echo "decoy ran"
exit 0
DECOY
chmod +x "$DECOY_DIR/$BIN_NAME"

rm -rf "${INSTALL:?}/node_modules/@driftmapper/cli-${KEY}"
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

echo "==> all e2e checks passed for the driftmapper alias on $KEY"
