import { useState, type FormEvent } from "react";
import { Alert, AuthBackdrop, Button, Heading, Input, Stack, Text } from "@bzync/rui";
import { api, ApiError, type Whoami } from "./api";
import { Wordmark } from "../shared/Brand";
import { Icon } from "../shared/icons";
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
          <aside className="nsm-login-aside">
            <Stack gap="md">
              <div className="nsm-login-aside-brand">
                <Wordmark size="lg" />
              </div>
              <Text variant="muted">
                Operator console for this nextsqld. Sign in as your own database
                user — server-side RBAC applies to everything you do.
              </Text>
              <ul className="nsm-login-facts">
                <li><Icon name="lock" size={14} /> Encrypted storage by default</li>
                <li><Icon name="shield" size={14} /> No engine-file access from this UI</li>
                <li><Icon name="key" size={14} /> Session cookie + CSRF on every write</li>
              </ul>
            </Stack>
          </aside>
          <div className="nsm-login-form">
            <Stack gap="md">
              <div className="nsm-brand-row">
                <Heading as="h1" size="lg">NextSQL Admin</Heading>
                <ThemeSelect />
              </div>
              <Text variant="muted">
                Sign in with NextSQL credentials. The database is optional when
                the server has a default.
              </Text>
              {error ? <Alert variant="error" title="Sign-in failed">{error}</Alert> : null}
              <form onSubmit={submit} autoComplete="off" aria-busy={busy || undefined}>
                <Stack gap="sm">
                  <Input id="f-user" name="user" label="User" required autoComplete="username" />
                  <Input id="f-pw" name="password" label="Password" type="password" required autoComplete="current-password" />
                  <Input id="f-db" name="database" label="Database" hint="Optional" />
                  <Button type="submit" disabled={busy} icon={<Icon name="lock" size={14} />}>
                    {busy ? "Signing in…" : "Sign in"}
                  </Button>
                </Stack>
              </form>
            </Stack>
          </div>
        </div>
      </main>
    </>
  );
}
