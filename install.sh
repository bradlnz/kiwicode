#!/bin/sh
# Run from an extracted Linux release; no Go toolchain is needed.
set -eu

bundle=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
command_dir="${XDG_BIN_HOME:-$HOME/.local/bin}"
data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/kiwicode"

if [ "$#" -ne 0 ]; then
	echo "Usage: ./install.sh (from an extracted release)"
	echo "Installs to $command_dir and $data_dir; uses sudo only for missing system packages."
	[ "$#" -eq 1 ] && [ "$1" = --help ] && exit 0
	exit 1
fi
if [ "$(uname -s)" != Linux ] || [ ! -x "$bundle/code-editor" ] || [ ! -f "$bundle/lib/libvterm.so.0" ]; then
	echo "Extract the Linux release for your architecture first. To create one from source, run ./release.sh." >&2
	exit 1
fi
if [ -e "$command_dir/kiwicode" ] && [ ! -L "$command_dir/kiwicode" ]; then
	echo "Refusing to overwrite an existing file: $command_dir/kiwicode" >&2
	exit 1
fi

missing=false
for command in git sqlite3 stty; do
	command -v "$command" >/dev/null 2>&1 || missing=true
done
if [ "$missing" = true ]; then
	if [ "$(id -u)" -eq 0 ]; then
		privilege=""
	else
		command -v sudo >/dev/null 2>&1 || { echo "Install git, sqlite3 and coreutils, then rerun this installer." >&2; exit 1; }
		privilege=sudo
	fi
	if command -v apt-get >/dev/null 2>&1; then
		$privilege apt-get update
		$privilege apt-get install -y git sqlite3 coreutils
	elif command -v pacman >/dev/null 2>&1; then
		$privilege pacman -S --needed --noconfirm git sqlite coreutils
	elif command -v dnf >/dev/null 2>&1; then
		$privilege dnf install -y git sqlite coreutils
	else
		echo "Install git, sqlite3 and coreutils with your package manager, then rerun this installer." >&2
		exit 1
	fi
fi

# Check architecture and libc compatibility before changing an existing installation.
libraries=$(LD_LIBRARY_PATH="$bundle/lib" ldd "$bundle/code-editor" 2>&1) || { echo "$libraries" >&2; exit 1; }
case "$libraries" in
	*"not found"*) echo "$libraries" >&2; exit 1 ;;
esac

# Keep each installation intact so an upgrade cannot overwrite a running binary.
mkdir -p "$data_dir" "$command_dir"
destination=$(mktemp -d "$data_dir/release.XXXXXX")
cp "$bundle/code-editor" "$bundle/LICENSE" "$bundle/THIRD_PARTY_NOTICES.md" "$bundle/README.md" "$destination/"
cp -R "$bundle/lib" "$bundle/examples" "$destination/"
cat > "$destination/kiwicode" <<'EOF'
#!/bin/sh
location=$(CDPATH= cd -- "$(dirname -- "$(readlink -f -- "$0")")" && pwd)
LD_LIBRARY_PATH="$location/lib${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export LD_LIBRARY_PATH
exec "$location/code-editor" "$@"
EOF
chmod 755 "$destination/kiwicode"
ln -sfnT "$destination/kiwicode" "$command_dir/kiwicode"
echo "Installed $command_dir/kiwicode"
echo "Bundled example: $destination/examples/taskboard"
case ":$PATH:" in
	*:"$command_dir":*) ;;
	*) echo "Add $command_dir to PATH to run kiwicode from any directory." ;;
esac
