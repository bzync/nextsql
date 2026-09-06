import { useCallback, useEffect, useState } from "react";
import { AuthBackdrop, Card, CardBody, Inline, Spinner, Text } from "@bzync/rui";
import { api, setCsrf, type Whoami } from "./api";
import { Login } from "./Login";
import { Shell } from "./Shell";

type AuthState = { phase: "checking" } | { phase: "out" } | { phase: "in"; who: Whoami };

export function App() {
  const [auth, setAuth] = useState<AuthState>({ phase: "checking" });

  const signedIn = useCallback((who: Whoami) => {
    setCsrf(who.csrf_token);
    setAuth({ phase: "in", who });
  }, []);

  // A Studio reconnect changes the session's realm/database in place (same
  // session id, same CSRF token). Merge those two fields so every consumer of
  // `who` — localStorage scoping keys, the identity label, the Studio remount
  // key — follows the live connection.
  const connectionChanged = useCallback((next: { realm: string; database: string }) => {
    setAuth((current) =>
      current.phase === "in"
        ? { phase: "in", who: { ...current.who, realm: next.realm, database: next.database } }
        : current,
    );
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
  if (auth.phase === "out") return <Login onSignedIn={signedIn} />;
  return <Shell who={auth.who} onSignOut={signOut} onConnectionChanged={connectionChanged} />;
}
