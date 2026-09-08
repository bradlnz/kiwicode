#!/usr/bin/env python3
"""Capture the real KiwiCode TUI in xterm, with an isolated example workspace.

Linux dependencies: Xvfb, xauth, xterm, x11-utils, DejaVu Sans Mono, Pillow.
Usage: python3 tools/capture_readme.py --binary ./code-editor
No UI mock, generated terminal output, provider keys, or saved workspace is used.
"""
from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import re
import select
import shutil
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import time

COLS, ROWS = 144, 42
ANSI = re.compile(r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))")
FRAME = b"\x1b[0;49m\x1b[?25l\x1b[H"
ROOT = Path(__file__).resolve().parents[1]


def clean(data: bytes) -> str:
    return ANSI.sub("", data.decode("utf-8", errors="replace"))


def bridge(address: str, binary: str, workspace: str) -> None:
    """Forward actual editor PTY output to xterm and to the assertion driver."""
    connection = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.connect(address)
    connection.sendall((os.environ["WINDOWID"] + "\n").encode())
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    state = str(Path(workspace).parent / "state")
    env = {key: os.environ[key] for key in ("PATH", "LANG", "LC_ALL", "GOCACHE") if key in os.environ}
    env.update(HOME=str(Path(workspace).parent / "home"),
               XDG_STATE_HOME=state, XDG_CONFIG_HOME=state + "/config",
               TERM="xterm-256color", SHELL="/bin/sh",
               KIWICODE_AGENT_HISTORY="0", KIWICODE_AGENT_ALLOW_COMMANDS="0",
               KIWICODE_AGENT_ENDPOINT="", KIWICODE_AGENT_MODEL="",
               KIWICODE_AGENT_API_KEY="")
    Path(env["HOME"]).mkdir(exist_ok=True)
    process = subprocess.Popen([binary, workspace], stdin=slave, stdout=slave,
                               stderr=slave, env=env, start_new_session=True)
    os.close(slave)
    try:
        while True:
            ready, _, _ = select.select([master, connection], [], [], 0.2)
            if connection in ready:
                data = connection.recv(65536)
                if not data:
                    break
                os.write(master, data)
            if master in ready:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    break
                if not data:
                    break
                # Write the complete output unchanged, including ANSI sequences.
                sys.stdout.buffer.write(data)
                sys.stdout.buffer.flush()
                connection.sendall(data)
            if process.poll() is not None and not ready:
                break
    finally:
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
        process.wait()
        os.close(master)
        connection.close()


class Session:
    def __init__(self, connection: socket.socket, window: str, output: Path):
        self.connection, self.window, self.output = connection, window, output
        self.transcript = bytearray()
        self.actions: list[dict] = []
        self.images: list[dict] = []

    def drain(self, duration: float = 0.2) -> None:
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            ready, _, _ = select.select([self.connection], [], [], max(0, deadline - time.monotonic()))
            if not ready:
                return
            data = self.connection.recv(65536)
            if not data:
                raise RuntimeError("Editor exited before capture completed")
            self.transcript.extend(data)

    def expect(self, *texts: str, since: int = 0, timeout: float = 10) -> None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if all(text in clean(bytes(self.transcript[since:])) for text in texts):
                self.drain(0.2)
                return
            self.drain(0.1)
        raise AssertionError(f"Expected {texts!r}; terminal tail:\n{clean(bytes(self.transcript[-18000:]))}")

    def press(self, keys: bytes, label: str, *expected: str) -> None:
        self.actions.append({"action": label, "keys_hex": keys.hex()})
        start = len(self.transcript)
        self.connection.sendall(keys)
        if expected:
            self.expect(*expected, since=start)
        else:
            self.drain(0.25)

    def click(self, column: int, row: int, label: str, *expected: str) -> None:
        self.press(f"\x1b[<0;{column};{row}M\x1b[<0;{column};{row}m".encode(), label, *expected)

    def capture(self, name: str, *expected: str) -> None:
        from PIL import ImageGrab
        self.drain(0.25)
        frame = bytes(self.transcript).rsplit(FRAME, 1)[-1]
        text = clean(frame)
        for phrase in expected:
            if phrase not in text:
                raise AssertionError(f"{name}: missing {phrase!r} in current frame: {text}")
        for error in ("Config:", "panic:", "Agent unavailable", "exit:", "FAIL"):
            if error in text:
                raise AssertionError(f"{name}: unexpected error {error!r}")
        image_path = self.output / f"{name}.png"
        geometry = subprocess.check_output(
            ["xwininfo", "-display", os.environ["DISPLAY"], "-id", self.window],
            text=True, timeout=10)
        def field(label: str) -> int:
            match = re.search(re.escape(label) + r":\s*(-?\d+)", geometry)
            if not match:
                raise RuntimeError(f"Missing {label} in xwininfo output")
            return int(match.group(1))
        x, y = field("Absolute upper-left X"), field("Absolute upper-left Y")
        width, height = field("Width"), field("Height")
        image = ImageGrab.grab(bbox=(x, y, x + width, y + height), xdisplay=os.environ["DISPLAY"])
        size = image.size
        if size[0] < 1300 or size[1] < 700:
            raise AssertionError(f"Unexpected window size: {size}")
        image.save(image_path, optimize=True)
        (self.output / f"{name}.ansi").write_bytes(frame)
        self.images.append({"file": image_path.name, "width": size[0], "height": size[1],
                            "sha256": hashlib.sha256(image_path.read_bytes()).hexdigest(),
                            "assertions": list(expected)})
        print(f"PASS: {name}: {', '.join(expected)}", flush=True)


def capture(binary: Path, example: Path, output: Path) -> None:
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="kiwicode-capture-") as temporary:
        temporary = Path(temporary)
        workspace = temporary / "taskboard"
        shutil.copytree(example, workspace)
        original = {p.relative_to(workspace): p.read_bytes() for p in workspace.rglob("*") if p.is_file()}
        server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        address = str(temporary / "driver.sock")
        server.bind(address)
        server.listen(1)
        server.settimeout(15)
        command = ["xterm", "-geometry", f"{COLS}x{ROWS}+0+0", "-fa", "DejaVu Sans Mono",
                   "-fs", "12", "-b", "12", "-bg", "#111711", "-fg", "#e4e4e4",
                   "-xrm", "XTerm*cursorBlink: false", "-xrm", "XTerm*renderFont: true",
                   "-title", "KiwiCode — Taskboard", "-e", sys.executable, str(Path(__file__).resolve()),
                   "--bridge", address, "--binary", str(binary), "--example", str(workspace)]
        with (output / "xterm.log").open("wb") as log:
            terminal = subprocess.Popen(command, stdout=log, stderr=log)
            session = None
            try:
                connection, _ = server.accept()
                connection.settimeout(15)
                header = bytearray()
                while not header.endswith(b"\n"):
                    header.extend(connection.recv(1))
                connection.settimeout(None)
                session = Session(connection, header.decode().strip(), output)
                session.expect(".code-editor.yaml", "TERMINAL")
                session.press(b"\x06", "Open file search", "Search files:")
                session.press(b"main.go", "Find main.go", "Search files: main.go")
                session.press(b"\r", "Open main.go", "func main()")
                session.press(b"\x10", "Show and focus file explorer", "README.md")
                # These are real explorer mouse actions in the fixed sample tree.
                session.click(5, 5, "Expand docs", "tasks.md")
                session.click(5, 8, "Expand internal", "httpapi")
                session.click(7, 9, "Expand httpapi", "tasks_test.go")
                session.click(7, 12, "Expand tasks", "store_test.go")
                session.click(5, 16, "Expand web", "styles.css")
                session.click(12, 10, "Open tasks.go from file explorer", "ListTasks(w")
                session.capture("editor-explorer", "main.go", "tasks_test.go", "store_test.go", "ListTasks(w")

                session.press(b"\x06", "Open file search", "Search files:")
                session.press(b"tasks", "Filter file paths by tasks", "Search files: tasks", "web/tasks.js")
                session.capture("file-search", "Search files: tasks", "internal/httpapi/tasks.go", "web/tasks.js")
                session.press(b"\x1b[B\r", "Open tasks.go from filtered results", "ListTasks(w")

                session.press(b"\x15", "Open function search", "Search functions:")
                session.press(b"ListTasks", "Find ListTasks definitions", "Search functions: ListTasks", "internal/tasks/store.go:")
                session.capture("symbol-search", "Search functions: ListTasks", "internal/httpapi/tasks.go:", "internal/tasks/store.go:")
                session.press(b"\r", "Jump to ListTasks definition", "Ln 20, Col 1")

                session.press(b"\x05", "Open test explorer", "TestListTasks", "TestTasksAreCopied")
                session.press(b"\r", "Jump to TestListTasks", "Ln 12, Col 1")
                session.capture("test-explorer", "TestListTasks", "TestHealth", "TestSeedTasks", "TestTasksAreCopied", "httptest.NewRequest")

                session.press(b"\x05", "Focus test explorer", "Tests: click or Enter to open")
                start = len(session.transcript)
                session.press(b"\x12", "Run all example tests", "Tests: go test ./...")
                session.expect("Tests passed", since=start, timeout=90)
                session.capture("test-run", "$ go test ./...",
                                "ok   example.com/kiwi-taskboard/internal/httpapi",
                                "ok   example.com/kiwi-taskboard/internal/tasks")
                session.press(b"\x14", "Hide test output")

                session.click(3, 1, "Open File menu", "New Project")
                session.click(3, 4, "Create a new project", "New project folder")
                session.press(b"\x7f" * (len(str(temporary)) + 1) + b"~/projects/hello-world",
                              "Choose the new project folder", "~/projects/hello-world")
                session.capture("new-project", "New project folder", "~/projects/hello-world", "Enter create")
                session.press(b"\r", "Create and open the project", "No files found", "Ctrl+N new file")
                session.capture("empty-project", "No files found", "Ctrl+N new file", "untitled.txt")
                session.press(b"\x05", "Show empty test explorer", "No tests found")
                session.capture("empty-tests", "No tests found")
                session.connection.sendall(b"\x11")
                terminal.wait(timeout=10)
                if terminal.returncode != 0:
                    raise RuntimeError(f"xterm exited with {terminal.returncode}")
                current = {p.relative_to(workspace): p.read_bytes() for p in workspace.rglob("*") if p.is_file()}
                if current != original:
                    raise AssertionError("Capture changed the example workspace")
                manifest = {"renderer": "Real editor PTY -> xterm under Xvfb -> Pillow X11 window capture",
                            "source_commit": os.environ.get("KIWICODE_SOURCE_COMMIT", "local working tree"),
                            "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                            "columns": COLS, "rows": ROWS, "example": "examples/taskboard",
                            "workspace_unchanged": True, "images": session.images}
                (output / "capture.json").write_text(json.dumps(manifest, indent=2) + "\n")
                print("PASS: search selection, definition jumps, test navigation and execution, unchanged example workspace", flush=True)
            finally:
                if session:
                    (output / "session.ansi").write_bytes(session.transcript)
                    (output / "actions.json").write_text(json.dumps(session.actions, indent=2) + "\n")
                    session.connection.close()
                if terminal.poll() is None:
                    terminal.terminate()
                    try:
                        terminal.wait(timeout=3)
                    except subprocess.TimeoutExpired:
                        terminal.kill()
                        terminal.wait()
                server.close()


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, default=ROOT / "code-editor")
    parser.add_argument("--example", type=Path, default=ROOT / "examples/taskboard")
    parser.add_argument("--output", type=Path, default=ROOT / "assets/screenshots")
    parser.add_argument("--on-display", action="store_true", help=argparse.SUPPRESS)
    parser.add_argument("--bridge", help=argparse.SUPPRESS)
    args = parser.parse_args()
    if args.bridge:
        bridge(args.bridge, str(args.binary.resolve()), str(args.example.resolve()))
        return
    if not args.binary.is_file() or not os.access(args.binary, os.X_OK):
        parser.error(f"Build an executable first: {args.binary}")
    for tool in ("xvfb-run", "Xvfb", "xterm", "xwininfo"):
        if not shutil.which(tool):
            parser.error(f"Missing screenshot dependency: {tool}")
    if not args.on_display:
        command = ["xvfb-run", "-a", "-s", "-screen 0 1800x1200x24",
                   sys.executable, str(Path(__file__).resolve()), *sys.argv[1:], "--on-display"]
        subprocess.run(command, check=True, timeout=180)
        return
    capture(args.binary.resolve(), args.example.resolve(), args.output.resolve())


if __name__ == "__main__":
    main()
