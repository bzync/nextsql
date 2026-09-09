import { useState } from "react";
import { Alert, Button, Card, CardBody, Checkbox, CodeBlock, Inline, Spinner, Stack, Text } from "@bzync/rui";
import type { Params, PlanResult, ServiceOutcome } from "../api";
import { SetupErrorAlert } from "../components/SetupErrorAlert";
import { StepHeader } from "../components/StepHeader";

export function InstallProgress() {
  return (
    <Card variant="bordered" role="status" aria-live="polite" aria-atomic="true">
      <CardBody>
        <StepHeader kicker="Step 6 of 6" title="Installing…" />
        <Inline gap="sm" align="center" style={{ marginTop: 20 }}>
          <Spinner size="sm" />
          <Text variant="muted">Creating the database and verifying it. This usually takes a few seconds.</Text>
        </Inline>
      </CardBody>
    </Card>
  );
}

export function Completion({
  params, result, service, error, finished, onBackToSummary, onFinish,
}: {
  params: Params;
  result: PlanResult | null;
  service: ServiceOutcome | null;
  error: string | null;
  finished: boolean;
  onBackToSummary: () => void;
  onFinish: () => void;
}) {
  const [recoverySavedConfirmed, setRecoverySavedConfirmed] = useState(false);

  if (finished) {
    return (
      <Card variant="bordered">
        <CardBody>
          <StepHeader kicker="Complete" title="Setup finished" description="The Setup service has stopped. You can close this tab." />
        </CardBody>
      </Card>
    );
  }
  if (error) {
    return (
      <Card variant="bordered">
        <CardBody>
          <StepHeader kicker="Step 6 of 6" title="Setup failed" />
          <SetupErrorAlert raw={error} style={{ marginTop: 20 }} />
          <div className="nsi-actions">
            <Button variant="outline" onClick={onBackToSummary}>Back to summary</Button>
          </div>
        </CardBody>
      </Card>
    );
  }

  const health = result?.health;
  const listenAddr = result?.listen_addr || params.listenAddr || "127.0.0.1:7210";
  const tlsFlags = result?.tls ? ` \\\n  --tls-cert ${params.tlsCert} \\\n  --tls-key ${params.tlsKey}` : "";
  // Three shapes, because three things can have been installed: a config
  // only, a deployment with no database yet (nextsqld fails closed on one,
  // so the next step is creating it), or a database ready to serve.
  const databaseCreated = Boolean(result?.initialized);
  const nextStep = params.skipInit
    ? `nextsql setup --data-dir ${params.dataDir} \\\n  --key-file ${params.keyFile} \\\n  --config-in ${result?.config_path ?? ""}`
    : !databaseCreated
      ? `nextsql init --data-dir ${params.dataDir} \\\n  --key-file ${params.keyFile} \\\n  --database NAME`
      : `nextsqld --data-dir ${params.dataDir} \\\n  --key-file ${params.keyFile} \\\n  --listen ${listenAddr}${tlsFlags}`;

  let serviceAlert = null;
  if (service?.enabled && service.active) {
    serviceAlert = <Alert variant="success">NextSQL is now enabled and running as a system service — it will start automatically at boot.</Alert>;
  } else if (service?.enabled) {
    serviceAlert = <Alert variant="warning">Enabled to start at boot, but not currently running: {service.error}</Alert>;
  } else if (service) {
    serviceAlert = <Alert variant="warning">Not started at boot: {service.error}</Alert>;
  }

  // A deployment with no database always shows its next step: the service
  // being "active" cannot mean the server is serving, because it isn't.
  const showNextStep = !databaseCreated || !service || !service.active;
  const finishDisabled = Boolean(result?.recovery_keys_created && !recoverySavedConfirmed);

  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader
          kicker="Complete"
          title={params.skipInit ? "Configuration written" : databaseCreated ? "NextSQL is ready" : "Deployment initialized"}
        />
        <Stack gap="sm" style={{ marginTop: 20 }}>
          {health ? (
            <Alert variant={health.ok ? "success" : "error"}>
              {health.ok ? "Health check passed." : "Health check reported a problem — see the server logs."}
            </Alert>
          ) : null}
          {result?.recovery_keys_created ? (
            <Alert variant="warning" title="Action required: Save recovery keys offline">
              <Stack gap="xs">
                <Text>
                  Two recovery keys were exported and verified against both keystores:
                </Text>
                <CodeBlock
                  className="nsi-code"
                  code={`${result.recovery_key_file}  (database keystore)\n${result.instance_recovery_key_file}  (deployment registry keystore)`}
                  language="text"
                />
                <Text size="sm">
                  These files are the only copies. Move both off this host now to an offline vault or secure external storage, then remove them from this host. If the root unlock key is ever lost, these recovery keys are the only way to recover your data.
                </Text>
                <Checkbox
                  id="confirmRecoverySaved"
                  label="I confirm that I have safely copied both recovery key files offline"
                  checked={recoverySavedConfirmed}
                  onChange={(e) => setRecoverySavedConfirmed(e.target.checked)}
                />
              </Stack>
            </Alert>
          ) : null}
          {serviceAlert}
          {showNextStep ? (
            <>
              <Text variant="muted">
                {params.skipInit
                  ? "The database has not been initialized yet. Run this when you're ready:"
                  : !databaseCreated
                    ? "This deployment has no database yet — the server will not start until one exists. Create it with:"
                    : "Start the server with:"}
              </Text>
              <CodeBlock className="nsi-code" code={nextStep} language="bash" />
            </>
          ) : null}
        </Stack>
        <div className="nsi-actions">
          <Button variant="primary" onClick={onFinish} disabled={finishDisabled}>Finish</Button>
        </div>
      </CardBody>
    </Card>
  );
}
