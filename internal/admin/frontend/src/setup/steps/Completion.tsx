import { Alert, Button, Card, CardBody, CodeBlock, Inline, Spinner, Stack, Text } from "@bzync/rui";
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
  const nextStep = params.skipInit
    ? `nextsql setup --data-dir ${params.dataDir} \\\n  --key-file ${params.keyFile} \\\n  --config-in ${result?.config_path ?? ""}`
    : `nextsqld --data-dir ${params.dataDir} \\\n  --key-file ${params.keyFile} \\\n  --listen ${listenAddr}${tlsFlags}`;

  let serviceAlert = null;
  if (service?.enabled && service.active) {
    serviceAlert = <Alert variant="success">NextSQL is now enabled and running as a system service — it will start automatically at boot.</Alert>;
  } else if (service?.enabled) {
    serviceAlert = <Alert variant="warning">Enabled to start at boot, but not currently running: {service.error}</Alert>;
  } else if (service) {
    serviceAlert = <Alert variant="warning">Not started at boot: {service.error}</Alert>;
  }

  const showNextStep = !service || !service.active;

  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader kicker="Complete" title={params.skipInit ? "Configuration written" : "NextSQL is ready"} />
        <Stack gap="sm" style={{ marginTop: 20 }}>
          {health ? (
            <Alert variant={health.ok ? "success" : "error"}>
              {health.ok ? "Health check passed." : "Health check reported a problem — see the server logs."}
            </Alert>
          ) : null}
          {serviceAlert}
          {showNextStep ? (
            <>
              <Text variant="muted">
                {params.skipInit ? "The database has not been initialized yet. Run this when you're ready:" : "Start the server with:"}
              </Text>
              <CodeBlock code={nextStep} language="bash" />
            </>
          ) : null}
        </Stack>
        <div className="nsi-actions">
          <Button variant="primary" onClick={onFinish}>Finish</Button>
        </div>
      </CardBody>
    </Card>
  );
}
