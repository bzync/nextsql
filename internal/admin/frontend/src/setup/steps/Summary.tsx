import { Alert, Button, Card, CardBody, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm, Inline } from "@bzync/rui";
import type { Params, RunResult } from "../api";
import { StepHeader } from "../components/StepHeader";

export function Summary({
  params, lastPlan, onBack, onInstall,
}: {
  params: Params;
  lastPlan: RunResult | null;
  onBack: () => void;
  onInstall: () => void;
}) {
  const keyFileKnown = !!lastPlan?.result;
  const keyFileAction = keyFileKnown
    ? (lastPlan!.result!.key_file_exists ? "existing — will be imported" : "new — will be generated")
    : "unknown — press Check on the previous step";

  return (
    <Card variant="elevated">
      <CardBody>
        <StepHeader title="Review" description="Nothing has been created yet. Installing will:" />
        <DescriptionList columns={1} density="compact" style={{ marginTop: 20 }}>
          <DescriptionItem>
            <DescriptionTerm>Data directory</DescriptionTerm>
            <DescriptionDetails>{params.dataDir}</DescriptionDetails>
          </DescriptionItem>
          <DescriptionItem>
            <DescriptionTerm>Unlock key file</DescriptionTerm>
            <DescriptionDetails>{params.keyFile} ({keyFileAction})</DescriptionDetails>
          </DescriptionItem>
          <DescriptionItem>
            <DescriptionTerm>Configuration file</DescriptionTerm>
            <DescriptionDetails>{params.configOut || "(default: inside the data directory)"}</DescriptionDetails>
          </DescriptionItem>
          <DescriptionItem>
            <DescriptionTerm>Deployment profile</DescriptionTerm>
            <DescriptionDetails>{params.profile || "developer"}</DescriptionDetails>
          </DescriptionItem>
          <DescriptionItem>
            <DescriptionTerm>Resource preset</DescriptionTerm>
            <DescriptionDetails>{params.preset}{params.preset === "custom" ? ` (${params.bufferPages} pages)` : ""}</DescriptionDetails>
          </DescriptionItem>
          {params.skipInit ? (
            <DescriptionItem>
              <DescriptionTerm>Database</DescriptionTerm>
              <DescriptionDetails>Not initialized now — configuration file only</DescriptionDetails>
            </DescriptionItem>
          ) : (
            <>
              <DescriptionItem>
                <DescriptionTerm>Administrator</DescriptionTerm>
                <DescriptionDetails>{params.adminUser || "(none — add one later with `nextsql init`'s user tools)"}</DescriptionDetails>
              </DescriptionItem>
              <DescriptionItem>
                <DescriptionTerm>Realm / database</DescriptionTerm>
                <DescriptionDetails>{params.realm} / {params.database}</DescriptionDetails>
              </DescriptionItem>
            </>
          )}
          <DescriptionItem>
            <DescriptionTerm>Listen address</DescriptionTerm>
            <DescriptionDetails>{params.listenAddr || "127.0.0.1:7210 (default)"}{params.tlsCert ? " (TLS)" : ""}</DescriptionDetails>
          </DescriptionItem>
          {!params.skipInit ? (
            <DescriptionItem>
              <DescriptionTerm>Start at boot</DescriptionTerm>
              <DescriptionDetails>{params.enableService ? "Yes — will enable and start the existing matching service" : "No"}</DescriptionDetails>
            </DescriptionItem>
          ) : null}
        </DescriptionList>
        <Alert variant="warning" style={{ marginTop: 20 }}>
          Write down the unlock key file path above. It is required every time the server starts
          and is never uploaded or recoverable by NextSQL if lost.
        </Alert>
        <Inline justify="between" style={{ marginTop: 20 }}>
          <Button variant="outline" onClick={onBack}>Back</Button>
          <Button variant="primary" onClick={onInstall}>Install</Button>
        </Inline>
      </CardBody>
    </Card>
  );
}
