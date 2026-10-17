"""Exercise the bundled Windows EXE with disposable config and renderer cache.

No provider accounts, browser profiles or existing application processes are used.
The receipt records only build identity and pass/fail checks, never credentials.
"""
import argparse
import ctypes as c
from ctypes import wintypes as w
import hashlib
import json
import os
from pathlib import Path
import socket
import struct
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen

parser = argparse.ArgumentParser()
parser.add_argument("exe", type=Path)
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
assert os.name == "nt", "Windows acceptance requires Windows"
exe = args.exe.resolve(strict=True)
output = args.output.resolve()
output.mkdir(parents=True, exist_ok=True)
root = Path(tempfile.mkdtemp(prefix="windows-", dir=output))
user32 = c.WinDLL("user32", use_last_error=True)
callback = c.WINFUNCTYPE(w.BOOL, w.HWND, w.LPARAM)
user32.EnumWindows.argtypes = [callback, w.LPARAM]
user32.EnumChildWindows.argtypes = [w.HWND, callback, w.LPARAM]
user32.GetWindowThreadProcessId.argtypes = [w.HWND, c.POINTER(w.DWORD)]
user32.IsWindowVisible.argtypes = [w.HWND]
user32.GetClassNameW.argtypes = [w.HWND, w.LPWSTR, c.c_int]
user32.GetWindowTextW.argtypes = [w.HWND, w.LPWSTR, c.c_int]
user32.PostMessageW.argtypes = [w.HWND, w.UINT, w.WPARAM, w.LPARAM]


def window(pid, class_name):
    found = []

    @callback
    def visit(hwnd, _):
        owner = w.DWORD()
        user32.GetWindowThreadProcessId(hwnd, c.byref(owner))
        buf = c.create_unicode_buffer(256)
        user32.GetClassNameW(hwnd, buf, len(buf))
        if owner.value == pid and user32.IsWindowVisible(hwnd) and buf.value == class_name:
            found.append(hwnd)
        return True

    user32.EnumWindows(visit, 0)
    return found[0] if found else None


def wait_for(check, message, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        result = check()
        if result:
            return result
        time.sleep(0.2)
    raise AssertionError(message)


def free_port():
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def request(port, path, key=""):
    req = Request(f"http://127.0.0.1:{port}{path}", headers={"Authorization": "Bearer " + key})
    with urlopen(req, timeout=2) as response:
        return json.load(response)


def healthy(port):
    try:
        return request(port, "/health").get("service") == "MIXED-API"
    except (OSError, ValueError):
        return False


children = []
env = dict(os.environ, LOCALAPPDATA=str(root / "cache"))
port = free_port()
config = root / "mixed-api.json"
config.write_text(json.dumps({"listen": f"127.0.0.1:{port}", "api_key": "fixture-client",
                              "admin_key": "fixture-admin", "backends": []}), encoding="utf-8")


def launch():
    proc = subprocess.Popen([str(exe), "--config", str(config)], cwd=root, env=env,
                            creationflags=subprocess.CREATE_NO_WINDOW,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    children.append(proc)
    return proc


def close_window(proc, hwnd):
    user32.PostMessageW(hwnd, 0x10, 0, 0)  # WM_CLOSE, only our test process
    proc.wait(timeout=15)


try:
    with exe.open("rb") as stream:
        stream.seek(0x3C)
        header = struct.unpack("<I", stream.read(4))[0]
        stream.seek(header + 24 + 68)
        assert struct.unpack("<H", stream.read(2))[0] == 2, "EXE uses a console subsystem"
    primary = launch()
    wait_for(lambda: healthy(port), "Gateway did not start")
    main_window = wait_for(lambda: window(primary.pid, "MIXED-API.Desktop"), "Independent application window missing")
    # Creating a native HWND precedes renderer extraction; wait for that too.
    renderer = root / "cache" / "MIXED-API" / "renderer"
    wait_for(lambda: list(renderer.glob("*/msedgewebview2.exe")), "Bundled renderer was not extracted into fresh cache", 120)
    assert not window(primary.pid, "ConsoleWindowClass"), "Visible application console"
    status = request(port, "/api/status", "fixture-admin")
    assert status["service"] == "MIXED-API" and status["version"], "Build identity missing"
    assert request(port, "/v1/models", "fixture-client")["data"] == [], "Unexpected fresh model catalog"
    before = config.read_bytes()
    reopened = launch()
    second_window = wait_for(lambda: window(reopened.pid, "MIXED-API.Desktop"), "Same build could not reopen")
    close_window(reopened, second_window)
    assert reopened.returncode == 0 and healthy(port), "Closing reopened window stopped the owning gateway"
    close_window(primary, main_window)
    assert primary.returncode == 0, "Application did not exit cleanly"
    wait_for(lambda: not healthy(port), "Gateway survived application exit")
    assert config.read_bytes() == before, "Reopening modified the saved configuration"

    # Old releases authenticated /api/status but provided no build identity.
    # The new EXE must show an actionable dialog, never load that old dashboard.
    requests = []

    class LegacyGateway(BaseHTTPRequestHandler):
        def do_GET(self):
            requests.append(self.path)
            assert self.headers.get("Authorization") == "Bearer fixture-admin"
            body = b'{"processes":{},"backends":[]}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_):
            pass

    legacy = ThreadingHTTPServer(("127.0.0.1", port), LegacyGateway)
    threading.Thread(target=legacy.serve_forever, daemon=True).start()
    try:
        blocked = launch()
        dialog = wait_for(lambda: window(blocked.pid, "#32770"), "Old process was not reported in a native dialog")
        labels = []

        @callback
        def read_label(hwnd, _):
            buf = c.create_unicode_buffer(1024)
            user32.GetWindowTextW(hwnd, buf, len(buf))
            labels.append(buf.value)
            return True

        user32.EnumChildWindows(dialog, read_label, 0)
        assert any("请先完全退出" in text for text in labels), "Startup dialog has no recovery instruction"
        assert requests == ["/api/status"], "New EXE loaded the old dashboard"
        close_window(blocked, dialog)
        assert blocked.returncode == 1, "Incompatible listener was accepted"
    finally:
        legacy.shutdown()
        legacy.server_close()
    with exe.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha256").hexdigest()
    receipt = {"version": status["version"], "sha256": digest,
               "passed": ["GUI subsystem", "independent window", "fresh bundled renderer cache",
                          "empty model catalog", "same-version reopen", "clean shutdown", "configuration preserved",
                          "old service rejected with native recovery dialog"],
               "not_verified": ["provider account generation", "clean Windows installation", "installer upgrade"]}
    (output / "acceptance.json").write_text(json.dumps(receipt, indent=2) + "\n", encoding="utf-8")
    print("Windows EXE acceptance passed")
finally:
    for proc in children:
        if proc.poll() is None:
            proc.terminate()  # Only exact child processes started by this script.
            proc.wait(timeout=15)
