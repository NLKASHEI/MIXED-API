"""Clean-emulator acceptance of the CI APK; does not sign in to any provider."""
import json
import os
import pathlib
import re
import subprocess
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

OUTPUT = pathlib.Path("android-device-evidence")
PACKAGE = "com.mixedapi.local"


def adb(*args, binary=False):
    result = subprocess.run(["adb", *args], check=True, capture_output=True, timeout=30)
    return result.stdout if binary else result.stdout.decode("utf-8")


def http(path, key="", method="GET", payload=None):
    request = urllib.request.Request("http://127.0.0.1:18765" + path, method=method)
    if payload is not None:
        request.data = json.dumps(payload).encode("utf-8")
        request.add_header("Content-Type", "application/json")
    if key:
        request.add_header("Authorization", "Bearer " + key)
    try:
        with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(request, timeout=3) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


def wait_for(check, seconds=30):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        try:
            if check():
                return
        except (OSError, urllib.error.URLError, subprocess.SubprocessError, ET.ParseError):
            pass
        time.sleep(1)
    raise AssertionError("Android acceptance condition timed out")


def screenshot(name):
    (OUTPUT / name).write_bytes(adb("exec-out", "screencap", "-p", binary=True))


def tree():
    for attempt in range(3):
        # A package update can temporarily leave UI automation without an idle
        # hierarchy. Never use the previous screenshot's stale XML as success.
        adb("shell", "rm", "-f", "/sdcard/mixed-ui.xml")
        status = adb("shell", "uiautomator", "dump", "/sdcard/mixed-ui.xml")
        raw = ""
        try:
            raw = adb("exec-out", "cat", "/sdcard/mixed-ui.xml")
            return ET.fromstring(raw)
        except (ET.ParseError, subprocess.CalledProcessError):
            (OUTPUT / "ui-dump-diagnostic.txt").write_text(status + "\n" + raw, encoding="utf-8")
            if attempt == 2:
                raise
            time.sleep(1)


def tap(node):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", node.attrib["bounds"]))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))


assert os.environ.get("MIXED_ANDROID_DISPOSABLE_EMULATOR") == "1", "Acceptance requires an explicitly disposable emulator"
assert re.fullmatch(r"emulator-\d+", adb("get-serialno").strip()), "Refusing to modify a physical device"
assert adb("shell", "getprop", "ro.kernel.qemu").strip() == "1", "Expected Android emulator"
OUTPUT.mkdir(exist_ok=True)

try:
    installed = json.loads(pathlib.Path("android-package.json").read_text())
    baseline = json.loads(pathlib.Path("android-update-baseline.json").read_text())
    assert installed["signer_sha256"] == baseline["signer_sha256"]
    assert installed["version_code"] > baseline["version_code"]
    assert installed["debuggable"] is False and baseline["debuggable"] is False
    adb("install", "MIXED-API-Android-update-baseline.apk")
    adb("shell", "pm", "grant", PACKAGE, "android.permission.POST_NOTIFICATIONS")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.MainActivity")
    adb("forward", "tcp:18765", "tcp:8765")
    wait_for(lambda: http("/health")[0] == 200)
    # This is a new disposable emulator. Credentials stay in memory, never artifacts/logs.
    # Only the disposable, rootable CI emulator reads these files. The shipped
    # application is not debuggable and does not require root on users' devices.
    prefs = ET.fromstring(adb("exec-out", "cat", "/data/user/0/" + PACKAGE + "/shared_prefs/mixed_config.xml"))
    config = json.loads(next(node.text for node in prefs if node.attrib.get("name") == "state"))
    assert http("/api/config", config["api_key"])[0] == 401
    code, data = http("/api/config", config["admin_key"])
    assert code == 200
    assert all(not source["enabled"] for source in json.loads(data)["backends"])
    # Synthetic credential exercises the real Android Keystore implementation;
    # the source remains disabled and no provider request/login is performed.
    fixture = "emulator-keystore-fixture"
    assert http("/api/accounts/codex/import", config["admin_key"], "POST", {
        "auth_mode": "chatgpt", "tokens": {"access_token": fixture,
        "refresh_token": "", "account_id": "emulator-account"}})[0] == 200
    sealed = adb("exec-out", "cat", "/data/user/0/" + PACKAGE + "/no_backup/accounts/codex.dat", binary=True)
    assert len(sealed) > 29 and fixture.encode() not in sealed
    # Upgrade in place, with a higher version code and the same signing identity.
    # Android must retain both configuration and the app's existing Keystore key.
    adb("shell", "am", "force-stop", PACKAGE)
    adb("install", "-r", "MIXED-API-Android.apk")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.MainActivity")
    wait_for(lambda: http("/health")[0] == 200)
    assert http("/api/config", config["admin_key"])[0] == 200
    assert json.loads(http("/api/accounts/codex", config["admin_key"])[1])["status"] == "authorized"
    wait_for(lambda: any(node.attrib.get("text") == "工作台" for node in tree().iter("node")), 45)
    screenshot("console.png")
    # The provider page must never open during ordinary installation/startup.
    activities = adb("shell", "dumpsys", "activity", "activities")
    assert "/.MainActivity" in activities and "/.GoogleSourceActivity" not in activities
    adb("shell", "cmd", "statusbar", "expand-notifications")
    # Android can defer a newly started foreground-service notification. Give
    # System UI time to post it without changing the app's notification policy.
    time.sleep(10)
    for attempt in range(12):
        root = tree()
        nodes = list(root.iter("node"))
        if not any(node.attrib.get("package") == "com.android.systemui" for node in nodes):
            adb("shell", "cmd", "statusbar", "expand-notifications")
            time.sleep(2)
            continue
        stop = next((node for node in nodes if node.attrib.get("text") == "停止服务"), None)
        if stop is not None:
            screenshot("notification.png")
            tap(stop)
            break
        parents = {child: parent for parent in root.iter() for child in parent}
        own = next((node for node in nodes if "MIXED-API" in node.attrib.get("text", "")), None)
        expand = None
        while own is not None and own is not root:
            expand = next((node for node in own.iter("node") if node.attrib.get("resource-id", "").endswith("/expand_button") and "collapse" not in node.attrib.get("content-desc", "").lower()), None)
            if expand is not None:
                break
            own = parents.get(own)
        if expand is not None:
            tap(expand)
        else:
            # Scroll the notification list upward to reveal later cards. A
            # downward swipe expands quick settings and hides the target.
            adb("shell", "input", "swipe", "160", "570", "160", "260", "400")
        time.sleep(1)
    else:
        (OUTPUT / "notifications.xml").write_bytes(ET.tostring(root, encoding="utf-8"))
        # Disposable emulator, no real accounts. This redacted diagnostic
        # distinguishes missing permission/channel from a UI locator failure.
        (OUTPUT / "notification-state.txt").write_text(adb("shell", "dumpsys", "notification"), encoding="utf-8")
        raise AssertionError("Running notification has no accessible stop action")

    def stopped():
        try:
            http("/health")
            return False
        except (OSError, urllib.error.URLError):
            return True

    wait_for(stopped, 10)
    adb("shell", "am", "force-stop", PACKAGE)
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.MainActivity")
    wait_for(lambda: http("/health")[0] == 200)
    assert http("/api/config", config["admin_key"])[0] == 200
    code, data = http("/api/accounts/codex", config["admin_key"])
    assert code == 200 and json.loads(data)["status"] == "authorized"
    assert http("/api/accounts/codex", config["admin_key"], "DELETE")[0] == 200
    assert json.loads(http("/api/accounts/codex", config["admin_key"])[1])["status"] == "none"
    crash = adb("logcat", "-d", "-s", "AndroidRuntime:E")
    assert "FATAL EXCEPTION" not in crash
    # Also install the exact final APK with no preceding application or state.
    adb("uninstall", PACKAGE)
    adb("install", "MIXED-API-Android.apk")
    adb("shell", "pm", "grant", PACKAGE, "android.permission.POST_NOTIFICATIONS")
    adb("shell", "cmd", "statusbar", "collapse")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.MainActivity")
    wait_for(lambda: http("/health")[0] == 200)
    assert http("/api/config", config["admin_key"])[0] == 401
    prefs = ET.fromstring(adb("exec-out", "cat", "/data/user/0/" + PACKAGE + "/shared_prefs/mixed_config.xml"))
    fresh = json.loads(next(node.text for node in prefs if node.attrib.get("name") == "state"))
    assert all(not source["enabled"] for source in json.loads(http("/api/config", fresh["admin_key"])[1])["backends"])
    assert json.loads(http("/api/accounts/codex", fresh["admin_key"])[1])["status"] == "none"
    wait_for(lambda: any(node.attrib.get("text") == "工作台" for node in tree().iter("node")), 45)
    (OUTPUT / "acceptance.json").write_text(json.dumps({"platform": "Android 15 emulator", "apk": installed, "upgrade_from_version_code": baseline["version_code"], "checks": ["clean release install", "signed in-place upgrade preserves configuration and Keystore account", "console rendered", "sources disabled", "admin isolation", "notification stop", "restart preserves configuration", "Android Keystore encryption and restart read", "account logout"], "provider_login": "not performed", "physical_device": "not tested"}, ensure_ascii=False, indent=2), encoding="utf-8")
    print("ANDROID_DEVICE_SMOKE_PASS")
finally:
    try:
        screenshot("final.png")
    except (OSError, subprocess.SubprocessError):
        pass
