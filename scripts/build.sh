#!/bin/sh

set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST="${ROOT}/dist"
mkdir -p "$DIST"

build() {
  NAME=$1
  ARCH=$2
  shift 2
  printf 'Building %s...\n' "$NAME"
  env CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$@" \
    go build -trimpath -ldflags='-s -w' -o "${DIST}/vps-reality-master-${NAME}" "$ROOT"
}

build linux-amd64 amd64
build linux-arm64 arm64
build linux-armv7 arm GOARM=7
build linux-mips-softfloat mips GOMIPS=softfloat
build linux-mipsle-softfloat mipsle GOMIPS=softfloat

printf 'Build artifacts are in %s\n' "$DIST"
