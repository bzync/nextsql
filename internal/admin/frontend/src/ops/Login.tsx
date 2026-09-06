import { useState, type FormEvent } from "react";
import { Alert, AuthBackdrop, Button, Card, CardBody, Heading, Input, Stack, Text } from "@bzync/rui";
import { api, ApiError, type Whoami } from "./api";
import { Wordmark } from "../shared/Brand";
import { ThemeSelect } from "../shared/ThemeSelect";

export function Login({ onSignedIn }: { onSignedIn: (who: Whoami) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    const fd = new FormData(e.currentTarget);
    try {
      const who = await api.login({
        user: String(fd.get("user") ?? ""),
        password: String(fd.get("password") ?? ""),
        database: String(fd.get("database") ?? ""),
        realm: String(fd.get("realm") ?? ""),
      });
      onSignedIn(who);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <AuthBackdrop aria-hidden="true" />
      <main className="nsm-login-page bg-bg text-foreground">
        <div className="nsm-login-shell">
          <div className="nsm-brand-row">
            <Wordmark />
            <ThemeSelect />
          </div>
          <Card variant="elevated">
            <CardBody>
              <Stack gap="md">
                <Heading as="h1" size="lg">NextSQL Admin</Heading>
                <Text variant="muted">
                  Sign in as your own NextSQL user. Admin connects to nextsqld with these
                  credentials; server-side RBAC applies to everything you do.
                </Text>
                {error ? <Alert variant="error" title="Sign-in failed">{error}</Alert> : null}
                <form onSubmit={submit} autoComplete="off" aria-busy={busy || undefined}>
                  <Stack gap="sm">
                    <Input id="f-user" name="user" label="User" required autoComplete="username" />
                    <Input id="f-pw" name="password" label="Password" type="password" required autoComplete="current-password" />
                    <Input id="f-db" name="database" label="Database" hint="Optional" placeholder="(default)" />
                    <Input id="f-realm" name="realm" label="Realm" hint="Optional" placeholder="(default)" />
                    <Button type="submit" disabled={busy}>
                      {busy ? "Signing in…" : "Sign in"}
                    </Button>
                  </Stack>
                </form>
              </Stack>
            </CardBody>
          </Card>
        </div>
      </main>
    </>
  );
}
