#!/usr/bin/env python3
"""Scan a repository and generate its .claude/skills/bzync-project skill."""
from __future__ import annotations

import argparse
import subprocess
import sys
from pathlib import Path


def main() -> None:
    ap = argparse.ArgumentParser(description="Bootstrap Bzync project context from the current codebase.")
    ap.add_argument("--root", default=".")
    ap.add_argument("--profile", default=".claude/bzync-project-profile.json")
    ap.add_argument("--output", default=".claude/skills/bzync-project")
    ap.add_argument("--max-files", type=int, default=15000)
    ap.add_argument("--force", action="store_true")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    output = Path(args.output)
    if not output.is_absolute():
        output = root / output
    skill_path = output / "SKILL.md"
    if skill_path.exists() and not args.force:
        raise SystemExit(f"Refusing to overwrite existing {skill_path}. Review it or pass --force intentionally.")

    here = Path(__file__).resolve().parent
    scan = here / "scan_codebase.py"
    create = here / "create_bzync_project_skill.py"

    subprocess.run([
        sys.executable, str(scan), "--root", args.root, "--output", args.profile,
        "--max-files", str(args.max_files), "--exclude", args.output,
    ], check=True)

    cmd = [
        sys.executable, str(create), "--root", args.root,
        "--profile", args.profile, "--output", args.output,
    ]
    if args.force:
        cmd.append("--force")
    subprocess.run(cmd, check=True)


if __name__ == "__main__":
    main()
