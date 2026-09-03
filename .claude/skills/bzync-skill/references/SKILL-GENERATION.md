# Bzync Project Skill Generation

The generated project skill is a repository-specific navigation and constraint layer.

## Required generated sections

A useful project skill should capture:

1. project identity and workspace layout;
2. observed languages/frameworks;
3. application/service boundaries;
4. data and migration locations;
5. API/integration evidence;
6. UI/design-system evidence;
7. infrastructure/deployment evidence;
8. test/quality tooling;
9. documented commands;
10. evidence snapshot and unknowns.

## Do not generate invented architecture

Never infer labels such as:

- microservices;
- clean architecture;
- DDD;
- hexagonal architecture;
- CQRS/event sourcing;

only from folder names. Use those labels only when project evidence supports them.

## Non-destructive behavior

- Do not overwrite an existing generated skill without `--force`.
- Do not modify application code during skill generation.
- Do not ingest secrets.
- Do not delete hand-authored instructions.
- Generate a profile that can be reviewed before skill creation.

## Refresh triggers

Refresh when the repository materially changes:

- framework/runtime migration;
- new application/service;
- database/storage change;
- major domain reorganization;
- new infrastructure topology;
- CI/CD or deployment replacement;
- design-system replacement.

Do not regenerate after every small feature.
