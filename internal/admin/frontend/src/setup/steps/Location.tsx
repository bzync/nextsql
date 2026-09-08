import { Alert, Button, Card, CardBody, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm, Inline, List, ListItem, Stack, Text } from "@bzync/rui";
import type { Params, RunResult } from "../api";
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
            <Button variant="secondary" onClick={onCheck}>Check</Button>
            <Button variant="primary" onClick={onNext}>Continue</Button>
          </Inline>
        </div>
      </CardBody>
    </Card>
  );
}
