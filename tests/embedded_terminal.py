#!/usr/bin/env python3
"""Nested PTY smoke test. Optional installed Codex check uses --version only."""
import fcntl
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


def main():
    binary = str(Path(sys.argv[1]).resolve())
    codex = shutil.which("codex") if "--codex-check" in sys.argv else None
    with tempfile.TemporaryDirectory(prefix="kiwicode-shell-") as directory:
        root = Path(directory)
        project = root / "project"
        project.mkdir()
        (project / "00-sample.go").write_text('package main\nimport "fmt"\n')
        (project / "lib").mkdir()
        (project / "lib" / "hidden.go").write_text("package lib\n")
        config = root / ".config" / "code-editor"
        config.mkdir(parents=True)
        (config / "config.yaml").write_text("terminal:\n  height: 6\n")
        (root / ".bashrc").write_text(
            "export KIWI_RC=loaded\nalias kiwi_alias='printf alias_ok'\nPS1='shell_ready> '\n")
        env = dict(os.environ, HOME=directory, SHELL="/bin/bash",
                   XDG_STATE_HOME=str(root / "state"), TERM="xterm-256color")
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
        original = termios.tcgetattr(slave)
        process = subprocess.Popen([binary], cwd=project, env=env,
                                   stdin=slave, stdout=slave, stderr=slave)

        def expect(text):
            output = bytearray()
            deadline = time.monotonic() + 8
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.02)[0]:
                    output.extend(os.read(master, 65536))
                    if text.encode() in output:
                        return bytes(output)
            raise AssertionError(f"Missing {text!r}: {output[-4000:]!r}")

        def send(data):
            os.write(master, data)

        try:
            frame = expect("shell_ready>")
            assert b"sample.go" in frame, "default terminal replaced the editor"
            assert b"Run Application" not in frame, "obsolete launcher is still visible"
            # The dock starts open without stealing editor focus; click into it.
            send(b"\x1b[<0;80;25M\x1b[<0;80;25m")
            send(b"printf '%s:' \"$KIWI_RC\"; kiwi_alias; test -t 0 && printf ':tty_ok\\n'\r")
            expect("loaded:alias_ok:tty_ok")
            if codex:
                send((shlex.quote(codex) + " --version\r").encode())
                expect("codex-cli")
            # Paste stays shell input, and readline gets its own Ctrl+A/Tab.
            send(b"\x1b[200~printf '\\120\\101\\123\\124\\105\\137\\117\\113\\n'\x1b[201~")
            send(b"\r")
            expect("PASTE_OK")
            send(b"export KEEP_SESSION=retained\r")
            expect("shell_ready>")
            send(b"\x14")
            expect("sample.go")
            send(b"\x14")
            expect("shell_ready>")
            send(b"printf 'session=%s\\n' \"$KEEP_SESSION\"\r")
            expect("session=retained")
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
            os.kill(process.pid, signal.SIGWINCH)
            expect("shell_ready>")
            send(b"stty size\r")
            expect("6 ")  # YAML-configured PTY height stays fixed when the window grows.
            send(b"\x14")
            expect("sample.go")
            send(b"\x07")
            frame = expect("3 nodes · 1 links")
            assert "▶".encode() in frame, "graph has no visual connections"
            assert "╭".encode() in frame, "graph has no visual nodes"
            assert b"hidden.go" not in frame, "folder started expanded"
            send(b"\x1b[<0;4;6M\x1b[<0;4;6m")
            frame = expect("4 nodes · 2 links")
            assert b"hidden.go" in frame, "folder click did not reveal its file"
            send(b"\x1b[<0;4;6M\x1b[<0;4;6m")
            frame = expect("3 nodes · 1 links")
            assert b"hidden.go" not in frame, "second click did not collapse folder"
            send(b"\x1b")
            expect("Closed view")
            send(b"\x11")
            process.wait(timeout=8)
            assert process.returncode == 0
            assert termios.tcgetattr(slave) == original, "outer terminal mode not restored"
            print("PASS: default-open terminal, bashrc, alias/PATH, real TTY, paste, persistent dock, YAML height, resize, graph expand/collapse")
            if codex:
                print("PASS: installed Codex CLI runs inside embedded terminal (--version; no model calls)")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    main()
