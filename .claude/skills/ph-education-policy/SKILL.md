---
name: ph-education-policy
description: Resolve Philippine education policies across historical and current school years. Use for DepEd/CHED curriculum, memoranda/orders, grading, competencies, school forms, report cards, program standards, or school-system logic where the applicable rule depends on time, grade/year, program, agency, institution type, amendments, transition rules, or policy status.
---

# Philippine Education Policy Resolver

You are the policy-resolution layer for Philippine school-system work.

## Non-negotiable rule

**Never assume the newest policy is the applicable policy.**

Before implementing, modifying, or explaining a policy-sensitive feature, resolve:

1. Agency: DepEd, CHED, or another authority.
2. Education sector: basic education, ALS, higher education, etc.
3. School year / academic year.
4. Grade/year level or degree program.
5. Curriculum/program version.
6. Public/private/non-DepEd/HEI context when relevant.
7. Issuance effective date/period.
8. Amendments, clarifications, rescissions, repeals, supersession.
9. Transitional, pilot, or phased implementation rules.
10. Any institution-approved variation explicitly allowed by policy.

If a required discriminator is absent and the code/data can safely support multiple outcomes, design the implementation to be configurable/versioned instead of baking in one guessed answer.

## Authority hierarchy

Prefer evidence in this order:

1. Statute / law and its implementing rules when directly controlling.
2. Official DepEd/CHED issuance and official annex/enclosure.
3. Official curriculum guide / official agency portal.
4. Official regional/division reproduction only when central source is unavailable.
5. Secondary summaries only for discovery; verify against an official source before treating as controlling.

Do not invent issuance numbers, titles, dates, effectivity, or relationships.

## Temporal behavior

For historical records:

- Resolve policy as of the record's school/academic year.
- Do not recalculate historical grades using current formulas unless an explicit migration/recomputation rule authorizes it.
- Preserve the policy/curriculum version and calculation inputs used to produce finalized records.

For “latest/current” requests:

- Verify the official source at task time if web/retrieval access exists.
- Distinguish publication date, effectivity date, pilot period, and full implementation date.
- Report transition populations separately.

## Policy statuses

Use explicit statuses such as:

- `DRAFT`
- `PROPOSED`
- `PILOT`
- `ACTIVE`
- `ACTIVE_WITH_AMENDMENTS`
- `TRANSITIONAL`
- `PARTIALLY_SUPERSEDED`
- `SUPERSEDED`
- `RESCINDED`
- `REPEALED`
- `EXPIRED`
- `UNKNOWN_REQUIRES_VERIFICATION`

Never reduce this to a boolean `latest` flag.

## Relationship vocabulary

Represent relationships explicitly:

- `AMENDS` / `AMENDED_BY`
- `CLARIFIES` / `CLARIFIED_BY`
- `SUPERSEDES` / `SUPERSEDED_BY`
- `RESCINDS` / `RESCINDED_BY`
- `REPEALS` / `REPEALED_BY`
- `IMPLEMENTS`
- `SUPPLEMENTS`
- `REFERENCES`
- `TRANSITIONS_TO`

## Skill routing

Read the matching sibling skill when the task requires it:

- DepEd curriculum/competencies/SHS/ALS → `../deped-curriculum/SKILL.md`
- CHED program/CMO/PSG → `../ched-higher-education/SKILL.md`
- Grading/assessment/promotion/honors → `../school-grading/SKILL.md`
- School forms/report cards/permanent records → `../school-forms-reporting/SKILL.md`
- Curriculum/database schema → `../curriculum-data-modeler/SKILL.md`
- End-to-end SIS design → `../school-system-designer/SKILL.md`
- Updating/researching policies → `../education-policy-researcher/SKILL.md`

## Required implementation posture

When writing application code, migrations, APIs, or business rules:

- Treat policies as versioned configuration/data whenever they can change.
- Store the legal/policy provenance for rules that affect official records.
- Make effective periods explicit.
- Avoid `if year >= 2026` scattered through application code.
- Resolve a policy version once, then execute the rules attached to that version.
- Separate policy definition from student transactions.
- Finalized official records should reference immutable/snapshotted rule versions.

## Output expectations

For policy-sensitive recommendations, include:

- resolved context;
- applicable policy/curriculum version;
- official basis when verified;
- transition/amendment caveats;
- implementation impact;
- verification gaps if any.

Do not claim legal compliance merely because a schema resembles a policy. Flag matters requiring school/agency/legal validation.

## Local data/tools

This skill contains normalized seed data and scripts:

```bash
python scripts/search_policy.py --agency DEPED --topic curriculum
python scripts/resolve_policy.py --agency DEPED --school-year 2026-2027 --grade 11 --topic curriculum
python scripts/validate_data.py
```

Read `references/POLICY-RESOLUTION.md`, `references/SOURCE-RULES.md`, and `references/TERMINOLOGY.md` for deeper rules.

The normalized corpus is `data/policies.jsonl`, with agencies in `data/agencies.json` and curriculum versions in `data/curricula.json`. Their contracts are `schemas/policy.schema.json`, `schemas/policy-relationship.schema.json`, and `schemas/curriculum.schema.json`. Use `scripts/new_policy.py` only to create an unverified template, then complete official-source review before appending it.
