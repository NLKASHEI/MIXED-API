"""Check the APK that will be installed/published, not just Gradle configuration."""
import argparse
import json
import os
import pathlib
import re
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("apk", type=pathlib.Path)
parser.add_argument("--signer-sha256", required=True)
parser.add_argument("--version-code", type=int, required=True)
args = parser.parse_args()
build_tools = pathlib.Path(os.environ["ANDROID_HOME"]) / "build-tools" / "35.0.0"


def run(tool, *arguments):
    suffix = (".bat" if tool == "apksigner" else ".exe") if os.name == "nt" else ""
    return subprocess.check_output([str(build_tools / (tool + suffix)), *map(str, arguments)], encoding="utf-8")


manifest = run("aapt", "dump", "xmltree", args.apk, "AndroidManifest.xml")
debuggable = re.search(r"android:debuggable[^\n]*\(type 0x12\)0x([0-9a-fA-F]+)", manifest)
assert not debuggable or int(debuggable[1], 16) == 0, "Published APK must not be debuggable"
package = re.search(r'A: package="([^"]+)"', manifest)
version = re.search(r"android:versionCode[^\n]*\(type 0x10\)0x([0-9a-fA-F]+)", manifest)
assert package and package[1] == "com.mixedapi.local", "Unexpected APK application ID"
assert version and int(version[1], 16) == args.version_code, "Unexpected APK version code"
signature = run("apksigner", "verify", "--verbose", "--print-certs", args.apk)
digests = re.findall(r"Signer #\d+ certificate SHA-256 digest: ([0-9a-fA-F]+)", signature)
assert len(digests) == 1, "Expected one verified APK signer"
digest = digests[0].lower()
expected = args.signer_sha256.lower().replace(":", "")
assert re.fullmatch(r"[0-9a-f]{64}", expected), "Expected signing certificate SHA-256 is required"
assert digest == expected, "APK signer differs from the expected certificate"
print(json.dumps({"application_id": package[1], "version_code": int(version[1], 16),
                  "debuggable": False, "signer_sha256": digest}))
