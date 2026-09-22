import { useEffect, useId, useMemo, useRef, useState, type FormEvent } from "react";
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Inline,
  Input,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  Select,
  Spinner,
  Stack,
  Text,
} from "@bzync/rui";
import { ApiError, api, type SessionProfile, type Whoami } from "./api";
import { EnvironmentBadge } from "../shared/status";

// Switch the whole Admin session — Operations and Studio — to another
// nextsqld server declared as a connection profile, or to the same server as
// a different user. The set of servers is fixed by whoever runs
// nextsql-admin (its --profiles file); this dialog can only pick one by ID.
//
// A switch is a fresh sign-in: nextsqld authenticates the new connection
// first, and only then does Admin issue a new session in place of this one.
// A failed switch leaves the current session untouched. The password is sent
// once and never kept by the browser; the operator may instead ask Admin to
// keep it in the operating system's credential store, where it is usable
// only when switching from this same server and user again.
export function profileLabel(profile: Pick<SessionProfile, "name" | "environment">): string {
  return profile.environment ? `${profile.name} (${profile.environment})` : profile.name;
}

export function SwitchServer({
  who,
  onClose,
  onSwitched,
  onUnauthorized,
}: {
  who: Whoami;
  onClose: () => void;
  onSwitched: (next: Whoami) => void;
  onUnauthorized: () => void;
}) {
  const formId = useId();
  const passwordRef = useRef<HTMLInputElement>(null);
  const [profiles, setProfiles] = useState<SessionProfile[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [profileId, setProfileId] = useState("");
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [useSaved, setUseSaved] = useState(false);
  const [save, setSave] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    let stopped = false;
    api
      .sessionProfiles()
      .then((list) => {
        if (stopped) return;
        setProfiles(list.profiles);
        // Default to the first server this session is not already on.
        const first = list.profiles.find((p) => p.id !== list.current) ?? list.profiles[0];
        if (first) {
          setProfileId(first.id);
          setUser(first.user || who.user);
        }
      })
      .catch((err: unknown) => {
        if (stopped) return;
        if (err instanceof ApiError && err.status === 401) {
          onUnauthorized();
          return;
        }
        setLoadError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      stopped = true;
    };
  }, [onUnauthorized, who.user]);

  const target = useMemo(() => profiles?.find((p) => p.id === profileId) ?? null, [profiles, profileId]);

  const chooseProfile = (id: string) => {
    setProfileId(id);
    const next = profiles?.find((p) => p.id === id);
    setUser(next?.user || who.user);
    setError(null);
    setNotice(null);
  };

  // A 401 means either "the target rejected these credentials" or "this
  // Admin session is gone". Ask the session route which, rather than
  // matching error text.
  const failure = (err: unknown, fallback: string) => {
    const message = err instanceof ApiError || err instanceof Error ? err.message : fallback;
    if (err instanceof ApiError && err.status === 401) {
      api
        .whoami()
        .then(() => setError(message))
        .catch(() => onUnauthorized());
      return;
    }
    setError(message);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!target || busy) return;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const next = await api.switchServer(
        useSaved
          ? { profile: target.id, user: user.trim(), use_saved_password: true }
          : { profile: target.id, user: user.trim(), password, save_password: save },
      );
      setPassword("");
      onSwitched(next);
    } catch (err) {
      setPassword("");
      if (useSaved) setUseSaved(false);
      failure(err, "Could not switch servers.");
      passwordRef.current?.focus();
    } finally {
      setBusy(false);
    }
  };

  const forget = async () => {
    if (!target || busy) return;
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await api.forgetSavedPassword({ profile: target.id, user: user.trim() });
      setUseSaved(false);
      setNotice(`Any password saved for ${user.trim()} on ${target.name} from this sign-in has been forgotten.`);
    } catch (err) {
      failure(err, "Could not forget the saved password.");
    } finally {
      setBusy(false);
    }
  };

  const canSubmit = Boolean(target) && user.trim() !== "" && (useSaved || password !== "") && !busy;

  return (
    <Modal open onClose={onClose} size="md" ariaLabel="Switch server">
      <ModalHeader>
        <ModalTitle>Switch server</ModalTitle>
      </ModalHeader>
      <ModalBody>
        {loadError ? (
          <Alert variant="error" title="Could not list servers" role="alert">{loadError}</Alert>
        ) : !profiles ? (
          <Inline gap="sm" align="center" role="status">
            <Spinner size="sm" />
            <Text size="sm" variant="muted">Loading connection profiles…</Text>
          </Inline>
        ) : (
          <form id={formId} onSubmit={submit} autoComplete="off" aria-busy={busy || undefined}>
            <Stack gap="sm">
              <Text size="sm" variant="muted">
                Sign in to another server declared in this Admin's connection profiles. Operations and
                Studio both move to it; your current session ends once the new sign-in succeeds, and
                stays as it is if it fails. Studio keeps each server's editor tabs separately in this
                browser.
              </Text>
              {error ? <Alert variant="error" title="Switch failed" role="alert">{error}</Alert> : null}
              {notice ? <Alert variant="info" role="status">{notice}</Alert> : null}
              <Select
                id={`${formId}-profile`}
                label="Server"
                options={profiles.map((p) => ({
                  value: p.id,
                  label: p.id === who.profile?.id ? `${profileLabel(p)} — current` : profileLabel(p),
                }))}
                value={profileId}
                onChange={(value) => chooseProfile(String(value))}
              />
              {target ? (
                <Inline gap="xs" align="center" wrap aria-label="Selected server">
                  <Text size="sm" className="font-mono">{target.address}</Text>
                  <Badge variant={target.tls ? "success" : "warning"} size="sm">
                    {target.tls ? (target.mtls ? "TLS 1.3 + client certificate" : "TLS 1.3") : "plaintext (loopback)"}
                  </Badge>
                  {target.environment ? <EnvironmentBadge environment={target.environment} /> : null}
                  {target.database ? <Badge variant="muted" size="sm" className="font-mono">db:{target.database}</Badge> : null}
                </Inline>
              ) : null}
              {target?.environment === "production" ? (
                <Text size="sm">
                  This is a <strong>production</strong> server. Studio starts in read-only mode there.
                </Text>
              ) : null}
              <Input
                id={`${formId}-user`}
                label="User"
                required
                autoComplete="username"
                spellCheck={false}
                value={user}
                onChange={(event) => setUser(event.currentTarget.value)}
              />
              <Input
                ref={passwordRef}
                id={`${formId}-password`}
                label="Password"
                type="password"
                autoComplete="current-password"
                required={!useSaved}
                disabled={useSaved}
                value={password}
                onChange={(event) => setPassword(event.currentTarget.value)}
              />
              <Checkbox
                id={`${formId}-use-saved`}
                label="Use the password I saved for this server and user"
                checked={useSaved}
                onChange={(event) => {
                  const on = event.currentTarget.checked;
                  setUseSaved(on);
                  if (on) {
                    setPassword("");
                    setSave(false);
                  }
                }}
              />
              <Checkbox
                id={`${formId}-save`}
                label="Save this password in the operating system's credential store"
                checked={save}
                disabled={useSaved}
                onChange={(event) => setSave(event.currentTarget.checked)}
              />
              <Text size="xs" variant="muted">
                A saved password is only offered back when you switch from the server and user you are
                signed in as now. Admin forgets it automatically if the server rejects it.
              </Text>
            </Stack>
          </form>
        )}
      </ModalBody>
      <ModalFooter>
        <Inline gap="sm" justify="between" wrap>
          <Button variant="ghost" size="sm" onClick={() => void forget()} disabled={!target || busy || user.trim() === ""}>
            Forget saved password
          </Button>
          <Inline gap="sm">
            <Button variant="ghost" size="sm" onClick={onClose} disabled={busy}>
              Cancel
            </Button>
            <Button type="submit" form={formId} size="sm" variant="primary" disabled={!canSubmit}>
              {busy ? "Switching…" : "Switch"}
            </Button>
          </Inline>
        </Inline>
      </ModalFooter>
    </Modal>
  );
}
