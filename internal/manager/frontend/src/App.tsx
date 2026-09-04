import { useCallback, useEffect, useState } from "react";
import { Spinner } from "@bzync/rui";
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

  if (auth.phase === "checking") return <Spinner />;
  if (auth.phase === "out") return <Login onSignedIn={signedIn} />;
  return <Shell who={auth.who} onSignOut={signOut} />;
}
