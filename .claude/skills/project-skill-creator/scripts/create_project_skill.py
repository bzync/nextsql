#!/usr/bin/env python3
"""Generate a concise Claude project-context skill from a scan_project.py profile."""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any


def clean_label(value: Any, fallback: str = "project") -> str:
    cleaned = re.sub(r"\s+", " ", str(value)).strip()
    return cleaned[:160] or fallback


def code_span(value: Any) -> str:
    text = str(value).replace("\n", " ").replace("\r", " ")
    longest = max((len(run) for run in re.findall(r"`+", text)), default=0)
    fence = "`" * (longest + 1)
    padding = " " if text.startswith("`") or text.endswith("`") else ""
    return f"{fence}{padding}{text}{padding}{fence}"


def yaml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def bullets(items: list[str], empty: str = "- Unknown / not detected") -> str:
    if not items:
        return empty
    return "\n".join(f"- {code_span(x)}" for x in items)


def named_with_evidence(items: list[dict[str, Any]]) -> str:
    if not items:
        return "- Unknown / not detected"
    lines = []
    for item in items:
        ev = ", ".join(code_span(x) for x in item.get("evidence", [])[:8])
        lines.append(f"- **{item.get('name', 'Unknown')}** — evidence: {ev or 'not recorded'}")
    return "\n".join(lines)


def list_signal(profile: dict[str, Any], section: str, key: str) -> list[str]:
    return list(profile.get(section, {}).get(key, []) or [])


def write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text.rstrip() + "\n", encoding="utf-8")


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    ap.add_argument("--profile", required=True)
    ap.add_argument("--output", default=".claude/skills/project-context")
    ap.add_argument("--force", action="store_true")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    profile_path = Path(args.profile)
    if not profile_path.is_absolute():
        profile_path = root / profile_path
    profile = json.loads(profile_path.read_text(encoding="utf-8"))
    if not isinstance(profile, dict) or profile.get("schema_version") != 1:
        raise SystemExit(f"Unsupported or invalid project profile: {profile_path}")

    out = Path(args.output)
    if not out.is_absolute():
        out = root / out
    if out.resolve() == root:
        raise SystemExit("Output must be a skill directory, not the project root")
    skill_path = out / "SKILL.md"
    if skill_path.exists() and not args.force:
        raise SystemExit(f"Refusing to overwrite existing {skill_path}. Review/diff or pass --force intentionally.")

    project = clean_label(profile.get("root_name") or root.name, root.name)
    description = (
        f"Repository-specific engineering context for {project}, generated from the current codebase. "
        "Use before material changes so implementation follows observed architecture, conventions, "
        "commands, infrastructure, and project constraints."
    )
    skill = f'''---
name: project-context
description: {yaml_string(description)}
---

# {project} Project Context

This skill is generated from repository evidence. It is a navigation and constraint layer, not a substitute for reading the code relevant to the current task.

## Precedence

1. Current user instruction.
2. Hand-authored repository instructions/local skills.
3. Existing code contracts and observed conventions.
4. This generated context.
5. Generic specialist skills/best practices.

## Before substantial changes

Read the relevant files under `references/`, then inspect the concrete source modules affected by the task. Do not assume inferred architecture is authoritative.

## Cross-disciplinary work

Use `end-to-end-project-engineer` for changes spanning multiple layers. Use `senior-software-engineer`, `software-architect`, `infrastructure-engineer`, `senior-ui-ux-designer`, and `chief-technology-officer` as appropriate when those skills exist.

## Evidence discipline

- `Observed` / `Documented` means repository evidence exists.
- `Inferred` is a recommendation or likely conclusion and must be verified before changing contracts.
- Never copy secrets into project instructions.
'''
    write(skill_path, skill)

    structure = profile.get("structure", {})
    profile_md = f'''# Project Profile

**Project:** {code_span(project)}  
**Generated from:** {code_span(profile_path)}  
**Files scanned:** {profile.get('scan', {}).get('file_count_scanned', 'unknown')}  
**Scan truncated:** {profile.get('scan', {}).get('truncated', False)}

## Languages — Observed
{named_with_evidence(profile.get('languages', []))}

## Frameworks / tooling — Observed
{named_with_evidence(profile.get('frameworks', []))}

## Manifests — Observed
{bullets(profile.get('manifests', []))}

## Top-level directories — Observed
{bullets(structure.get('top_level_directories', []))}

## Top-level files — Observed
{bullets(structure.get('top_level_files', []))}

## Documentation headings — Documented
{bullets(profile.get('documentation_headings', []))}
'''
    write(out / "references/PROJECT-PROFILE.md", profile_md)

    a = profile.get("architecture_signals", {})
    architecture_md = f'''# Architecture Evidence

This is a structural inventory, not an automatically asserted architecture style.

## API / server signals — Observed
{bullets(a.get('api_or_server', []))}

## Domain / service signals — Observed
{bullets(a.get('domain_or_services', []))}

## Database / migration signals — Observed
{bullets(a.get('database_or_migrations', []))}

## Frontend / UI signals — Observed
{bullets(a.get('frontend_or_ui', []))}

## Test signals — Observed
{bullets(a.get('tests', []))}

## Working architecture rule

Before moving responsibility across these areas, inspect the relevant source and determine actual ownership, interfaces and dependency direction. Do not label the system microservices, DDD, clean architecture, MVC, etc. unless project evidence supports it.
'''
    write(out / "references/ARCHITECTURE.md", architecture_md)

    commands = profile.get("commands", {}) or {}
    if commands:
        cmd_lines = [f"- {code_span(name)} → {code_span(cmd)}" for name, cmd in sorted(commands.items())]
        cmd_text = "\n".join(cmd_lines)
    else:
        cmd_text = "- No package.json scripts detected. Inspect project docs/manifests before inventing commands."
    commands_md = f'''# Commands

## Documented package scripts
{cmd_text}

## Verification rule

A detected/documented command is not proof it succeeds in the current environment. When a task depends on it, run the smallest relevant verification and report the actual result.
'''
    write(out / "references/COMMANDS.md", commands_md)

    infra = profile.get("infrastructure", {})
    infra_md = f'''# Infrastructure Evidence

## Containers
{bullets(infra.get('containers', []))}

## Kubernetes / orchestration
{bullets(infra.get('kubernetes', []))}

## Terraform / OpenTofu
{bullets(infra.get('terraform', []))}

## Ansible / configuration management
{bullets(infra.get('ansible', []))}

## CI/CD
{bullets(infra.get('ci', []))}

## Proxy / edge indicators
{bullets(infra.get('proxies', []))}

## Rule

Preserve the project's established runtime/deployment path unless the current task intentionally changes it. Verify actual production topology from deployment documentation and configuration before making availability/security claims.
'''
    write(out / "references/INFRASTRUCTURE.md", infra_md)

    conventions_md = '''# Conventions

The scanner intentionally does not invent code-style or architectural conventions from filenames alone.

Before material implementation, inspect nearby code and repository configuration for:

- naming and package/module boundaries;
- error handling;
- validation;
- API serialization;
- data access and transaction patterns;
- migrations;
- tests;
- logging/metrics;
- frontend component/design-system patterns;
- lint/format/type-check configuration.

Prefer local consistency unless there is a concrete reason to improve the convention. If changing a convention, scope and migration impact must be explicit.
'''
    write(out / "references/CONVENTIONS.md", conventions_md)

    evidence = {
        "profile_source": str(profile_path),
        "scan": profile.get("scan", {}),
        "manifests": profile.get("manifests", []),
        "languages": profile.get("languages", []),
        "frameworks": profile.get("frameworks", []),
        "architecture_signals": profile.get("architecture_signals", {}),
        "infrastructure": profile.get("infrastructure", {}),
    }
    evidence_md = "# Evidence Snapshot\n\n```json\n" + json.dumps(evidence, indent=2, ensure_ascii=False) + "\n```\n"
    write(out / "references/EVIDENCE.md", evidence_md)

    print(f"Generated project skill: {out}")
    print("Review the generated references and merge any hand-authored project rules intentionally.")


if __name__ == "__main__":
    main()
