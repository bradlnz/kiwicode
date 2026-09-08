#!/bin/sh
# Package on the oldest Linux/glibc system the release should support.
set -eu
cd "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
[ "$(uname -s)" = Linux ] || { echo "Build Linux releases on Linux." >&2; exit 1; }
pkg-config --atleast-version=0.3 vterm
go test ./src
name="kiwicode-linux-$(go env GOARCH)"
mkdir -p dist
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT HUP INT TERM
mkdir -p "$stage/$name/lib"
CGO_ENABLED=1 go build -trimpath -o "$stage/$name/code-editor" ./src
cp -L "$(pkg-config --variable=libdir vterm)/libvterm.so.0" "$stage/$name/lib/"
cp install.sh LICENSE THIRD_PARTY_NOTICES.md README.md "$stage/$name/"
cp -R examples "$stage/$name/"
tar -czf "dist/$name.tar.gz" -C "$stage" "$name"
(cd dist && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
echo "Created dist/$name.tar.gz and checksum. Extract it and run $name/install.sh."
