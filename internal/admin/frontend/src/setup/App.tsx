import { useEffect, useRef, useState } from "react";
import { Alert, AuthBackdrop, Link, List, ListItem, Stepper, Text, type StepperStep } from "@bzync/rui";
import { api, ApiError, defaultParams, type Hello, type Params, type PlanResult, type RunResult, type ServiceOutcome, type ServiceStatus } from "./api";
import { Wordmark } from "../shared/Brand";
import { ThemeSelect } from "../shared/ThemeSelect";
import { StepHeader } from "./components/StepHeader";
import { Welcome } from "./steps/Welcome";
import { Location } from "./steps/Location";
import { Resources } from "./steps/Resources";
import { Administrator } from "./steps/Administrator";
import { Summary } from "./steps/Summary";
import { Completion, InstallProgress } from "./steps/Completion";

const STEPS: StepperStep[] = [
  { label: "Welcome", description: "Overview" },
  { label: "Location", description: "Data & unlock key" },
  { label: "Resources", description: "Preset & network" },
  { label: "Administrator", description: "Admin account" },
  { label: "Summary", description: "Review" },
  { label: "Install", description: "Create the database" },
];

function errMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}

export function App() {
  const [step, setStep] = useState(0);
  const [hello, setHello] = useState<Hello | null>(null);
  const [helloError, setHelloError] = useState<string | null>(null);
  const [params, setParams] = useState<Params>(defaultParams());
  const [showNetwork, setShowNetwork] = useState(false);
  const [serviceStatus, setServiceStatus] = useState<ServiceStatus | null>(null);
  const [serviceStatusChecked, setServiceStatusChecked] = useState(false);
  const [lastPlan, setLastPlan] = useState<RunResult | null>(null);
  const [planError, setPlanError] = useState<string | null>(null);
  const [installResult, setInstallResult] = useState<PlanResult | null>(null);
  const [installService, setInstallService] = useState<ServiceOutcome | null>(null);
  const [installError, setInstallError] = useState<string | null>(null);
  const [installing, setInstalling] = useState(false);
  const [finished, setFinished] = useState(false);
  const previousView = useRef<string | null>(null);

  useEffect(() => {
    api.hello().then((res) => {
      setHello(res);
      setParams((p) => ({
        ...p,
        dataDir: res.defaults.dataDir,
        keyFile: res.defaults.keyFile,
        configOut: res.defaults.configOut || "",
        recoveryKeyOut: res.defaults.recoveryKeyOut || "",
      }));
    }).catch((err) => setHelloError(errMessage(err)));

    // Best-effort, read-only: a failure here just leaves the "start at
    // boot" checkbox degraded to "unavailable" — the rest of the wizard is
    // unaffected either way.
    api.service().then((res) => {
      setServiceStatus(res);
      setServiceStatusChecked(true);
    }).catch(() => setServiceStatusChecked(true));
  }, []);

  function patch(p: Partial<Params>) {
    setParams((prev) => ({ ...prev, ...p }));
  }

  async function checkPlan(next?: Params) {
    setPlanError(null);
    try {
      const res = await api.plan(next ?? params);
      setLastPlan(res);
      if (!res.ok) setPlanError(res.error || "unknown error");
    } catch (err) {
      setPlanError(errMessage(err));
    }
  }

  async function doInstall() {
    setInstalling(true);
    setInstallError(null);
    try {
      const res = await api.install(params);
      if (res.ok) {
        setInstallResult(res.result ?? null);
        setInstallService(res.service ?? null);
      } else {
        setInstallError(res.error || "unknown error");
      }
    } catch (err) {
      setInstallError(errMessage(err));
    } finally {
      setInstalling(false);
    }
  }

  function doFinish() {
    // Optimistic: the installer process may already be gone by the time a
    // response would arrive (it stops itself right after), and that's
    // fine — the UI has already moved on regardless of whether this
    // specific fetch succeeds.
    setFinished(true);
    api.finish().catch(() => {});
  }

  let body;
  if (step === 0) {
    body = <Welcome hello={hello} onNext={() => setStep(1)} />;
  } else if (step === 1) {
    body = (
      <Location
        params={params}
        patch={patch}
        lastPlan={lastPlan}
        planError={planError}
        onCheck={() => checkPlan()}
        onBack={() => setStep(0)}
        onNext={() => { setStep(2); checkPlan(); }}
      />
    );
  } else if (step === 2) {
    body = (
      <Resources
        params={params}
        patch={patch}
        showNetwork={showNetwork}
        setShowNetwork={setShowNetwork}
        serviceStatus={serviceStatus}
        serviceStatusChecked={serviceStatusChecked}
        lastPlan={lastPlan}
        planError={planError}
        onCheck={() => checkPlan()}
        onBack={() => setStep(1)}
        onNext={() => { const s = params.skipInit ? 4 : 3; setStep(s); checkPlan(); }}
      />
    );
  } else if (step === 3) {
    body = <Administrator params={params} patch={patch} onBack={() => setStep(2)} onNext={() => setStep(4)} />;
  } else if (step === 4) {
    body = (
      <Summary
        params={params}
        lastPlan={lastPlan}
        onBack={() => setStep(params.skipInit ? 2 : 3)}
        onInstall={() => { setStep(5); doInstall(); }}
      />
    );
  } else {
    body = installing
      ? <InstallProgress />
      : (
        <Completion
          params={params}
          result={installResult}
          service={installService}
          error={installError}
          finished={finished}
          onBackToSummary={() => setStep(4)}
          onFinish={doFinish}
        />
      );
  }

  const version = hello ? `${hello.nextsql_version} · ${hello.defaults.os}${hello.defaults.elevated ? " · elevated" : ""}` : "";
  const installState = installing ? "progress" : finished ? "finished" : installError ? "error" : "complete";
  const viewKey = helloError ? "connection-error" : step === 5 ? `install-${installState}` : `step-${step}`;
  const viewLabel = helloError
    ? "Installer unavailable"
    : step === 5
      ? installing
        ? "Installing"
        : finished
          ? "Setup finished"
          : installError
            ? "Setup failed"
            : params.skipInit
              ? "Configuration written"
              : "NextSQL is ready"
      : STEPS[step].label;

  // Treat both wizard navigation and the install/result state transition as
  // page-like changes: update the document title, announce the new view, and
  // move focus to its heading. Do not steal focus on the initial render.
  useEffect(() => {
    document.title = `${viewLabel} · NextSQL Admin`;
    if (previousView.current !== null && previousView.current !== viewKey) {
      document.getElementById("installer-step-title")?.focus();
    }
    previousView.current = viewKey;
  }, [viewKey, viewLabel]);

  return (
    <>
      <Link className="nsa-skip" href="#installer-main">Skip to Admin setup</Link>
      <AuthBackdrop aria-hidden="true" />
      <div className="nsi-page bg-bg text-foreground">
        <div className="nsi-shell">
          <aside className="nsi-rail">
            <div className="nsi-brand-row">
              <Wordmark size="md" className="nsi-brand" />
              <ThemeSelect />
            </div>
            <span className="nsi-mode">Setup</span>
            <nav className="sr-only" aria-label="Installation progress">
              <List>
                {STEPS.map((item, index) => (
                  <ListItem key={item.label} aria-current={index === step ? "step" : undefined}>
                    {index + 1}. {item.label}{index < step ? " — complete" : ""}
                  </ListItem>
                ))}
              </List>
            </nav>
            <div aria-hidden="true">
              <Stepper current={step} steps={STEPS} orientation="vertical" className="nsi-stepper-desktop" />
              <Stepper current={step} steps={STEPS} orientation="horizontal" className="nsi-stepper-mobile" />
            </div>
            <div className="nsi-rail-footer">
              <Text variant="muted" size="sm">{version}</Text>
              <Text variant="muted" size="sm">Encrypted-by-default</Text>
            </div>
          </aside>
          <main id="installer-main" className="nsi-main" aria-labelledby="installer-step-title" aria-busy={installing || undefined}>
            <Text className="sr-only" aria-live="polite" aria-atomic="true">
              {helloError ? viewLabel : `Step ${step + 1} of ${STEPS.length}: ${viewLabel}`}
            </Text>
            {helloError ? (
              <div>
                <StepHeader title="Installer unavailable" />
                <Alert variant="error" style={{ marginTop: 20 }}>Failed to reach Admin Setup: {helloError}</Alert>
              </div>
            ) : body}
          </main>
        </div>
      </div>
    </>
  );
}
