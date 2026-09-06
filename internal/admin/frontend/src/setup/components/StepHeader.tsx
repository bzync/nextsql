// A consistent title (+ optional description) block for every wizard
// step. @bzync/rui's Heading/Text carry no built-in margin at all (see
// node_modules/@bzync/rui/dist/components/typography.js) — left as bare
// siblings, which most steps originally were, they touch with zero space
// between them. One shared component means every step gets the same
// title-to-description gap by construction, instead of each step file
// re-guessing (or forgetting) its own.
import { Heading, Stack, Text } from "@bzync/rui";
import type { ReactNode } from "react";

export function StepHeader({
  title, description, level = "h2", size = "md",
}: {
  title: ReactNode;
  description?: ReactNode;
  level?: "h1" | "h2";
  size?: "md" | "lg";
}) {
  return (
    <Stack gap="xs">
      <Heading id="installer-step-title" tabIndex={-1} as={level} size={size}>{title}</Heading>
      {description ? <Text variant="muted">{description}</Text> : null}
    </Stack>
  );
}
