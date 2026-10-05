#!/usr/bin/env python3
"""Check or sync the reviewed Terraform fork without fetching remote code."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
DEST = ROOT / "third_party/terraform-provider-logto"
MANIFEST = DEST / "snapshot.json"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_files(source):
    files = [source / name for name in ("LICENSE", "go.mod", "go.sum", "main.go", "Makefile", "provider_code_spec.json")]
    for directory in ("client", "internal", "provider", "scripts", "docs", "config"):
        files.extend(p for p in (source / directory).rglob("*") if p.is_file() and p.suffix in (".go", ".md", ".json", ".yml", ".py"))
    return {p.relative_to(source).as_posix(): p for p in files}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sync", action="store_true")
    parser.add_argument("--source", type=Path, default=ROOT.parent / "terraform-provider-logto")
    args = parser.parse_args()
    if args.sync:
        source = args.source.resolve()
        if not source.is_relative_to(ROOT.parent) or not source.is_dir():
            parser.error("source must be a repository inside this workspace")
        files = source_files(source)
        if MANIFEST.exists():
            stale = set(json.loads(MANIFEST.read_text())["files"]) - files.keys()
            if stale:
                parser.error("snapshot contains stale paths; review them manually: " + ", ".join(sorted(stale)))
        for name, path in sorted(files.items()):
            target = DEST / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(path, target)
        base_commit = json.loads(MANIFEST.read_text())["base_commit"] if MANIFEST.exists() else subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip()
        manifest = {"upstream": "https://github.com/Lenstra/terraform-provider-logto", "base_commit": base_commit, "source_repository": "https://github.com/the-ccsn/terraform-provider-logto", "source_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=source, text=True).strip(), "files": {name: digest(path) for name, path in sorted(files.items())}}
        manifest["source_dirty"] = bool(subprocess.check_output(["git", "status", "--porcelain", "--untracked-files=all"], cwd=source, text=True).strip())
        MANIFEST.write_text(json.dumps(manifest, indent=2) + "\n")
    manifest = json.loads(MANIFEST.read_text())
    actual = {p.relative_to(DEST).as_posix() for p in DEST.rglob("*") if p.is_file() and p.name != "snapshot.json" and ".work" not in p.parts and p.name != "coverage.out"}
    if actual != set(manifest["files"]):
        raise SystemExit("snapshot file set does not match manifest")
    for name, expected in manifest["files"].items():
        path = DEST / name
        if not path.is_file() or digest(path) != expected:
            raise SystemExit("snapshot checksum mismatch: " + name)
    print(f"Verified {len(manifest['files'])} Terraform provider source files")


if __name__ == "__main__":
    main()
