# Project Skill Generation Rules

## Evidence levels

Use these labels:

- **Observed**: directly supported by repository structure/content.
- **Documented**: explicitly stated in a project document/manifest.
- **Inferred**: likely based on evidence but not guaranteed.
- **Unknown**: not enough evidence; do not guess.

## Good generated instruction

> Observed: API entrypoint is `server/cmd/api`. Keep HTTP transport thin and place business rules in existing service/domain packages. Evidence: `server/cmd/api`, `server/internal/...`.

## Bad generated instruction

> This project uses clean architecture and all services must follow DDD.

unless the repository actually establishes that rule.

## Secrets

Never copy values from files matching or resembling:

```text
.env*
*.pem
*.key
*.p12
*.pfx
id_rsa*
credentials*
secrets*
```

The scanner intentionally ignores these.

## Size

Keep `SKILL.md` concise. Put inventories and evidence in `references/`.

## Updating existing skills

Default: fail if the destination already contains `SKILL.md`.

Preferred update workflow:

1. generate to a temporary destination;
2. diff against current skill;
3. merge intentional new observations;
4. retain hand-authored constraints;
5. remove stale observations only when evidence confirms they are stale.
