# Codebase Discovery

Use this order before significant implementation.

## Project instructions

Look for:

- `CLAUDE.md`, `AGENTS.md`, `SKILL.md` and `.claude/skills/`;
- `README*`, `PROJECT.md`, `ARCHITECTURE.md`, ADRs and runbooks;
- contribution and release documentation.

## Identity and workspace

Find:

- language/package manifests;
- monorepo/workspace configuration;
- application/service/package roots;
- generated-code boundaries.

## Architecture

Locate:

- entry points and routers;
- application/domain/service layers;
- persistence/repositories/migrations;
- API schemas/contracts;
- event, queue, webhook and background-job paths;
- shared libraries and cross-service dependencies.

## UI/UX

Locate:

- framework/router structure;
- components/design system;
- tokens/themes/CSS/Tailwind configuration;
- data-fetching/state conventions;
- forms, tables, navigation, layout and accessibility utilities;
- frontend tests and visual/story documentation.

## Infrastructure

Locate:

- containers and build images;
- Kubernetes/Helm/Kustomize;
- VM/Proxmox provisioning;
- Terraform/OpenTofu/Ansible;
- proxy/ingress/DNS/TLS configuration;
- CI/CD;
- secrets/config handling;
- storage/database runtime configuration;
- monitoring/logging/backup scripts.

## Quality

Locate:

- unit/integration/e2e tests;
- lint, format and type-check configuration;
- security/static analysis;
- release/build scripts;
- migration verification.

## Output

A project skill should summarize evidence and direct Claude back to the concrete source paths. It should not copy large portions of the repository into prompt files.
