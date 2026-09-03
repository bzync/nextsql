#!/usr/bin/env python3
"""Validate the installed Bzync meta-skill and all bundled resources."""
from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RESOURCE_DIRS = ("references", "scripts", "schemas", "templates", "data", "assets")
FRONTMATTER_RE = re.compile(r"\A---\n(?P<body>.*?)\n---(?:\n|\Z)", re.DOTALL)
RESOURCE_RE = re.compile(r"`((?:references|scripts|schemas|templates|data|assets)/[^`\n]+)`")


def main() -> None:
    errors: list[str] = []
    skill_path = ROOT / "SKILL.md"
    try:
        skill_text = skill_path.read_text(encoding="utf-8")
    except (OSError, UnicodeError) as exc:
        raise SystemExit(f"Cannot read SKILL.md: {exc}") from exc

    match = FRONTMATTER_RE.match(skill_text)
    if not match or not re.search(r"^name:\s*(?:['\"])?bzync-skill(?:['\"])?\s*$", match.group("body"), re.MULTILINE):
        errors.append("SKILL.md has invalid frontmatter or name")
    if not re.search(r"^description:\s*\S", match.group("body") if match else "", re.MULTILINE):
        errors.append("SKILL.md is missing a description")

    resources = sorted(
        path for directory in RESOURCE_DIRS
        for path in (ROOT / directory).rglob("*")
        if path.is_file() and "__pycache__" not in path.parts
    )
    for resource in resources:
        relative = resource.relative_to(ROOT).as_posix()
        if relative not in skill_text:
            errors.append(f"resource is not routed from SKILL.md: {relative}")
        if resource.suffix == ".json":
            try:
                json.loads(resource.read_text(encoding="utf-8"))
            except (OSError, UnicodeError, json.JSONDecodeError) as exc:
                errors.append(f"invalid JSON {relative}: {exc}")
        elif resource.suffix == ".py":
            try:
                source = resource.read_text(encoding="utf-8")
                compile(source, str(resource), "exec")
            except (OSError, UnicodeError, SyntaxError) as exc:
                errors.append(f"invalid Python {relative}: {exc}")

    for reference in RESOURCE_RE.findall(skill_text):
        if not (ROOT / reference).is_file():
            errors.append(f"referenced resource is missing: {reference}")

    if errors:
        print("Bzync skill validation FAILED:")
        for error in errors:
            print(f"- {error}")
        raise SystemExit(1)
    print(f"Bzync skill validation OK ({len(resources)} routed resources)")


if __name__ == "__main__":
    main()
