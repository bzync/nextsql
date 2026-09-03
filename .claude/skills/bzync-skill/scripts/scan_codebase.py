#!/usr/bin/env python3
"""Secret-aware structural scanner for generating Bzync repository-specific Claude skills."""
from __future__ import annotations

import argparse
import json
import os
import re
from collections import Counter
from pathlib import Path
from typing import Any

IGNORE_DIRS = {
    ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv", "env",
    "dist", "build", ".next", ".nuxt", ".svelte-kit", "coverage", ".cache",
    "target", "bin", "obj", "tmp", "temp", "logs", ".terraform", ".idea",
    ".vscode", ".claude", "__pycache__",
}
SENSITIVE_RE = re.compile(
    r"(^|/)(\.env(?:\.[^/]*)?|.*\.(?:pem|key|p12|pfx|jks|keystore)$|"
    r"id_(?:rsa|dsa|ecdsa|ed25519)(?:\.pub)?|"
    r"credentials?(?:\.[^/]*)?|secrets?(?:\.[^/]*)?|\.npmrc|\.pypirc)(?:/|$)",
    re.IGNORECASE,
)
SECRET_ASSIGNMENT_RE = re.compile(
    r"(?i)\b(password|passwd|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)"
    r"(\s*[:=]\s*)([^\s,;]+)"
)
TOKEN_RE = re.compile(r"\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,})\b")
MAX_FILES_DEFAULT = 15000
MAX_READ = 384 * 1024


def rel(path: Path, root: Path) -> str:
    return path.relative_to(root).as_posix()


def safe_text(path: Path) -> str:
    try:
        if path.is_symlink() or not path.is_file():
            return ""
        if path.stat().st_size > MAX_READ:
            return ""
        raw = path.read_bytes()
        if b"\x00" in raw[:8192]:
            return ""
        return raw.decode("utf-8", errors="replace")
    except OSError:
        return ""


def redact_secrets(value: str) -> str:
    value = SECRET_ASSIGNMENT_RE.sub(lambda m: f"{m.group(1)}{m.group(2)}<redacted>", value)
    return TOKEN_RE.sub("<redacted-token>", value)


def is_excluded(path: Path, excluded: tuple[Path, ...]) -> bool:
    absolute = path.absolute()
    return any(absolute == target or target in absolute.parents for target in excluded)


def walk_files(root: Path, max_files: int, excluded: tuple[Path, ...]) -> tuple[list[Path], bool]:
    files: list[Path] = []
    for current, dirs, names in os.walk(root):
        current_path = Path(current)
        dirs[:] = sorted(
            d for d in dirs
            if d not in IGNORE_DIRS and not is_excluded(current_path / d, excluded)
        )
        for name in sorted(names):
            p = current_path / name
            if p.is_symlink() or not p.is_file() or is_excluded(p, excluded):
                continue
            rp = rel(p, root)
            if SENSITIVE_RE.search(rp):
                continue
            if len(files) >= max_files:
                return files, True
            files.append(p)
    return files, False


def contains(paths: list[str], needles: tuple[str, ...], limit: int = 80) -> list[str]:
    out: list[str] = []
    for p in paths:
        lp = p.lower()
        if any(n in lp for n in needles):
            out.append(p)
            if len(out) >= limit:
                break
    return out


def suffix(paths: list[str], suffixes: tuple[str, ...], limit: int = 20) -> list[str]:
    return [p for p in paths if p.lower().endswith(suffixes)][:limit]


def load_json(path: Path) -> dict[str, Any]:
    try:
        data = json.loads(safe_text(path))
        return data if isinstance(data, dict) else {}
    except json.JSONDecodeError:
        return {}


def package_profile(root: Path) -> dict[str, Any]:
    p = root / "package.json"
    if not p.exists():
        return {}
    data = load_json(p)
    deps: dict[str, str] = {}
    for key in ("dependencies", "devDependencies", "peerDependencies", "optionalDependencies"):
        if isinstance(data.get(key), dict):
            deps.update(data[key])
    scripts = data.get("scripts", {})
    safe_scripts = {
        str(name): redact_secrets(value)
        for name, value in scripts.items()
        if isinstance(name, str) and isinstance(value, str)
    } if isinstance(scripts, dict) else {}
    return {
        "path": "package.json",
        "name": data.get("name"),
        "private": data.get("private"),
        "type": data.get("type"),
        "package_manager": data.get("packageManager"),
        "workspaces": data.get("workspaces"),
        "scripts": safe_scripts,
        "dependencies": sorted(deps),
    }


def detect_frameworks(pkg: dict[str, Any], paths: list[str]) -> list[dict[str, Any]]:
    deps = set(pkg.get("dependencies") or [])
    checks = [
        ("Next.js", "next"), ("React", "react"), ("Vue", "vue"), ("Nuxt", "nuxt"),
        ("Svelte", "svelte"), ("SvelteKit", "@sveltejs/kit"), ("Vite", "vite"),
        ("NestJS", "@nestjs/core"), ("Express", "express"), ("Fastify", "fastify"),
        ("Tailwind CSS", "tailwindcss"), ("Prisma", "prisma"), ("Drizzle", "drizzle-orm"),
        ("Playwright", "@playwright/test"), ("Vitest", "vitest"), ("Jest", "jest"),
        ("Storybook", "storybook"), ("TanStack Query", "@tanstack/react-query"),
    ]
    found: list[dict[str, Any]] = []
    for label, dep in checks:
        if dep in deps:
            found.append({"name": label, "evidence": [f"package.json dependency: {dep}"]})
    if "artisan" in paths:
        found.append({"name": "Laravel", "evidence": ["artisan"]})
    if "manage.py" in paths:
        found.append({"name": "Django/Python web project signal", "evidence": ["manage.py"]})
    return found


def file_extension_counts(paths: list[str]) -> list[dict[str, Any]]:
    counts: Counter[str] = Counter()
    for p in paths:
        ext = Path(p).suffix.lower()
        if ext:
            counts[ext] += 1
    return [{"extension": ext, "count": count} for ext, count in counts.most_common(20)]


def main() -> None:
    ap = argparse.ArgumentParser(description="Scan a repository for risk-reduced structural evidence.")
    ap.add_argument("--root", default=".")
    ap.add_argument("--output", default=".claude/bzync-project-profile.json")
    ap.add_argument("--max-files", type=int, default=MAX_FILES_DEFAULT)
    ap.add_argument("--exclude", action="append", default=[], help="additional file/directory to exclude")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    if not root.exists() or not root.is_dir():
        raise SystemExit(f"Project root is not a directory: {root}")
    if args.max_files < 1:
        raise SystemExit("--max-files must be at least 1")

    out = Path(args.output)
    if not out.is_absolute():
        out = root / out
    out = out.resolve()
    excluded = tuple(
        (Path(value) if Path(value).is_absolute() else root / value).resolve()
        for value in args.exclude
    ) + (out,)
    files, truncated = walk_files(root, args.max_files, excluded)
    paths = sorted(rel(p, root) for p in files)
    fset = set(paths)
    pkg = package_profile(root)

    language_checks = [
        ("TypeScript", (".ts", ".tsx")), ("JavaScript", (".js", ".jsx", ".mjs", ".cjs")),
        ("Go", (".go",)), ("Python", (".py",)), ("PHP", (".php",)), ("Rust", (".rs",)),
        ("Java", (".java",)), ("Kotlin", (".kt", ".kts")), ("C#", (".cs",)),
        ("SQL", (".sql",)), ("Shell", (".sh",)), ("Ruby", (".rb",)),
    ]
    languages: list[dict[str, Any]] = []
    for name, exts in language_checks:
        hits = suffix(paths, exts, 10)
        if hits:
            languages.append({"name": name, "evidence": hits})

    manifests = [p for p in (
        "package.json", "pnpm-workspace.yaml", "yarn.lock", "pnpm-lock.yaml", "package-lock.json",
        "bun.lock", "bun.lockb", "go.mod", "go.work", "pyproject.toml", "requirements.txt",
        "composer.json", "Cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts",
        "Gemfile", "Makefile", "Taskfile.yml", "Taskfile.yaml",
    ) if p in fset]

    instruction_files = [p for p in paths if Path(p).name.lower() in {
        "claude.md", "agents.md", "project.md", "architecture.md", "contributing.md", "skills.md"
    }][:80]
    docs = [p for p in paths if p.lower().endswith((".md", ".mdx"))][:120]

    profile = {
        "schema_version": 1,
        "generator": "bzync-skill",
        "company": "Bzync Software Development Services",
        "root_name": root.name,
        "scan": {
            "file_count_scanned": len(paths),
            "max_files": args.max_files,
            "truncated": truncated,
            "ignored_directories": sorted(IGNORE_DIRS),
            "sensitive_paths_excluded": True,
            "symlinks_excluded": True,
            "content_redaction_applied": True,
        },
        "structure": {
            "top_level_directories": sorted({p.split("/", 1)[0] for p in paths if "/" in p and not p.startswith(".")})[:100],
            "top_level_files": sorted([p for p in paths if "/" not in p])[:160],
        },
        "instructions": instruction_files,
        "documentation": docs,
        "manifests": manifests,
        "package_json": pkg,
        "languages": languages,
        "extension_counts": file_extension_counts(paths),
        "frameworks": detect_frameworks(pkg, paths),
        "software": {
            "server_api": contains(paths, ("server/", "backend/", "api/", "routes/", "controllers/", "cmd/api", "handlers/")),
            "domain_services": contains(paths, ("domain/", "services/", "service/", "usecases/", "use_cases/", "repositories/", "repository/")),
            "database_migrations": contains(paths, ("database/", "db/", "migration", "migrations/", "schema.sql", "prisma/", "drizzle/")),
            "integrations_jobs": contains(paths, ("webhook", "integrations/", "jobs/", "workers/", "queue", "events/")),
        },
        "ui_ux": {
            "frontend": contains(paths, ("client/", "frontend/", "app/", "pages/", "src/app/", "src/pages/")),
            "components": contains(paths, ("components/", "ui/", "design-system", "design_system")),
            "styles_theme": contains(paths, ("styles/", "theme", "tailwind", "tokens", "globals.css", "global.css")),
            "storybook_docs": contains(paths, ("storybook", ".stories.", "stories/")),
        },
        "infrastructure": {
            "containers": contains(paths, ("dockerfile", "containerfile", "docker-compose", "compose.yaml", "compose.yml")),
            "kubernetes": contains(paths, ("k8s/", "kubernetes/", "helm/", "charts/", "kustomization.yaml")),
            "terraform_opentofu": contains(paths, (".tf", "terraform/", "opentofu/")),
            "ansible": contains(paths, ("ansible/", "playbook", "roles/")),
            "proxmox_vm": contains(paths, ("proxmox", "cloud-init", "cloud_init", "vm/", "lxc/")),
            "ci_cd": contains(paths, (".github/workflows/", ".gitlab-ci", "jenkinsfile", "azure-pipelines", "ci/", "cd/")),
            "edge_proxy": contains(paths, ("nginx", "haproxy", "caddy", "traefik", "ingress")),
            "observability": contains(paths, ("prometheus", "grafana", "otel", "opentelemetry", "loki", "monitor", "observability")),
            "backup_restore": contains(paths, ("backup", "restore", "snapshot")),
        },
        "quality": {
            "tests": contains(paths, ("test/", "tests/", "__tests__/", ".test.", ".spec.", "e2e/"), 120),
            "lint_format_typecheck": contains(paths, ("eslint", "prettier", "biome", "golangci", "ruff", "mypy", "phpstan", "pint", "tsconfig")),
            "security": contains(paths, ("security", "semgrep", "codeql", "trivy", "dependabot", "renovate")),
        },
        "commands": pkg.get("scripts", {}) if pkg else {},
    }

    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(profile, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"Bzync codebase profile written: {out}")
    print(f"Files scanned: {len(paths)}")
    if profile["scan"]["truncated"]:
        print(f"WARNING: scan truncated at {args.max_files} files")


if __name__ == "__main__":
    main()
