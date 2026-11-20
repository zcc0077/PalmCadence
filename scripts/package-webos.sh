#!/usr/bin/env bash
set -euo pipefail

repo="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo"
output="$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
export GOTOOLCHAIN="${PALMCADENCE_TOOLCHAIN:-go1.26.11}"
source_go="$(go env GOROOT)"
cp -R "$source_go" "$scratch/go"
chmod -R u+w "$scratch/go"
patch -d "$scratch/go" -p1 --batch --forward < webos/go-linux-2.6.patch
export GOROOT="$scratch/go" GOTOOLCHAIN=local

bundle="$scratch/ca.pem"
if [[ -n "${PALMCADENCE_CA_BUNDLE:-}" ]]; then
  cp "$PALMCADENCE_CA_BUNDLE" "$bundle"
else
  curl --fail --show-error --silent --location --retry 2 https://curl.se/ca/cacert.pem -o "$bundle"
fi

CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=6,softfloat "$GOROOT/bin/go" build -trimpath -ldflags='-s -w' -o "$scratch/palmcadence-arm" ./cmd/palmcadence
CGO_ENABLED=0 GOOS=linux GOARCH=386 "$GOROOT/bin/go" build -trimpath -ldflags='-s -w' -o "$scratch/palmcadence-i686" ./cmd/palmcadence
python3 scripts/make-ipk.py "$scratch" "$output"
