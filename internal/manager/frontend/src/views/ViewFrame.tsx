import type { ReactNode } from "react";
import { Alert, Spinner, Stack } from "@bzync/rui";

// ViewFrame renders the common loading / error / warnings scaffolding around a
// Manager read-model view.
export function ViewFrame({
  loading,
  error,
  warnings,
  children,
}: {
  loading: boolean;
  error: string | null;
  warnings?: string[];
  children: ReactNode;
}) {
  if (loading) return <Spinner />;
  if (error)
    return (
      <Alert variant="error" title="Could not load this view">
        {error}
      </Alert>
    );
  return (
    <Stack gap="lg">
      {warnings?.length ? (
        <Alert variant="warning" title="Some data was unavailable">
          <ul>
            {warnings.map((w, i) => (
              <li key={i}>{w}</li>
            ))}
          </ul>
        </Alert>
      ) : null}
      {children}
    </Stack>
  );
}
