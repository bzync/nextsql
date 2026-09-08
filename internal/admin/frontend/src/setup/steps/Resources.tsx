import { Alert, Button, Card, CardBody, Checkbox, Divider, Inline, Input, NumberInput, Radio, RadioGroup, Stack, Text } from "@bzync/rui";
import type { Params, RunResult, ServiceStatus } from "../api";
import { PathField } from "../components/PathField";
import { SetupErrorAlert } from "../components/SetupErrorAlert";
import { StepHeader } from "../components/StepHeader";
import { humanBytes, looksNonLoopback } from "../util";

const PRESETS: Array<[Params["preset"], string, string]> = [
  ["conservative", "Conservative", "~10% of RAM for the buffer pool — leaves headroom for other services on this machine."],
  ["balanced", "Balanced (recommended)", "~25% of RAM — a reasonable default for a dedicated or lightly shared machine."],
  ["high-performance", "High performance", "~50% of RAM — for a machine dedicated to NextSQL."],
  ["custom", "Custom", "Set the buffer pool size yourself, in pages."],
];

const PROFILES: Array<[Params["profile"], string, string]> = [
  ["production", "Production (recommended)", "Live-server defaults: disk watermarks, drain and statement timeouts, and a fail-closed preflight. Requires an administrator account. Unlock key must stay off the data volume."],
  ["developer", "Developer", "Loopback-friendly local defaults. Skip-init and a missing administrator are allowed. Do not expose this listener on a network."],
];

// ServiceOption renders the "start at boot" checkbox. It stays disabled with
// an explaining reason in every state except "a matching, already-installed
// unit was found" — checking is asynchronous (still in flight), unsupported
// on this host, no unit installed, or an installed unit that points at a
// different config than the one about to be written. The wizard never
// authors a unit itself — see internal/installgui/service.go.
//
// expectedConfig uses params.configOut (resolved from /api/v1/hello's
// defaults.configOut — the same /etc-or-per-user split the tarball/.deb/.run
// installers use, see defaults.go) rather than guessing DataDir/nextsql.conf:
// nextsql setup's own config path when --config-out is omitted. Guessing the
// latter here previously meant this checkbox silently disabled itself with a
// "points at a different configuration" warning for every packaged install,
// since a packaged installer's systemd unit always points at the separate
// config path, never inside the data directory.
function ServiceOption({
  params, patch, serviceStatus, serviceStatusChecked,
}: {
  params: Params;
  patch: (p: Partial<Params>) => void;
  serviceStatus: ServiceStatus | null;
  serviceStatusChecked: boolean;
}) {
  const box = (disabled: boolean, hint: string, variant?: "warning") => {
    if (disabled && params.enableService) patch({ enableService: false });
    return (
      <Stack gap="xs">
        <Checkbox
          id="enableService"
          label="Start NextSQL automatically at boot"
          checked={params.enableService && !disabled}
          disabled={disabled}
          onChange={(e) => patch({ enableService: e.target.checked })}
        />
        {variant ? <Alert variant={variant}>{hint}</Alert> : <Text variant="muted" size="sm">{hint}</Text>}
      </Stack>
    );
  };

  if (!serviceStatusChecked) return box(true, "Checking for an installed NextSQL service…");
  const st = serviceStatus;
  if (!st || !st.supported) return box(true, "Not available on this host (no systemd, or an unsupported platform).");
  if (!st.unitFound) {
    const scope = st.scope === "user" ? "--user " : "";
    return box(
      true,
      `No NextSQL service is installed on this host yet. Install via a packaged .deb/.tar.gz/.run installer to get one, then re-run this wizard — or run \`systemctl ${scope}enable --now nextsql\` yourself once this finishes.`,
    );
  }
  const expectedConfig = params.configOut || (params.dataDir.replace(/\/+$/, "") + "/nextsql.conf");
  if (st.configPath && st.configPath !== expectedConfig) {
    return box(
      true,
      `A NextSQL service is installed here, but it points at a different configuration (${st.configPath}) than this install will write (${expectedConfig}). Not offered to avoid enabling it against the wrong database.`,
      "warning",
    );
  }
  const scope = st.scope === "user" ? "--user " : "";
  return box(false, `A matching NextSQL service (${st.scope} scope) is already installed on this host. Checking this runs \`systemctl ${scope}enable --now nextsql\` once the database is created and verified healthy.`);
}

export function Resources({
  params, patch, showNetwork, setShowNetwork, serviceStatus, serviceStatusChecked, lastPlan, planError, onCheck, onBack, onNext,
}: {
  params: Params;
  patch: (p: Partial<Params>) => void;
  showNetwork: boolean;
  setShowNetwork: (v: boolean) => void;
  serviceStatus: ServiceStatus | null;
  serviceStatusChecked: boolean;
  lastPlan: RunResult | null;
  planError: string | null;
  onCheck: () => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const rec = lastPlan?.result?.recommendation;
  const remoteLooking = looksNonLoopback(params.listenAddr);
  const hasBothTLS = !!params.tlsCert && !!params.tlsKey;

  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader kicker="Step 3 of 6" title="Deployment profile" />
        <Stack gap="md" style={{ marginTop: 20 }}>
          <RadioGroup
            label="Choose a deployment profile"
            value={params.profile}
            onChange={(v) => {
              const profile = v as Params["profile"];
              patch(profile === "production" ? { profile, skipInit: false } : { profile });
              onCheck();
            }}
          >
            {PROFILES.map(([value, label, desc]) => (
              <Radio key={value} value={value} label={label} description={desc} />
            ))}
          </RadioGroup>
          {params.profile === "production" ? (
            <Alert variant="success">
              Production preflight will refuse a key file inside the data directory, --skip-init,
              and an install without an administrator. A tmpfs/ramfs data volume is warned, not blocked.
            </Alert>
          ) : (
            <Alert variant="warning">
              Developer profile is for local work. Use Production for any live deployment.
            </Alert>
          )}
        </Stack>

        <Divider label="Resources" spacing="md" />
        <Stack gap="md">
          <RadioGroup
            label="Choose a resource preset"
            value={params.preset}
            onChange={(v) => { patch({ preset: v as Params["preset"] }); onCheck(); }}
          >
            {PRESETS.map(([value, label, desc]) => (
              <Radio key={value} value={value} label={label} description={desc} />
            ))}
          </RadioGroup>

          {params.preset === "custom" ? (
            <NumberInput
              id="bufferPages" label="Buffer pool pages" min={16} value={params.bufferPages || undefined}
              onChange={(v) => patch({ bufferPages: v || 0 })}
            />
          ) : null}

          {rec ? (
            <Alert variant="success">
              Buffer pool: {rec.buffer_pages} pages ({humanBytes(rec.buffer_bytes)}) — {rec.rationale}
            </Alert>
          ) : null}
        </Stack>

        <Divider label="Advanced" spacing="md" />
        <Stack gap="md">
          <Checkbox
            id="skipInit"
            label="Write the configuration file only — don't initialize the database now"
            description={
              params.profile === "production"
                ? "Not available on the production profile — a live install must initialize the database with an administrator."
                : (params.skipInit ? "You can initialize it later with `nextsql setup --skip-init=false` or `nextsql init` against the generated config." : undefined)
            }
            checked={params.skipInit && params.profile !== "production"}
            disabled={params.profile === "production"}
            onChange={(e) => {
              const skipInit = e.target.checked;
              patch(skipInit ? { skipInit, adminUser: "", adminPassword: "", enableService: false } : { skipInit });
            }}
          />

          <Stack gap="sm">
            <Checkbox
              id="showNetwork"
              label="Configure a remote listen address"
              description="Only needed to accept connections from other machines — leave off for the loopback default (127.0.0.1:7210, no TLS)."
              checked={showNetwork}
              aria-expanded={showNetwork}
              aria-controls="network-settings"
              onChange={(e) => setShowNetwork(e.target.checked)}
            />
            {showNetwork ? (
              <Stack id="network-settings" gap="sm" style={{ marginLeft: 28 }}>
                <Text variant="muted" size="sm">
                  A remote address requires TLS 1.3 — provide the certificate and private key files
                  already present on this machine; their content is never uploaded, only the paths
                  are sent.
                </Text>
                <Input
                  id="listenAddr" label="Listen address" placeholder="127.0.0.1:7210"
                  value={params.listenAddr} onChange={(e) => patch({ listenAddr: e.target.value })}
                />
                <PathField
                  id="tlsCert" label="TLS certificate file (PEM)" mode="file"
                  placeholder="/etc/nextsql/tls/fullchain.pem" value={params.tlsCert}
                  onChange={(v) => patch({ tlsCert: v })}
                />
                <PathField
                  id="tlsKey" label="TLS private key file (PEM)" mode="file"
                  placeholder="/etc/nextsql/tls/privkey.pem" value={params.tlsKey}
                  onChange={(v) => patch({ tlsKey: v })}
                />
                {remoteLooking && !hasBothTLS ? (
                  <Alert variant="warning">
                    A non-loopback listen address requires both a TLS certificate and key file above
                    — nextsql setup refuses otherwise and writes nothing.
                  </Alert>
                ) : null}
              </Stack>
            ) : null}
          </Stack>
        </Stack>

        {!params.skipInit ? (
          <>
            <Divider label="Startup" spacing="md" />
            <ServiceOption params={params} patch={patch} serviceStatus={serviceStatus} serviceStatusChecked={serviceStatusChecked} />
          </>
        ) : null}

        {planError ? <SetupErrorAlert raw={planError} style={{ marginTop: 20 }} /> : null}
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
