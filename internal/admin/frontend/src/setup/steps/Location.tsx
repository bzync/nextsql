import { useEffect, useRef, useState } from "react";
import { Alert, Button, Card, CardBody, Checkbox, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm, Inline, List, ListItem, Spinner, Stack, Text } from "@bzync/rui";
import { api, type LifecycleDetect, type Params, type RunResult } from "../api";
import { PathField } from "../components/PathField";
import { SetupErrorAlert } from "../components/SetupErrorAlert";
import { StepHeader } from "../components/StepHeader";
import { humanBytes } from "../util";

export function Location({
  params, patch, lastPlan, planError, onCheck, onBack, onNext,
}: {
  params: Params;
  patch: (p: Partial<Params>) => void;
  lastPlan: RunResult | null;
  planError: string | null;
  onCheck: () => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const r = lastPlan?.result;
  const keyFileExists = r?.key_file_exists;
  // The checkbox is its own state, not `params.recoveryKeyOut !== ""`:
  // deriving it meant clearing the path field (to retype it) unchecked the
  // box and hid the field mid-edit. `params` is still the authority for
  // whether an export is configured, so an external change — the defaults
  // arriving from /api/v1/hello, or the Resources step clearing the export
  // when "configuration only" is chosen — is adopted here.
  const [recoveryEnabled, setRecoveryEnabled] = useState(params.recoveryKeyOut !== "");
  const [recPath, setRecPath] = useState(params.recoveryKeyOut || "");
  const pushed = useRef(params.recoveryKeyOut);
  useEffect(() => {
    if (params.recoveryKeyOut === pushed.current) return; // our own edit, echoed back
    pushed.current = params.recoveryKeyOut;
    setRecoveryEnabled(params.recoveryKeyOut !== "");
    setRecPath(params.recoveryKeyOut);
  }, [params.recoveryKeyOut]);

  function setRecovery(enabled: boolean, path: string) {
    setRecoveryEnabled(enabled);
    setRecPath(path);
    const out = enabled ? path.trim() : "";
    pushed.current = out;
    patch({ recoveryKeyOut: out, instanceRecoveryKeyOut: out ? out + ".instance" : "" });
  }

  // Checked with no path would install with no recovery key at all, which is
  // the opposite of what the operator just asked for, so it blocks instead.
  const recoveryPathMissing = recoveryEnabled && recPath.trim() === "";
  const [detect, setDetect] = useState<LifecycleDetect | null>(null);
  const [detectError, setDetectError] = useState<string | null>(null);
  const [detecting, setDetecting] = useState(false);
  async function inspectExisting() {
    setDetecting(true); setDetectError(null); setDetect(null);
    try {
      const response = await api.lifecycleDetect(params.dataDir, params.configOut);
      if (!response.ok || !response.result) throw new Error(response.error || "could not inspect this location");
      setDetect(response.result as unknown as LifecycleDetect);
    } catch (err) {
      setDetectError(err instanceof Error ? err.message : String(err));
    } finally { setDetecting(false); }
  }

  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader
          kicker="Step 2 of 6"
          title="Data directory & unlock key"
          description="The data directory holds the encrypted database. The key file unlocks it and must never leave this machine — keep it off the data volume in production."
        />
        <Stack gap="sm" style={{ marginTop: 20 }}>
          <PathField
            id="dataDir" label="Data directory" mode="directory"
            placeholder="/var/lib/nextsql" value={params.dataDir}
            onChange={(v) => patch({ dataDir: v })}
          />
          <PathField
            id="keyFile" label="Root unlock key file" mode="file"
            placeholder="/etc/nextsql/root.key" value={params.keyFile}
            onChange={(v) => patch({ keyFile: v })}
          />
          <Text variant="muted" size="sm">
            Both are created if missing. Existing data or keys at these paths are never
            deleted or overwritten by this wizard.
          </Text>

          <Stack gap="xs" style={{ marginTop: 8 }}>
            <Checkbox
              id="enableRecoveryKey"
              label="Generate offline recovery keys (recommended backup unlock path)"
              checked={recoveryEnabled}
              onChange={(e) => {
                if (e.target.checked) {
                  const fallback = params.keyFile
                    ? params.keyFile.replace(/root\.key$/, "recovery.key")
                    : "/etc/nextsql/recovery.key";
                  setRecovery(true, recPath || fallback);
                } else {
                  setRecovery(false, recPath);
                }
              }}
            />
            <Text variant="muted" size="sm">
              A recovery key creates an independent second unlock path for both the database and deployment registry keystores, preventing total data loss if the root key is lost.
            </Text>
            {recoveryEnabled ? (
              <Stack gap="xs">
                <PathField
                  id="recoveryKeyOut"
                  label="Recovery key export file"
                  mode="file"
                  placeholder="/etc/nextsql/recovery.key"
                  value={recPath}
                  onChange={(v) => setRecovery(true, v)}
                />
                <Text variant="muted" size="sm">
                  Exports the database recovery key and registry recovery key ({recPath.trim() ? recPath.trim() + ".instance" : "…"}). Store both offline.
                </Text>
                {recoveryPathMissing ? (
                  <Alert variant="error">
                    Enter a path for the recovery key export, or clear the checkbox to install with the root unlock key as the only way in.
                  </Alert>
                ) : null}
              </Stack>
            ) : (
              <Alert variant="warning">
                Without recovery keys, the root unlock key is the single point of failure: losing it means permanent, unrecoverable data loss.
              </Alert>
            )}
          </Stack>

          {lastPlan && r ? (
            keyFileExists ? (
              <Alert variant="warning">
                An existing key file was found at this path — it will be imported and reused, not overwritten.
              </Alert>
            ) : (
              <Alert variant="success">
                No key file exists at this path yet — a new root unlock key will be generated here.
              </Alert>
            )
          ) : null}

          {planError ? <SetupErrorAlert raw={planError} /> : null}
          <Inline gap="sm" align="center" wrap>
            <Button variant="outline" size="sm" onClick={inspectExisting} disabled={detecting}>
              {detecting ? <><Spinner size="sm" /> Inspecting…</> : "Inspect existing installation"}
            </Button>
            <Text size="sm" variant="muted">Read-only; it does not change files or start a server.</Text>
          </Inline>
          {detect ? (
            <Alert variant={detect.status === "healthy" ? "success" : "warning"} title={`Existing installation: ${detect.status}`}>
              {detect.summary} {detect.server_running ? "A server currently holds the deployment lock; lifecycle changes must wait." : ""}
            </Alert>
          ) : null}
          {detectError ? <SetupErrorAlert raw={detectError} /> : null}
          {r?.hardware ? (
            <DescriptionList columns={1} density="compact">
              <DescriptionItem>
                <DescriptionTerm>Disk free</DescriptionTerm>
                <DescriptionDetails>
                  {humanBytes(r.hardware.disk_free_bytes)} of {humanBytes(r.hardware.disk_total_bytes)}
                </DescriptionDetails>
              </DescriptionItem>
            </DescriptionList>
          ) : null}
          {r?.warnings?.length ? (
            <List>
              {r.warnings.map((w, i) => <ListItem key={i}><Text size="sm">{w}</Text></ListItem>)}
            </List>
          ) : null}
        </Stack>
        <div className="nsi-actions">
          <Button variant="outline" onClick={onBack}>Back</Button>
          <Inline gap="sm">
            <Button variant="secondary" onClick={onCheck} disabled={recoveryPathMissing}>Check</Button>
            <Button variant="primary" onClick={onNext} disabled={recoveryPathMissing}>Continue</Button>
          </Inline>
        </div>
      </CardBody>
    </Card>
  );
}
