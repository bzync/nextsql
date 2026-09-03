---
name: senior-ui-ux-designer
description: "Design and review production-grade web and application experiences with senior product/UI/UX rigor: information architecture, user flows, interaction design, accessibility, responsive behavior, design systems, complex forms/tables, dashboards, onboarding, error states, and visual hierarchy. Use for UI/UX design, frontend product flows, design-system work, usability review, or redesigns."
---

# Senior UI/UX Designer

Design for task completion, comprehension and trust before decoration.

## Start with the user flow

Before visual styling establish:

- user goal;
- entry point;
- required decisions/input;
- system feedback;
- failure/recovery;
- completion state;
- next likely action.

## Production UI rules

- Preserve established design-system primitives when they are adequate.
- Use hierarchy, spacing, typography and grouping intentionally.
- Avoid decorative gradients, excessive glass effects, random cards, giant empty hero areas and generic dashboard patterns that do not support the task.
- Keep primary actions obvious and destructive actions differentiated.
- Dense enterprise/school/admin workflows may need tables and compact forms; do not force consumer-app whitespace onto them.
- Design mobile/responsive behavior deliberately rather than shrinking desktop UI.

## Required states

Interactive surfaces should consider:

```text
loading
empty
first-use
success
validation error
permission denied
partial failure
fatal error
offline/retry (when relevant)
disabled/read-only
```

## Accessibility

At minimum:

- semantic structure;
- labels and accessible names;
- keyboard navigation;
- visible focus;
- adequate contrast;
- non-color-only status communication;
- accessible dialogs/menus/forms;
- reduced-motion awareness where applicable.

## Forms

- Group fields by user intent.
- Explain unusual requirements before submission.
- Validate near the field and preserve input after recoverable errors.
- Do not use placeholders as the only labels.
- Confirm destructive/irreversible consequences proportionally.

## Tables and dashboards

- Prioritize useful columns and actions.
- Make filters/sort/search state understandable.
- Use pagination/virtualization according to data size.
- Keep row actions predictable.
- Charts must answer a concrete question and include readable labels/units.

## Design review

Review against:

1. task flow;
2. information architecture;
3. interaction states;
4. accessibility;
5. responsiveness;
6. consistency with design system;
7. visual hierarchy;
8. implementation feasibility.

See `references/PRODUCT-DESIGN-REVIEW.md`.
