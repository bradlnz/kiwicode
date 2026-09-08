#!/usr/bin/env python3
"""Real PTY smoke test for editor input, paste, tabs and guarded quit."""
import fcntl
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


def main() -> None:
    binary = str(Path(sys.argv[1]).resolve())
    with tempfile.TemporaryDirectory(prefix="kiwicode-terminal-") as directory:
        root = Path(directory)
        source = root / "sample.txt"
        source.write_text("sample text\n", encoding="utf-8")
        env = dict(os.environ)
        env.update(HOME=directory, XDG_STATE_HOME=str(root / "state"),
                   SHELL="/bin/bash", PATH="/usr/bin:/bin", TERM="xterm-256color")
        master, slave = pty.openpty()
        original = termios.tcgetattr(slave)
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        process = subprocess.Popen([binary], cwd=directory, env=env, stdin=slave,
                                   stdout=slave, stderr=slave, start_new_session=True)
        transcript = bytearray()

        def drain(timeout: float = 0.08) -> bytes:
            output = bytearray()
            until = time.monotonic() + timeout
            while time.monotonic() < until:
                ready, _, _ = select.select([master], [], [], max(0, until - time.monotonic()))
                if not ready:
                    break
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                transcript.extend(chunk)
            return bytes(output)

        def expect(text: str, timeout: float = 5) -> bytes:
            expected = text.encode("utf-8")
            output = bytearray()
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                output.extend(drain(0.05))
                if expected in output:
                    return bytes(output)
                if process.poll() is not None:
                    break
            raise AssertionError(f"Missing {text!r}; tail: {bytes(transcript[-4000:])!r}")

        def send(data: bytes) -> None:
            os.write(master, data)

        try:
            frame = expect("sample.txt")
            assert b"Agent" not in frame, "removed Agent tab remains"
            send(b"\x01\x18")  # Retired Agent shortcuts must leave the editor alone.
            # First tab begins at the editor edge, with no reserved Agent width.
            send(b"\x1b[<0;3;2M\x1b[<0;3;2m")
            send(b"X")
            expect("sample.txt *")
            send(b"\x1b[200~pasted\ntext\x1b[201~")
            expect("textsample text")
            send(b"\x1a")  # A bracketed paste is a single undo operation.
            expect("Undid last change")
            assert source.read_text(encoding="utf-8") == "sample text\n"
            drain()
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 26, 90, 0, 0))
            os.kill(process.pid, signal.SIGWINCH)
            expect("\x1b[26;1H")  # No keypress: idle resize must redraw.
            send(b"\x11")
            expect("Unsaved changes")
            send(b"\x11")
            process.wait(timeout=5)
            drain()
            assert process.returncode == 0
            assert source.read_text(encoding="utf-8") == "sample text\n"
            assert termios.tcgetattr(slave) == original, "terminal mode was not restored"
            assert b"\x1b[?2004l" in transcript, "bracketed paste was not disabled on exit"
            print("PASS: editor-only tabs, retired shortcuts, bracketed paste/undo, idle resize, guarded quit, terminal restoration")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    main()
