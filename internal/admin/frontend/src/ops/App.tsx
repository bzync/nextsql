import { useCallback, useEffect, useState } from "react";
import { AuthBackdrop, Card, CardBody, Inline, Spinner, Text } from "@bzync/rui";
import { api, setCsrf, type Whoami } from "./api";
import { Login } from "./Login";
import { Shell } from "./Shell";

// serial counts sign-ins in this tab. A server switch is a new session, so
// the Shell is keyed on it and remounts from scratch — every Operations view
// and the Studio workspace re-read against the new server.
type AuthState = { phase: "checking" } | { phase: "out" } | { phase: "in"; who: Whoami; serial: number };

export function App() {
  const [auth, setAuth] = useState<AuthState>({ phase: "checking" });

  const signedIn = useCallback((who: Whoami) => {
    setCsrf(who.csrf_token);
    setAuth((current) => ({ phase: "in", who, serial: (current.phase === "in" ? current.serial : 0) + 1 }));
  }, []);

  const signOut = useCallback(() => {
    api.logout().catch(() => undefined);
    setCsrf(null);
    setAuth({ phase: "out" });
  }, []);

  useEffect(() => {
    api
      .whoami()
      .then((who) => signedIn(who))
      .catch(() => setAuth({ phase: "out" }));
  }, [signedIn]);

  if (auth.phase === "checking") return (
    <>
      <AuthBackdrop aria-hidden="true" />
      <main className="nsm-login-page bg-bg text-foreground">
        <Card variant="bordered" role="status" aria-live="polite">
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
  if (auth.phase === "out") return <Login onSignedIn={signedIn} />;
  return <Shell key={auth.serial} who={auth.who} onSignOut={signOut} onSwitched={signedIn} />;
}
