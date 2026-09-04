#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
binary="$script_dir/code-editor"
command_dir="${XDG_BIN_HOME:-$HOME/.local/bin}"
command_path="$command_dir/kiwicode"

cd "$script_dir"
go test ./src
go build -o "$binary" ./src

mkdir -p "$command_dir"
if [ -L "$command_path" ]; then
	[ "$(readlink "$command_path")" = "$binary" ] || {
		echo "kiwicode already points somewhere else: $command_path" >&2
		exit 1
	}
elif [ -e "$command_path" ]; then
	echo "kiwicode already exists and is not a symlink: $command_path" >&2
	exit 1
else
	ln -s "$binary" "$command_path"
fi

echo "Built $command_path"
