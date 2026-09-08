// Renders a failed `nextsql setup` plan/install run for a first-run
// operator: a plain-language headline and next step from explainSetupError,
// with the engine's verbatim message always one disclosure click away
// (expanded by default when the rewrite is only the generic fallback).
// Purely presentational — it never changes the outcome or suppresses the
// original text, matching the "actionable errors, technical details
// available separately" installer-UX rule.
import type { CSSProperties } from "react";
import { Alert, CodeBlock, Stack, Text } from "@bzync/rui";
import { explainSetupError } from "../util";

export function SetupErrorAlert({ raw, style }: { raw: string; style?: CSSProperties }) {
  const e = explainSetupError(raw);
  return (
    <Alert variant="error" title={e.title} style={style}>
      <Stack gap="sm">
        <Text>{e.action}</Text>
        <details className="nsi-error-detail" open={e.detailOpen || undefined}>
          <summary>Technical details</summary>
          <CodeBlock code={e.detail} language="text" />
        </details>
      </Stack>
    </Alert>
  );
}
