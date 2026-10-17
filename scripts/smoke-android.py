"""Clean-emulator acceptance of the CI APK; does not sign in to any provider."""
import json
import pathlib
import re
import subprocess
import time
import urllib.error
import urllib.request
import xml.etree.ElementTree as ET

OUTPUT = pathlib.Path("android-device-evidence")
OUTPUT.mkdir(exist_ok=True)
PACKAGE = "com.mixedapi.local"


def adb(*args, binary=False):
    result = subprocess.run(["adb", *args], check=True, capture_output=True, timeout=30)
    return result.stdout if binary else result.stdout.decode("utf-8")


def http(path, key=""):
    request = urllib.request.Request("http://127.0.0.1:18765" + path)
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
        except (OSError, urllib.error.URLError, subprocess.SubprocessError):
            pass
        time.sleep(1)
    raise AssertionError("Android acceptance condition timed out")


def screenshot(name):
    (OUTPUT / name).write_bytes(adb("exec-out", "screencap", "-p", binary=True))


def tree():
    adb("shell", "uiautomator", "dump", "/sdcard/mixed-ui.xml")
    return ET.fromstring(adb("exec-out", "cat", "/sdcard/mixed-ui.xml"))


def tap(node):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", node.attrib["bounds"]))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))


try:
    adb("install", "-r", "MIXED-API-Android.apk")
    adb("shell", "pm", "grant", PACKAGE, "android.permission.POST_NOTIFICATIONS")
    adb("shell", "am", "start", "-W", "-n", PACKAGE + "/.MainActivity")
    adb("forward", "tcp:18765", "tcp:8765")
    wait_for(lambda: http("/health")[0] == 200)
    # This is a new disposable emulator. Credentials stay in memory, never artifacts/logs.
    prefs = ET.fromstring(adb("exec-out", "run-as", PACKAGE, "cat", "shared_prefs/mixed_config.xml"))
    config = json.loads(next(node.text for node in prefs if node.attrib.get("name") == "state"))
    assert http("/api/config", config["api_key"])[0] == 401
    code, data = http("/api/config", config["admin_key"])
    assert code == 200
    assert all(not source["enabled"] for source in json.loads(data)["backends"])
    wait_for(lambda: any(node.attrib.get("text") == "工作台" for node in tree().iter("node")), 45)
    screenshot("console.png")
    # The provider page must never open during ordinary installation/startup.
    activities = adb("shell", "dumpsys", "activity", "activities")
    assert "/.MainActivity" in activities and "/.GoogleSourceActivity" not in activities
    adb("shell", "cmd", "statusbar", "expand-notifications")
    time.sleep(2)
    for attempt in range(3):
        nodes = list(tree().iter("node"))
        stop = next((node for node in nodes if node.attrib.get("text") == "停止服务"), None)
        if stop is not None:
            screenshot("notification.png")
            tap(stop)
            break
        expand = next((node for node in nodes if node.attrib.get("content-desc", "").lower() in ("expand", "展开")), None)
        if expand is not None:
            tap(expand)
        else:
            adb("shell", "input", "swipe", "180", "150", "180", "550", "400")
        time.sleep(1)
    else:
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
    crash = adb("logcat", "-d", "-s", "AndroidRuntime:E")
    assert "FATAL EXCEPTION" not in crash
    (OUTPUT / "acceptance.json").write_text(json.dumps({"platform": "Android 15 emulator", "apk": "CI artifact", "checks": ["install", "console rendered", "sources disabled", "admin isolation", "notification stop", "restart preserves configuration"], "provider_login": "not performed", "physical_device": "not tested"}, ensure_ascii=False, indent=2), encoding="utf-8")
    print("ANDROID_DEVICE_SMOKE_PASS")
finally:
    try:
        screenshot("final.png")
    except (OSError, subprocess.SubprocessError):
        pass
