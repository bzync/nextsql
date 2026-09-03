#!/usr/bin/env python3
"""Create a conservative, secret-aware structural profile of a source repository."""
from __future__ import annotations

import argparse
import json
import os
import re
from pathlib import Path
from typing import Any

IGNORE_DIRS = {
    ".git", ".hg", ".svn", "node_modules", "vendor", ".venv", "venv", "env",
    "dist", "build", ".next", ".nuxt", ".svelte-kit", "coverage", ".cache",
    "target", "bin", "obj", "tmp", "temp", "logs", ".terraform", ".idea", ".vscode", ".claude",
    "__pycache__",
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
MAX_FILES = 12000
MAX_READ = 256 * 1024


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
        cpath = Path(current)
        dirs[:] = sorted(
            d for d in dirs
            if d not in IGNORE_DIRS and not is_excluded(cpath / d, excluded)
        )
        for name in sorted(names):
            p = cpath / name
            if p.is_symlink() or not p.is_file() or is_excluded(p, excluded):
                continue
            rp = rel(p, root)
            if SENSITIVE_RE.search(rp):
                continue
            if len(files) >= max_files:
                return files, True
            files.append(p)
    return files, False


def exists(files_set: set[str], *names: str) -> list[str]:
    return [n for n in names if n in files_set]


def suffix_hits(paths: list[str], suffixes: tuple[str, ...], limit: int = 30) -> list[str]:
    out = [p for p in paths if p.lower().endswith(suffixes)]
    return out[:limit]


def contains_path(paths: list[str], needles: tuple[str, ...], limit: int = 30) -> list[str]:
    out = []
    for p in paths:
        lp = p.lower()
        if any(n in lp for n in needles):
            out.append(p)
            if len(out) >= limit:
                break
    return out


def parse_package_json(root: Path) -> dict[str, Any]:
    p = root / "package.json"
    if not p.exists():
        return {}
    try:
        data = json.loads(safe_text(p))
    except json.JSONDecodeError:
        return {"path": "package.json", "parse_error": True}
    deps = {}
    for key in ("dependencies", "devDependencies", "peerDependencies"):
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
        "package_manager": data.get("packageManager"),
        "workspaces": data.get("workspaces"),
        "scripts": safe_scripts,
        "dependencies": sorted(deps.keys()),
    }


def detect_frameworks(pkg: dict[str, Any], files: list[str]) -> list[dict[str, Any]]:
    deps = set(pkg.get("dependencies") or [])
    detections: list[dict[str, Any]] = []
    checks = [
        ("Next.js", "next"), ("React", "react"), ("Vue", "vue"), ("Nuxt", "nuxt"),
        ("Svelte", "svelte"), ("SvelteKit", "@sveltejs/kit"), ("Vite", "vite"),
        ("NestJS", "@nestjs/core"), ("Express", "express"), ("Fastify", "fastify"),
        ("Tailwind CSS", "tailwindcss"), ("Prisma", "prisma"), ("Drizzle", "drizzle-orm"),
        ("Playwright", "@playwright/test"), ("Vitest", "vitest"), ("Jest", "jest"),
    ]
    for label, dep in checks:
        if dep in deps:
            detections.append({"name": label, "evidence": [f"package.json:{dep}"]})
    if any(p.endswith("artisan") or p == "artisan" for p in files):
        detections.append({"name": "Laravel", "evidence": ["artisan"]})
    return detections


def main() -> None:
    ap = argparse.ArgumentParser(description="Scan a repository for risk-reduced structural evidence.")
    ap.add_argument("--root", default=".")
    ap.add_argument("--output", required=True)
    ap.add_argument("--max-files", type=int, default=MAX_FILES)
    ap.add_argument("--exclude", action="append", default=[], help="additional file/directory to exclude")
    args = ap.parse_args()

    root = Path(args.root).resolve()
    if not root.is_dir():
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
    top_dirs = sorted({p.split("/", 1)[0] for p in paths if "/" in p and not p.startswith(".")})[:80]
    top_files = sorted([p for p in paths if "/" not in p])[:120]

    pkg = parse_package_json(root)
    evidence: list[dict[str, Any]] = []
    languages: list[dict[str, Any]] = []

    language_checks = [
        ("TypeScript", (".ts", ".tsx")), ("JavaScript", (".js", ".jsx", ".mjs", ".cjs")),
        ("Go", (".go",)), ("Python", (".py",)), ("PHP", (".php",)), ("Rust", (".rs",)),
        ("Java", (".java",)), ("Kotlin", (".kt", ".kts")), ("C#", (".cs",)),
        ("SQL", (".sql",)), ("Shell", (".sh",)),
    ]
    for name, exts in language_checks:
        hits = suffix_hits(paths, exts, 8)
        if hits:
            languages.append({"name": name, "evidence": hits})

    manifests = exists(fset, "package.json", "pnpm-workspace.yaml", "yarn.lock", "pnpm-lock.yaml", "package-lock.json",
                       "go.mod", "go.work", "pyproject.toml", "requirements.txt", "composer.json", "Cargo.toml",
                       "pom.xml", "build.gradle", "build.gradle.kts")

    infra = {
        "containers": contains_path(paths, ("dockerfile", "containerfile", "docker-compose", "compose.yaml", "compose.yml")),
        "kubernetes": contains_path(paths, ("k8s/", "kubernetes/", "helm/", "charts/", "kustomization.yaml")),
        "terraform": contains_path(paths, (".tf", "terraform/", "opentofu/")),
        "ansible": contains_path(paths, ("ansible/", "playbook", "roles/")),
        "ci": contains_path(paths, (".github/workflows/", ".gitlab-ci", "jenkinsfile", "azure-pipelines")),
        "proxies": contains_path(paths, ("nginx", "haproxy", "caddy")),
    }

    migrations = contains_path(paths, ("migration", "migrations/", "migrate"), 50)
    tests = contains_path(paths, ("test/", "tests/", "__tests__/", ".test.", ".spec.", "e2e/"), 50)
    ui = contains_path(paths, ("components/", "ui/", "design-system", "styles/", "tailwind", "app/", "pages/"), 50)
    docs = [p for p in paths if p.lower().endswith((".md", ".mdx"))][:80]

    readmes = [p for p in paths if Path(p).name.lower().startswith("readme")][:10]
    readme_headings: list[str] = []
    for rp in readmes[:3]:
        text = safe_text(root / rp)
        for line in text.splitlines():
            if line.startswith("#"):
                readme_headings.append(redact_secrets(f"{rp}: {line[:160]}"))
                if len(readme_headings) >= 30:
                    break

    profile = {
        "schema_version": 1,
        "root_name": root.name,
        "scan": {
            "file_count_scanned": len(paths),
            "max_files": args.max_files,
            "truncated": truncated,
            "ignored_directories": sorted(IGNORE_DIRS),
            "sensitive_files_excluded": True,
            "sensitive_paths_excluded": True,
            "symlinks_excluded": True,
            "content_redaction_applied": True,
        },
        "structure": {"top_level_directories": top_dirs, "top_level_files": top_files},
        "manifests": manifests,
        "package_json": pkg,
        "languages": languages,
        "frameworks": detect_frameworks(pkg, paths),
        "architecture_signals": {
            "api_or_server": contains_path(paths, ("server/", "api/", "routes/", "controllers/", "cmd/api", "backend/"), 50),
            "domain_or_services": contains_path(paths, ("domain/", "services/", "service/", "usecases/", "use_cases/", "repositories/"), 50),
            "database_or_migrations": migrations,
            "frontend_or_ui": ui,
            "tests": tests,
            "documentation": docs,
        },
        "infrastructure": infra,
        "documentation_headings": readme_headings,
        "commands": pkg.get("scripts", {}) if pkg else {},
        "evidence_notes": evidence,
    }

    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(profile, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"Wrote project profile: {out}")
    print(f"Scanned files: {len(paths)}")
    if profile["scan"]["truncated"]:
        print(f"WARNING: file scan truncated at {args.max_files} files")


if __name__ == "__main__":
    main()
