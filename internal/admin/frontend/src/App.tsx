import { useEffect, useState } from "react";
import { Alert, AuthBackdrop, Card, CardBody, Inline, Spinner, Text } from "@bzync/rui";
import { fetchMode, type Mode } from "./shared/mode";
import { App as SetupApp } from "./setup/App";
import { App as OpsApp } from "./ops/App";

type ModeState = { phase: "checking" } | { phase: Mode } | { phase: "error"; message: string };

// NextSQL Admin picks one mode at startup (no initialized install found ->
// Setup; an initialized install found -> Operations) and never shows both at
// once — Setup and Operations are sequential lifecycle phases of the same
// install, not simultaneous UI states. See internal/admin/server.go's
// GET /api/v1/mode.
export function App() {
  const [state, setState] = useState<ModeState>({ phase: "checking" });

  useEffect(() => {
    fetchMode()
      .then((res) => setState({ phase: res.mode }))
      .catch((err) => setState({ phase: "error", message: err instanceof Error ? err.message : String(err) }));
  }, []);

  if (state.phase === "checking") {
    return (
      <>
        <AuthBackdrop aria-hidden="true" />
        <main className="nsa-loading-page bg-bg text-foreground">
          <Card variant="elevated" role="status" aria-live="polite">
            <CardBody>
              <Inline gap="sm" align="center">
                <Spinner size="sm" />
                <Text variant="muted">Connecting to NextSQL Admin…</Text>
              </Inline>
            </CardBody>
          </Card>
        </main>
      </>
    );
  }

  if (state.phase === "error") {
    return (
      <>
        <AuthBackdrop aria-hidden="true" />
        <main className="nsa-loading-page bg-bg text-foreground">
          <Card variant="elevated">
            <CardBody>
              <Alert variant="error" title="Could not reach NextSQL Admin">{state.message}</Alert>
            </CardBody>
          </Card>
        </main>
      </>
    );
  }

  if (state.phase === "setup") return <SetupApp />;
  return <OpsApp />;
}
