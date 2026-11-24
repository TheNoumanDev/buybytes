#!/bin/sh
# Builds both Mac binaries into npm/bin and packs the npm package.
set -e
cd "$(dirname "$0")/.."
VERSION=$(sed -n 's/^var version = "\(.*\)"/\1/p' main.go)
for arch in arm64 amd64; do
  out=npm/bin/buybytes-darwin-$( [ $arch = amd64 ] && echo x64 || echo arm64 )
  CGO_ENABLED=0 GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$out" .
done
cp README.md LICENSE npm/
cd npm && npm version "$VERSION" --no-git-tag-version --allow-same-version >/dev/null && npm pack
