# Policy Resolution Algorithm

## 1. Normalize the request

Create a context object:

```json
{
  "agency": "DEPED",
  "school_year": "2026-2027",
  "education_level": "SENIOR_HIGH_SCHOOL",
  "grade_level": 11,
  "program": null,
  "institution_type": "PRIVATE",
  "topic": "grading"
}
```

Unknown values stay unknown. Do not fabricate them.

## 2. Identify candidate policy records

Filter by:

- agency;
- topic/tags;
- grade/program applicability;
- institution scope;
- effective period.

## 3. Traverse relationships

For each candidate, inspect amendments, clarifications, supersession, rescission, and transition links. A newer document may modify only one provision rather than replacing the full source policy.

## 4. Evaluate implementation phase

A curriculum can coexist with a predecessor during phased rollout. Grade level, pilot-school status, and learner cohort may determine which version applies.

## 5. Resolve with confidence

Use confidence states:

- `VERIFIED_OFFICIAL`: official source checked and applicability clear.
- `LIKELY_OFFICIAL_NEEDS_ENCLOSURE_REVIEW`: official issuance identified but detailed provision requires annex/enclosure review.
- `CONFLICT_REQUIRES_REVIEW`: sources or transition rules conflict.
- `INSUFFICIENT_CONTEXT`: missing discriminator prevents one safe answer.

## 6. Persist provenance

For official transactions, store at least:

```text
policy_version_id
curriculum_version_id
rule_set_version
computed_at
calculation_snapshot or immutable inputs
```

This makes historical reproduction possible.

## Example: transition-safe reasoning

A Grade 11 learner and a Grade 12 learner in the same SY can be on different SHS curriculum versions. Never use only `school_year` as the curriculum key when cohort/grade transition matters.
