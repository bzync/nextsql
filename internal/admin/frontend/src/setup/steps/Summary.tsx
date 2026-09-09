import { Alert, Button, Card, CardBody, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm } from "@bzync/rui";
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
    <Card variant="bordered">
      <CardBody>
        <StepHeader kicker="Step 5 of 6" title="Review" description="Nothing has been created yet. Installing will:" />
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
            <DescriptionTerm>Recovery keys</DescriptionTerm>
            <DescriptionDetails>
              {params.recoveryKeyOut
                ? `Will be exported to ${params.recoveryKeyOut} and ${params.instanceRecoveryKeyOut || params.recoveryKeyOut + ".instance"} — save offline immediately`
                : params.skipInit
                  ? "Not now — no keystore exists until the database is initialized; export one then with `nextsql key add-recovery`"
                  : "None (single unlock path; root unlock key is a single point of failure)"}
            </DescriptionDetails>
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
                <DescriptionTerm>Database</DescriptionTerm>
                <DescriptionDetails>
                  {params.database
                    ? `Will be created as "${params.database}"`
                    : "None — the deployment is initialized without one; create it later with `nextsql init --database NAME`"}
                </DescriptionDetails>
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
          {params.recoveryKeyOut
            ? "Write down the unlock key path and save both recovery key files offline immediately after installation. Losing both keys means total, unrecoverable data loss."
            : "Write down the unlock key file path above. Without recovery keys, it is the only way to unlock this database and is never uploaded or recoverable if lost."}
        </Alert>
        <div className="nsi-actions">
          <Button variant="outline" onClick={onBack}>Back</Button>
          <Button variant="primary" onClick={onInstall}>Install</Button>
        </div>
      </CardBody>
    </Card>
  );
}
