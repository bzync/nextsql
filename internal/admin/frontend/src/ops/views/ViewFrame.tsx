import type { ReactNode } from "react";
import { Alert, List, ListItem, Spinner, Stack } from "@bzync/rui";

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
          <List>
            {warnings.map((w, i) => (
              <ListItem key={i}>{w}</ListItem>
            ))}
          </List>
        </Alert>
      ) : null}
      {children}
    </Stack>
  );
}
