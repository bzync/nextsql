#!/usr/bin/env python3
"""Generate a repository-specific Bzync Claude skill from a risk-reduced structural profile."""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path
from typing import Any


def write(path: Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text.rstrip() + "\n", encoding="utf-8")


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
    return "\n".join(f"- {code_span(x)}" for x in items) if items else empty


def named(items: list[dict[str, Any]]) -> str:
    if not items:
        return "- Unknown / not detected"
    lines: list[str] = []
    for item in items:
        evidence = ", ".join(code_span(x) for x in item.get("evidence", [])[:8]) or "not recorded"
        lines.append(f"- **{item.get('name', 'Unknown')}** — evidence: {evidence}")
    return "\n".join(lines)


def slugify(value: str) -> str:
    s = re.sub(r"[^a-z0-9]+", "-", value.lower()).strip("-")
    return s or "project"


def section(title: str, values: list[str]) -> str:
    return f"## {title}\n{bullets(values)}\n"


def main() -> None:
    ap = argparse.ArgumentParser(description="Generate .claude/skills/bzync-project from a Bzync codebase profile.")
    ap.add_argument("--root", default=".")
    ap.add_argument("--profile", default=".claude/bzync-project-profile.json")
    ap.add_argument("--output", default=".claude/skills/bzync-project")
    ap.add_argument("--force", action="store_true")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    profile_path = Path(args.profile)
    if not profile_path.is_absolute():
        profile_path = root / profile_path
    profile = json.loads(profile_path.read_text(encoding="utf-8"))
    if (
        not isinstance(profile, dict)
        or profile.get("schema_version") != 1
        or profile.get("generator") != "bzync-skill"
    ):
        raise SystemExit(f"Unsupported or invalid Bzync project profile: {profile_path}")

    out = Path(args.output)
    if not out.is_absolute():
        out = root / out
    if out.resolve() == root:
        raise SystemExit("Output must be a skill directory, not the project root")
    skill_path = out / "SKILL.md"
    if skill_path.exists() and not args.force:
        raise SystemExit(f"Refusing to overwrite existing {skill_path}. Review it or pass --force intentionally.")

    project = clean_label(profile.get("root_name") or root.name, root.name)
    project_slug = slugify(project)
    description = (
        f"Repository-specific Bzync Software Development Services engineering context for {project}. "
        "Generated from codebase evidence; use before material implementation, architecture, infrastructure, "
        "UI/UX, product, testing, security, privacy, compliance, or CTO-level decisions in this project."
    )

    skill = f'''---
name: bzync-project
description: {yaml_string(description)}
---

# {project} — Bzync Project Context

This skill is generated from risk-reduced structural repository evidence. Review the output before committing it. It is a navigation and constraint layer, not a replacement for reading the source files affected by the current task.

## Project identity

- Project: {code_span(project)}
- Normalized slug: {code_span(project_slug)}
- Engineering organization: **Bzync Software Development Services**

## Precedence

1. Current user instruction.
2. Hand-authored project instructions and local skills.
3. Existing source/data/API/runtime contracts.
4. This generated project context.
5. `bzync-skill` and specialist engineering skills.
6. Generic best practices.

## Before substantial changes

Read the relevant files in `references/`, then inspect the concrete implementation. Never treat an inferred pattern as authoritative without verification.

## End-to-end engineering

For cross-cutting work use `bzync-end-to-end-engineer` or `end-to-end-project-engineer` and apply the senior software engineering, architecture, infrastructure, UI/UX, CTO, product competency, testing, security and privacy lenses that match the change.

## Change discipline

- Preserve existing capabilities unless replacement/removal is intentional.
- Keep migrations and API changes backwards-compatible when practical.
- Do not copy secrets into source or generated documentation.
- Do not introduce a second architecture/deployment/design system without a concrete requirement.
- Verify important assumptions against the repository before implementation.

## References

- `references/PROJECT-SNAPSHOT.md`
- `references/ARCHITECTURE.md`
- `references/SOFTWARE-ENGINEERING.md`
- `references/INFRASTRUCTURE.md`
- `references/UI-UX.md`
- `references/QUALITY-AND-TESTING.md`
- `references/COMMANDS.md`
- `references/DELIVERY.md`
- `references/EVIDENCE.json`
'''
    write(skill_path, skill)

    structure = profile.get("structure", {})
    snapshot = f'''# Project Snapshot

**Project:** {code_span(project)}  
**Generated profile:** {code_span(profile_path)}  
**Files scanned:** {profile.get('scan', {}).get('file_count_scanned', 'unknown')}  
**Scan truncated:** {profile.get('scan', {}).get('truncated', False)}

## Languages — Observed
{named(profile.get('languages', []))}

## Frameworks / major tooling — Observed
{named(profile.get('frameworks', []))}

## Manifests — Observed
{bullets(profile.get('manifests', []))}

## Repository instructions — Observed
{bullets(profile.get('instructions', []))}

## Top-level directories — Observed
{bullets(structure.get('top_level_directories', []))}

## Top-level files — Observed
{bullets(structure.get('top_level_files', []))}

## Rule

This snapshot describes structural evidence only. Read relevant source and hand-authored project documentation before changing contracts or architecture.
'''
    write(out / "references/PROJECT-SNAPSHOT.md", snapshot)

    sw = profile.get("software", {})
    architecture = f'''# Architecture Evidence

Do not automatically label an architecture style from these paths.

{section('Server / API signals — Observed', sw.get('server_api', []))}
{section('Domain / service signals — Observed', sw.get('domain_services', []))}
{section('Database / migration signals — Observed', sw.get('database_migrations', []))}
{section('Integration / job signals — Observed', sw.get('integrations_jobs', []))}
## Architecture working rule

Determine actual ownership, dependency direction, transaction boundaries and external contracts from source before moving responsibilities. Use `software-architect` for material boundary decisions.
'''
    write(out / "references/ARCHITECTURE.md", architecture)

    software = f'''# Software Engineering Context

## Observed server/API areas
{bullets(sw.get('server_api', []))}

## Observed domain/service areas
{bullets(sw.get('domain_services', []))}

## Observed database/migrations
{bullets(sw.get('database_migrations', []))}

## Project implementation rules

- Inspect nearby code before introducing new patterns.
- Preserve public contracts unless the task intentionally changes them.
- Keep validation and authorization on authoritative server/domain paths.
- Make schema/data migrations explicit and deterministic.
- Add regression protection for changed business behavior.
- Avoid broad unrelated refactoring.

Use `senior-software-engineer` for deep implementation/review tasks.
'''
    write(out / "references/SOFTWARE-ENGINEERING.md", software)

    infra = profile.get("infrastructure", {})
    infra_md = "# Infrastructure Evidence\n\n" + "\n".join([
        section("Containers", infra.get("containers", [])),
        section("Kubernetes / orchestration", infra.get("kubernetes", [])),
        section("Terraform / OpenTofu", infra.get("terraform_opentofu", [])),
        section("Ansible", infra.get("ansible", [])),
        section("Proxmox / VM signals", infra.get("proxmox_vm", [])),
        section("CI/CD", infra.get("ci_cd", [])),
        section("Edge / proxy", infra.get("edge_proxy", [])),
        section("Observability", infra.get("observability", [])),
        section("Backup / restore", infra.get("backup_restore", [])),
    ]) + '''\n## Infrastructure rule\n\nVerify the actual production topology before making availability, network or security claims. Preserve the established deployment path unless a change is intentional. Use `infrastructure-engineer` for material runtime changes.\n'''
    write(out / "references/INFRASTRUCTURE.md", infra_md)

    ui = profile.get("ui_ux", {})
    ui_md = f'''# UI/UX Evidence

{section('Frontend/router signals — Observed', ui.get('frontend', []))}
{section('Components/design-system signals — Observed', ui.get('components', []))}
{section('Styles/theme/tokens — Observed', ui.get('styles_theme', []))}
{section('Storybook/design documentation — Observed', ui.get('storybook_docs', []))}
## UI/UX working rule

Follow the project's existing component system, interaction conventions, typography, spacing and responsive model where they are adequate. Avoid generic AI-slop visual patterns. Design loading, empty, error, success, permission and responsive states deliberately. Use `senior-ui-ux-designer` for product/design changes.
'''
    write(out / "references/UI-UX.md", ui_md)

    q = profile.get("quality", {})
    quality_md = f'''# Quality and Testing Evidence

{section('Tests — Observed', q.get('tests', []))}
{section('Lint / format / type-check — Observed', q.get('lint_format_typecheck', []))}
{section('Security tooling — Observed', q.get('security', []))}
## Verification rule

Detected files are evidence that tooling exists, not proof that it passes. Run the smallest relevant checks for the task and report actual outcomes. Do not claim verification that was not performed.

Use `software-testing-skill` for material test strategy, `software-security-skill` / `web-security-skill` for security-sensitive changes, `cybersecurity-skill` for runtime/organizational risk, and the applicable privacy skill whenever personal data changes.
'''
    write(out / "references/QUALITY-AND-TESTING.md", quality_md)

    commands = profile.get("commands", {}) or {}
    if commands:
        command_lines = "\n".join(f"- {code_span(name)} -> {code_span(cmd)}" for name, cmd in sorted(commands.items()))
    else:
        command_lines = "- No package.json scripts detected. Read project manifests/docs before inventing commands."
    write(out / "references/COMMANDS.md", f'''# Documented Commands

{command_lines}

## Rule

A documented command is not proof it succeeds in the current environment. Execute relevant verification when the task depends on it.
''')

    write(out / "references/DELIVERY.md", '''# Bzync Delivery Expectations

For substantial changes evaluate the complete path:

```text
current repository evidence
 -> architecture/data/API
 -> user flow/UI
 -> implementation
 -> tests + software/web security + privacy checks
 -> deployment/runtime + cyber/resilience checks
 -> observability/rollback
```

A production-ready change should preserve data integrity, enforce permissions, handle failure states, remain deployable, be observable, have proportionate automated/manual test evidence, address security/privacy risks, and have a safe migration/rollback story appropriate to its risk.

Use `bzync-end-to-end-engineer` for cross-cutting work and `chief-technology-officer` for high-impact strategy/cost/risk decisions.
''')

    evidence = dict(profile)
    evidence["generated_skill"] = {
        "name": "bzync-project",
        "project": project,
        "output": str(out),
    }
    write(out / "references/EVIDENCE.json", json.dumps(evidence, indent=2, ensure_ascii=False))

    print(f"Generated Bzync project skill: {out}")
    print("Review generated evidence and merge hand-authored rules deliberately.")


if __name__ == "__main__":
    main()
