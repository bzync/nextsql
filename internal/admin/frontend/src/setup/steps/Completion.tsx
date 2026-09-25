import { useEffect, useState } from "react";
import { Alert, Button, Card, CardBody, Checkbox, CodeBlock, Inline, Spinner, Stack, Text } from "@bzync/rui";
import type { FirewallOutcome, Params, PlanResult, ServiceOutcome } from "../api";
import { SetupErrorAlert } from "../components/SetupErrorAlert";
import { StepHeader } from "../components/StepHeader";
import { extractPort, looksNonLoopback } from "../util";

interface Stage {
  label: string;
}

function getStages(params?: Params): Stage[] {
  if (params?.skipInit) {
    return [
      { label: "Validating configuration & storage paths" },
      { label: "Writing configuration file (nextsql.conf)" },
      { label: "Finalizing setup" },
    ];
  }
  const stages: Stage[] = [
    { label: "Validating configuration & storage paths" },
    { label: "Initializing database & cryptographic keystores" },
  ];
  if (params?.recoveryKeyOut) {
    stages.push({ label: "Sealing & verifying offline recovery keys" });
  } else {
    stages.push({ label: "Configuring encryption envelope" });
  }
  stages.push({ label: "Writing configuration file (nextsql.conf)" });
  stages.push({ label: "Running post-install health verification" });
  if (params?.enableService || params?.enableFirewall) {
    stages.push({ label: "Configuring system service & network firewall" });
  } else {
    stages.push({ label: "Finalizing installation" });
  }
  return stages;
}

export function InstallProgress({ params }: { params?: Params }) {
  const stages = getStages(params);
  const [activeStage, setActiveStage] = useState(0);
  const [elapsedMs, setElapsedMs] = useState(0);

  useEffect(() => {
    const startTime = Date.now();
    const interval = setInterval(() => {
      const elapsed = Date.now() - startTime;
      setElapsedMs(elapsed);
      // Advance stages over time while installation runs.
      const stepDuration = 450;
      const nextStage = Math.min(stages.length - 1, Math.floor(elapsed / stepDuration));
      setActiveStage(nextStage);
    }, 100);
    return () => clearInterval(interval);
  }, [stages.length]);

  const percent = Math.min(95, Math.round(((activeStage + 0.6) / stages.length) * 100));
  const elapsedSec = (elapsedMs / 1000).toFixed(1);

  return (
    <Card variant="bordered" role="status" aria-live="polite" aria-atomic="false">
      <CardBody>
        <StepHeader kicker="Step 6 of 6" title="Installing…" />

        <div
          role="progressbar"
          aria-valuenow={percent}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuetext={`Step ${activeStage + 1} of ${stages.length}: ${stages[activeStage].label}`}
          aria-label="Installation progress"
          className="nsi-progress-bar"
        >
          <div className="nsi-progress-bar-fill" style={{ width: `${percent}%` }} />
        </div>

        <Inline justify="between" align="center" style={{ marginTop: 4 }}>
          <Text size="sm" weight="semibold">
            {stages[activeStage].label}
          </Text>
          <Text size="xs" variant="muted">
            Elapsed: {elapsedSec}s
          </Text>
        </Inline>

        <ul className="nsi-stage-list" role="list" aria-label="Installation stages">
          {stages.map((st, idx) => {
            const isDone = idx < activeStage;
            const isCurrent = idx === activeStage;
            return (
              <li
                key={st.label}
                className={`nsi-stage-item ${isDone ? "is-done" : isCurrent ? "is-active" : "is-pending"}`}
              >
                <span className="nsi-stage-icon" aria-hidden="true">
                  {isDone ? (
                    <span className="nsi-stage-check">✓</span>
                  ) : isCurrent ? (
                    <Spinner size="xs" />
                  ) : (
                    <span className="nsi-stage-dot" />
                  )}
                </span>
                <span className="nsi-stage-label">{st.label}</span>
                <span className="nsi-stage-state">
                  {isDone ? "Done" : isCurrent ? "In progress…" : "Pending"}
                </span>
              </li>
            );
          })}
        </ul>
      </CardBody>
    </Card>
  );
}

export function Completion({
  params, result, service, firewall, error, finished, onBackToSummary, onFinish,
}: {
  params: Params;
  result: PlanResult | null;
  service: ServiceOutcome | null;
  firewall: FirewallOutcome | null;
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

  let firewallAlert = null;
  if (firewall?.applied) {
    firewallAlert = (
      <Alert variant="success" title="Firewall rule created">
        <Text size="sm">
          Allowed incoming connections on TCP port {extractPort(params.listenAddr)} via {firewall.tool}:
        </Text>
        {firewall.command ? (
          <CodeBlock className="nsi-code" code={firewall.command} language="bash" />
        ) : null}
      </Alert>
    );
  } else if (firewall && !firewall.applied && firewall.error) {
    firewallAlert = (
      <Alert variant="warning" title="Firewall rule not created">
        <Stack gap="xs">
          <Text size="sm">{firewall.error}</Text>
          {firewall.command ? (
            <CodeBlock className="nsi-code" code={firewall.command} language="bash" />
          ) : null}
        </Stack>
      </Alert>
    );
  } else if (looksNonLoopback(params.listenAddr) && !params.enableFirewall) {
    const port = extractPort(params.listenAddr);
    firewallAlert = (
      <Alert variant="info" title="Network firewall notice">
        <Text size="sm">
          NextSQL is configured to listen on an external address ({listenAddr}). Ensure incoming TCP traffic on port {port} is permitted by your host or cloud firewall (e.g. <code>sudo ufw allow {port}/tcp</code>).
        </Text>
      </Alert>
    );
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
          {firewallAlert}
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
