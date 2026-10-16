"""Read-only upstream audit. Does not install, log in, or move version pins."""
import argparse
import concurrent.futures
import datetime
import json
import os
from pathlib import Path
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent


def github(repo, suffix):
    headers = {"User-Agent": "MIXED-API-Upstream-Audit", "Accept": "application/vnd.github+json"}
    if os.environ.get("GH_TOKEN"):
        headers["Authorization"] = "Bearer " + os.environ["GH_TOKEN"]
    request = urllib.request.Request("https://api.github.com/repos/" + repo + suffix, headers=headers)
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def inspect(source):
    repo = source["repo"]
    metadata = github(repo, "")
    head = github(repo, "/commits?per_page=1")[0]
    pinned = github(repo, "/commits/" + source["revision"])
    result = {"id": source["id"], "repo": metadata["full_name"], "branch": metadata["default_branch"],
              "pinned": source["revision"], "pinned_commit": pinned["sha"], "head": head["sha"],
              "committed_at": head["commit"]["committer"]["date"],
              "source_ahead": pinned["sha"] != head["sha"]}
    try:
        release = github(repo, "/releases/latest")
        result["release"] = {k: release[k] for k in ["tag_name", "published_at", "html_url"]}
        if source.get("url"):
            asset = next((a for a in release["assets"] if a["browser_download_url"] == source["url"]), None)
            result["pinned_asset_matches_latest"] = bool(asset and asset.get("digest") == "sha256:" + source["sha256"])
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        result["release"] = None
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, help="Write a reviewable JSON snapshot")
    args = parser.parse_args()
    lock = json.loads((ROOT / "internal/gateway/upstreams.json").read_text(encoding="utf-8"))
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        sources = list(pool.map(inspect, lock["sources"]))
    now = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8)))
    snapshot = {"checked_at": now.isoformat(), "sources": sources}
    if args.output:
        args.output.write_text(json.dumps(snapshot, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    for source in sources:
        state = "has unreleased commits" if source["source_ahead"] else "at reviewed HEAD"
        print(f'{source["repo"]}: {state}; latest release: {(source["release"] or {}).get("tag_name", "none")}')


if __name__ == "__main__":
    main()
