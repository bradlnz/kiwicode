#!/usr/bin/env python3
"""Exercise configurable project slots through a real PTY, including dirty buffers."""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import signal
import statistics
import struct
import subprocess
import sys
import tempfile
import termios
import time


def main():
    binary = str(Path(sys.argv[1]).resolve())
    count = 9 if "--nine-slots" in sys.argv else 5
    with tempfile.TemporaryDirectory(prefix="kiwicode-switch-") as directory:
        root = Path(directory)
        config = root / ".config" / "code-editor"
        config.mkdir(parents=True)
        (config / "config.yaml").write_text(f"projects:\n  quick_picks: {count}\n")
        projects = [root / str(i) for i in range(1, count + 1)]
        for i, project in enumerate(projects, 1):
            project.mkdir()
            (project / f"project-{i}.txt").write_text(f"project {i}\n")
        state = root / "state" / "code-editor"
        state.mkdir(parents=True)
        (state / "projects.json").write_text(json.dumps(list(map(str, projects))))
        env = dict(os.environ, XDG_STATE_HOME=str(root / "state"),
                   HOME=directory, TERM="xterm-256color")
        slow = "--slow-checkpoint" in sys.argv
        delay = root / "delay-checkpoint"
        if slow:
            sqlite = shutil.which("sqlite3")
            assert sqlite, "sqlite3 required"
            shim = root / "bin"
            shim.mkdir()
            wrapper = shim / "sqlite3"
            wrapper.write_text(f"#!/bin/sh\nwhile [ -f {shlex.quote(str(delay))} ]; do sleep 0.01; done\n"
                               f"exec {shlex.quote(sqlite)} \"$@\"\n")
            wrapper.chmod(0o700)
            env["PATH"] = str(shim) + os.pathsep + env["PATH"]
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))
        process = subprocess.Popen([binary, str(projects[0])], env=env,
                                   stdin=slave, stdout=slave, stderr=slave)

        def expect(text):
            output = bytearray()
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if select.select([master], [], [], 0.05)[0]:
                    output.extend(os.read(master, 65536))
                    if text.encode() in output:
                        return bytes(output)
            raise AssertionError(f"Missing {text!r}: {output[-3000:]!r}")

        def send(data):
            os.write(master, data)

        try:
            expect("project-1.txt")
            send(b"X")
            expect("project-1.txt *")
            for i in range(2, count + 1):
                if slow and i == 2:
                    delay.touch()
                send(f"\x1b{i}".encode())
                if slow and i == 2:
                    started = time.monotonic()
                    expect("Switching project")
                    assert time.monotonic() - started < 1, "checkpoint froze loader"
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 100, 0, 0))
                    os.kill(process.pid, signal.SIGWINCH)
                    expect("\x1b[18;27H")  # Centered modal follows resize while sqlite is blocked.
                    expect("Switching project")  # Animation continues without input.
                    delay.unlink()
                expect(f"project-{i}.txt")
            if slow:
                delay.touch()  # Block writes throughout every cached return, including slot 1.
            times = []
            for mouse in (False, True):
                for i in range(1, count + 1):
                    started = time.monotonic()
                    if mouse:
                        column = 100 - count * 3 + 2 + (i - 1) * 3
                        send(f"\x1b[<0;{column};1M\x1b[<0;{column};1m".encode())
                    else:
                        send(f"\x1b{i}".encode())
                    frame = expect(f"project-{i}.txt")
                    times.append((time.monotonic() - started) * 1000)
                    if slow:
                        assert times[-1] < 500, f"cached slot {i} waited for blocked checkpoint"
                    assert b"Loading project" not in frame, "warm switch restarted loading"
                    if i == 1:
                        assert b"project-1.txt *" in frame, "switch lost dirty buffer"
            send(b"\x1b1")
            expect("project-1.txt *")
            send(b"\x1a")
            frame = expect("Undid last change")
            assert b"project-1.txt *" not in frame, "switch lost undo history"
            assert (projects[0] / "project-1.txt").read_text() == "project 1\n"
            if slow:
                delay.unlink()
            send(b"\x11")
            process.wait(timeout=10)
            assert process.returncode == 0
            print(f"PASS: {count} slots via Alt and mouse, dirty round trip and Undo; "
                  f"warm key/click-to-frame median {statistics.median(times):.1f} ms, "
                  f"max {max(times):.1f} ms")
            if slow:
                print("PASS: blocked sqlite checkpoint keeps loader animation and resize responsive")
                print("PASS: returning to slot 1 and all cached slots does not wait for blocked writes")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=5)
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    main()
