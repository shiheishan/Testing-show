#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APP_NAME="${APP_NAME:-vps-monitor}"
VERSION="${VERSION:-$(tr -d '[:space:]' < VERSION)}"
TARGET_OS="${TARGET_OS:-${GOOS:-$(go env GOOS)}}"
TARGET_ARCH="${TARGET_ARCH:-${GOARCH:-$(go env GOARCH)}}"
OUT_DIR="${OUT_DIR:-release}"
GOCACHE="${GOCACHE:-$ROOT/.gocache}"
export GOCACHE

case "$OUT_DIR" in
  /*) ;;
  *) OUT_DIR="$ROOT/$OUT_DIR" ;;
esac

PACKAGE_NAME="${APP_NAME}_${VERSION}_${TARGET_OS}_${TARGET_ARCH}"
STAGING_PARENT="$OUT_DIR/.staging"
PACKAGE_DIR="$STAGING_PARENT/$PACKAGE_NAME"
ARCHIVE="$OUT_DIR/$PACKAGE_NAME.tar.gz"
BIN_NAME="$APP_NAME"

if [ "$TARGET_OS" = "windows" ]; then
  BIN_NAME="$APP_NAME.exe"
fi

if [ ! -d node_modules ]; then
  echo "node_modules is missing. Run npm ci before packaging." >&2
  exit 1
fi

cleanup() {
  rm -rf "$STAGING_PARENT"
}
trap cleanup EXIT

rm -rf "$PACKAGE_DIR"
rm -f "$ARCHIVE" "$ARCHIVE.sha256"
mkdir -p "$PACKAGE_DIR" "$OUT_DIR"

npm run build

CGO_ENABLED=0 GOOS="$TARGET_OS" GOARCH="$TARGET_ARCH" go build \
  -trimpath \
  -ldflags="-s -w" \
  -o "$PACKAGE_DIR/$BIN_NAME" \
  .

cp -R dist "$PACKAGE_DIR/dist"
cp config.yaml.example "$PACKAGE_DIR/config.yaml.example"
cp subscriptions.yaml.example "$PACKAGE_DIR/subscriptions.yaml.example"
cp README.md CHANGELOG.md VERSION "$PACKAGE_DIR/"

if [ -d samples ]; then
  cp -R samples "$PACKAGE_DIR/samples"
fi

if [ -d deploy ]; then
  cp -R deploy "$PACKAGE_DIR/deploy"
fi

cat > "$PACKAGE_DIR/QUICKSTART.md" <<EOF
# $APP_NAME $VERSION

## Requirements

- sqlite3 command available in PATH

## Start

\`\`\`bash
cp config.yaml.example config.yaml
cp subscriptions.yaml.example subscriptions.yaml
./$BIN_NAME
\`\`\`

Open http://127.0.0.1:8080 after the server starts.
EOF

tar -czf "$ARCHIVE" -C "$STAGING_PARENT" "$PACKAGE_NAME"

(
  cd "$OUT_DIR"
  ARCHIVE_BASENAME="$(basename "$ARCHIVE")"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$ARCHIVE_BASENAME" > "$ARCHIVE_BASENAME.sha256"
  else
    sha256sum "$ARCHIVE_BASENAME" > "$ARCHIVE_BASENAME.sha256"
  fi
)

echo "Created $ARCHIVE"
echo "Created $ARCHIVE.sha256"
