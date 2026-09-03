---
name: project-skill-creator
description: Inspect the current project codebase and generate or refresh evidence-based Claude project skills that capture architecture, stack, conventions, commands, domain rules, infrastructure, testing, UI system, and change constraints. Use when onboarding Claude to a repository, creating project-specific SKILL.md files, refreshing skills after major architecture changes, or deriving agent instructions from an existing codebase.
---

# Project Skill Creator

Create project-specific guidance from **repository evidence**, not generic assumptions.

## Purpose

A generated project skill should let another Claude session quickly answer:

- What is this project?
- How is it structured?
- Which technology/runtime does it use?
- Where do business rules live?
- How are data, APIs, UI and infrastructure organized?
- Which commands are canonical?
- Which conventions must changes preserve?
- What is known versus inferred?

## Safety and trust rules

1. Never copy secret values into generated skills.
2. Do not scan `.env`, private keys, credential stores, dependency/vendor directories, build outputs or VCS internals.
3. Record **file paths and structural evidence**, not sensitive file contents.
4. Label observations as `Observed` and recommendations/uncertain conclusions as `Inferred`.
5. Do not overwrite existing hand-authored skills unless explicitly requested.
6. Preserve existing project rules; generated skills supplement them.
7. Do not claim commands work unless executed successfully or already documented by the repository.
8. Keep generated guidance concise; point to source files instead of copying large code sections.

## Recommended workflow

From the project root:

```bash
python .claude/skills/project-skill-creator/scripts/scan_project.py \
  --root . \
  --output .claude/project-profile.json
```

Review the profile, then generate:

```bash
python .claude/skills/project-skill-creator/scripts/create_project_skill.py \
  --root . \
  --profile .claude/project-profile.json \
  --output .claude/skills/project-context
```

By default the generator refuses to overwrite an existing `SKILL.md`. Use `--force` only after reviewing the diff.

## What to inspect

### Project identity
- README/project docs;
- repository name;
- monorepo/workspace manifests;
- language/package manifests.

### Software architecture
- top-level application/service/package directories;
- API entrypoints and route definitions;
- domain/service/repository patterns;
- database schemas/migrations;
- events/queues/webhooks;
- shared libraries.

### Frontend/UI
- app/router structure;
- component/design-system locations;
- Tailwind/CSS/theme files;
- state/data-fetching patterns;
- testing tools.

### Infrastructure
- Docker/Containerfile;
- Kubernetes/Helm/Kustomize;
- Terraform/OpenTofu/Ansible/Pulumi;
- Proxmox/VM/bootstrap scripts;
- CI/CD workflows;
- reverse proxy, DNS/TLS, observability configs.

### Quality
- unit/integration/e2e tests;
- lint/typecheck/format commands;
- security/static analysis;
- release/build scripts.

## Generated skill structure

Create:

```text
.claude/skills/project-context/
├── SKILL.md
└── references/
    ├── PROJECT-PROFILE.md
    ├── ARCHITECTURE.md
    ├── COMMANDS.md
    ├── CONVENTIONS.md
    ├── INFRASTRUCTURE.md
    └── EVIDENCE.md
```

The generated `SKILL.md` should instruct Claude to use relevant specialist skills when available:

- `end-to-end-project-engineer`
- `senior-software-engineer`
- `software-architect`
- `infrastructure-engineer`
- `senior-ui-ux-designer`
- `chief-technology-officer`
- domain-specific skills such as the Philippine school-system skills in this package.

## Project-specific precedence

When rules conflict, apply this order unless the repository states otherwise:

1. explicit user instruction for the current task;
2. repository/project instructions and hand-authored local skills;
3. observed codebase conventions and contracts;
4. generated project-context skill;
5. generic specialist skills;
6. generic best practice.

Do not use “best practice” to silently override an intentional project decision.

## Refresh policy

Refresh the project skill after material changes such as:

- new application/service;
- framework/runtime migration;
- database/storage change;
- major infrastructure topology change;
- CI/CD replacement;
- design-system replacement;
- domain restructuring.

Avoid regenerating for every small feature.

See `references/GENERATION-RULES.md` and `references/DETECTION.md`.

Run `scripts/bootstrap_project_skill.py` for the normal workflow. Use `scripts/scan_project.py` and `scripts/create_project_skill.py` separately when the profile should be reviewed before generation. See `references/OUTPUT-EXAMPLE.md` for the generated layout.
