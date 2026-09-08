#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
binary="$script_dir/code-editor"
command_dir="${XDG_BIN_HOME:-$HOME/.local/bin}"
command_path="$command_dir/kiwicode"

cd "$script_dir"
if ! pkg-config --atleast-version=0.3 vterm; then
	echo "Install libvterm development headers and pkg-config (Arch: libvterm pkgconf; Debian/Ubuntu: libvterm-dev pkg-config)." >&2
	exit 1
fi
go test ./src
go build -o "$binary" ./src

mkdir -p "$command_dir"
if [ -e "$command_path" ] && [ ! -L "$command_path" ]; then
	echo "kiwicode already exists and is not a symlink: $command_path" >&2
	exit 1
fi
ln -sfn "$binary" "$command_path"

echo "Built $command_path"
