#!/usr/bin/env python3
"""Keep Terraform and OpenTofu schema exports identical for code generation."""

import argparse
import json
from pathlib import Path


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("schema", type=Path)
    args = parser.parse_args()
    document = json.loads(args.schema.read_text(encoding="utf-8"))
    providers = document.get("provider_schemas", {})
    canonical = "registry.terraform.io/lenstra/logto"
    aliases = {canonical, "registry.opentofu.org/lenstra/logto"}
    if len(providers) != 1 or next(iter(providers)) not in aliases:
        parser.error("expected exactly one Lenstra/logto provider schema")
    document["provider_schemas"] = {canonical: next(iter(providers.values()))}
    args.schema.write_text(json.dumps(document, ensure_ascii=False, separators=(",", ":")) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
