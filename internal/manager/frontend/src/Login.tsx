import { useState, type FormEvent } from "react";
import { Alert, Button, FormField, Heading, Input, Stack, Text } from "@bzync/rui";
import { api, ApiError, type Whoami } from "./api";

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
    <div style={{ maxWidth: 360, margin: "48px auto", padding: "0 20px" }}>
      <Stack gap="md">
        <Heading as="h1" size="lg">NextSQL Manager</Heading>
        <Text variant="muted">
          Sign in as your own NextSQL user. The Manager connects to nextsqld with these
          credentials; server-side RBAC applies to everything you do.
        </Text>
        {error ? <Alert variant="error" title="Sign-in failed">{error}</Alert> : null}
        <form onSubmit={submit} autoComplete="off">
          <Stack gap="sm">
            <FormField label="User" htmlFor="f-user">
              <Input id="f-user" name="user" required autoComplete="username" />
            </FormField>
            <FormField label="Password" htmlFor="f-pw">
              <Input id="f-pw" name="password" type="password" required autoComplete="current-password" />
            </FormField>
            <FormField label="Database" hint="optional" htmlFor="f-db">
              <Input id="f-db" name="database" placeholder="(default)" />
            </FormField>
            <FormField label="Realm" hint="optional" htmlFor="f-realm">
              <Input id="f-realm" name="realm" placeholder="(default)" />
            </FormField>
            <Button type="submit" disabled={busy}>
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </Stack>
        </form>
      </Stack>
    </div>
  );
}
