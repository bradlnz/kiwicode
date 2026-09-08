#!/usr/bin/env python3
"""Check a real release install/upgrade in an isolated home; no system changes."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


archive = Path(sys.argv[1]).resolve()
for command in ("git", "sqlite3", "stty"):
    assert shutil.which(command), f"Install {command} before running this test"
with tempfile.TemporaryDirectory(prefix="kiwicode-install-") as directory:
    root = Path(directory)
    extracted = root / "extracted release"
    extracted.mkdir()
    subprocess.run(["tar", "-xzf", archive, "-C", extracted], check=True)
    bundle, = extracted.iterdir()
    env = dict(os.environ, HOME=str(root / "home"),
               XDG_BIN_HOME=str(root / "command path"),
               XDG_DATA_HOME=str(root / "data path"))
    command = Path(env["XDG_BIN_HOME"]) / "kiwicode"

    def install():
        return subprocess.run([bundle / "install.sh"], env=env, cwd=root,
                              text=True, capture_output=True)

    result = install()
    assert result.returncode == 0, result.stderr
    first = command.resolve()
    assert (first.parent / "lib/libvterm.so.0").is_file()
    assert (first.parent / "examples/taskboard/main.go").is_file()
    result = install()
    assert result.returncode == 0, result.stderr
    assert command.resolve() != first and first.exists()
    command.unlink()
    command.write_text("existing user command")
    result = install()
    assert result.returncode != 0 and command.read_text() == "existing user command"
    command.unlink()
    result = install()
    assert result.returncode == 0, result.stderr
    shutil.rmtree(extracted)
    result = subprocess.run([command, "--mcp", root], env=env, text=True,
                            input='{"jsonrpc":"2.0","id":1,"method":"ping"}\n',
                            capture_output=True, check=True)
    assert json.loads(result.stdout)["result"] == {}
    print("PASS: release install, paths with spaces, upgrade, overwrite protection, launch after extraction removal")
