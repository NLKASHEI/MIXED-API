"""Maintainer-only: regenerate reviewed OpenCode routes from an immutable commit.

Not used by the application or normal build. Review the resulting diff before
committing; the Go core also validates every endpoint against its allowlist.
"""
import concurrent.futures
import json
from pathlib import Path
import re
import sys
import tomllib
import urllib.request

pin = sys.argv[1] if len(sys.argv) == 2 else "f4088b386739663acaf3a88c916d7dfe9ec5efd4"
if not re.fullmatch(r"[0-9a-f]{40}", pin):
    raise SystemExit("Supply a reviewed 40-character commit SHA")
base = f"https://raw.githubusercontent.com/anomalyco/models.dev/{pin}/"


def read(url):
    with urllib.request.urlopen(url, timeout=20) as response:
        return response.read()


provider = tomllib.loads(read(base + "providers/opencode/provider.toml").decode())
files = json.loads(read(f"https://api.github.com/repos/anomalyco/models.dev/contents/providers/opencode/models?ref={pin}"))
protocols = {"@ai-sdk/openai": "responses", "@ai-sdk/openai-compatible": "chat/completions",
             "@ai-sdk/anthropic": "messages", "@ai-sdk/google": "gemini"}


def model_route(file):
    model = tomllib.loads(read(file["download_url"]).decode())
    options = model.get("provider", {})
    protocol = protocols.get(options.get("npm", provider["npm"]))
    if protocol:
        return file["name"].removesuffix(".toml"), {"protocol": protocol, "base": options.get("api", provider["api"])}


with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
    routes = dict(sorted(row for row in pool.map(model_route, (f for f in files if f["name"].endswith(".toml"))) if row))
root = Path(__file__).resolve().parent.parent
(root / "internal/gateway/opencode-models.json").write_text(json.dumps(routes, indent=2) + "\n", encoding="utf-8", newline="\n")
(root / "docs/third-party/models.dev-MIT.txt").write_bytes(read(base + "LICENSE"))
print(f"Generated {len(routes)} routes from {pin}. Update the provenance pin and review the diff.")
