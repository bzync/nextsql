---
name: curriculum-data-modeler
description: Create maintainable SQL/database schemas for Philippine school curricula, learning areas, subjects, competencies, curriculum versions, policy applicability, grading profiles, and historical learner assignment. Use when designing migrations, ERDs, APIs, or data models that must support old and new DepEd/CHED curricula without destructive overwrites.
---

# Curriculum Data Modeler

## Core principle

Curriculum content is versioned master data; enrollment and grades are transactions that reference a resolved version.

## Recommended relational core

```text
education_agencies
policy_issuances
policy_relationships

curricula
curriculum_versions
curriculum_policy_bases

education_levels
grade_levels

learning_areas
subjects
curriculum_subjects

competency_framework_versions
competency_domains
competencies
competency_relations

assessment_policy_versions
rating_scale_versions

student_curriculum_assignments
```

## Avoid

- `subjects` with one mutable name/grade relation used for every year.
- deleting old competencies on curriculum update.
- one `is_current` field as the only version logic.
- deriving historical curriculum solely from current configuration.
- coupling official policy identifiers to auto-increment row IDs.

## Effective dating

Use a combination of immutable version IDs plus applicability periods. Do not use date ranges alone when transition cohorts/pilot schools matter.

## Cohort discriminator

Allow applicability rules such as:

```text
school_year
start/end dates
grade level
admission year
pilot flag
school/program
institution type
```

## Policy provenance

A configurable rule should be traceable to:

```text
source_policy_id
source_section_or_annex
verified_at
reviewed_by (optional)
```

## Migration strategy

When introducing policy versioning to a legacy school database:

1. inventory existing grades/subjects/competencies by SY;
2. derive explicit historical curriculum versions;
3. backfill references without recomputing official grades;
4. lock finalized historical snapshots;
5. enable new-year rules only through a new version;
6. audit ambiguous records rather than guessing.

Read `references/SQL-BLUEPRINT.md` for a schema blueprint.
