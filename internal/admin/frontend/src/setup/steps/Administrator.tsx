import { useState } from "react";
import { Button, Card, CardBody, Checkbox, Input, Stack, Text } from "@bzync/rui";
import type { Params } from "../api";
import { StepHeader } from "../components/StepHeader";
import { passwordStrength } from "../util";

// fieldErrors is deliberately attached to the specific field it's about,
// not surfaced as one generic banner: the whole point is that "why is
// Continue disabled" must be obvious at a glance from whichever field is
// actually wrong (see the docs/design-installer-gui.md M1 note this step's
// rewrite added — a real gap in the original hand-written version, where a
// too-short password disabled the button with only a muted hint, easy to
// miss, as the reason). Leaving both fields blank is always allowed (an
// admin account is optional): these only ever trigger once the operator
// has started typing something.
type FieldErrors = { user?: string; password?: string; confirm?: string };

function fieldErrors(p: Params, confirmVal: string): FieldErrors {
  const errors: FieldErrors = {};
  const production = p.profile === "production";
  const bothOrNeither = (p.adminUser === "") === (p.adminPassword === "");
  if (production && p.adminUser === "") {
    errors.user = "Production profile requires an administrator username.";
  } else if (production && p.adminPassword === "") {
    errors.password = "Production profile requires an administrator password.";
  } else if (!bothOrNeither) {
    if (p.adminUser === "") errors.user = "Enter a username, or clear the password below to skip this account.";
    else errors.password = "Enter a password, or clear the username above to skip this account.";
  } else if (p.adminPassword !== "" && p.adminPassword.length < 8) {
    errors.password = "Must be at least 8 characters.";
  }
  if (p.adminPassword !== "" && confirmVal === "") errors.confirm = "Confirm the password above.";
  else if (confirmVal !== "" && confirmVal !== p.adminPassword) errors.confirm = "Doesn't match the password above.";
  return errors;
}

export function Administrator({
  params, patch, onBack, onNext,
}: {
  params: Params;
  patch: (p: Partial<Params>) => void;
  onBack: () => void;
  onNext: () => void;
}) {
  const [confirmVal, setConfirmVal] = useState("");
  const [show, setShow] = useState(false);
  const pwType = show ? "text" : "password";
  const strength = passwordStrength(params.adminPassword);
  const errors = fieldErrors(params, confirmVal);
  const hasError = errors.user !== undefined || errors.password !== undefined || errors.confirm !== undefined;

  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader
          kicker="Step 4 of 6"
          title="Administrator account"
          description={
            params.profile === "production"
              ? "Required for the production profile: this account can connect and administer the new database immediately."
              : "Optional, but recommended: this account can connect and administer the new database immediately. Leave both fields below blank to skip it — nothing about this step is required."
          }
        />
        <Text variant="muted" size="sm" style={{ marginTop: 12 }}>
          This password is sent once, over this local connection, straight into a private file
          the installer reads and deletes — it is never written to a URL, a log, or uploaded
          anywhere.
        </Text>
        <Stack gap="sm" style={{ marginTop: 20 }}>
          <Input
            id="adminUser" label="Username" error={errors.user}
            value={params.adminUser} onChange={(e) => patch({ adminUser: e.target.value })}
          />
          <Input
            id="adminPassword" label="Password" error={errors.password}
            hint={!errors.password && params.adminPassword ? strength.label : undefined}
            type={pwType} value={params.adminPassword} autoComplete="new-password"
            onChange={(e) => patch({ adminPassword: e.target.value })}
          />
          <Input
            id="adminPasswordConfirm" label="Confirm password" error={errors.confirm}
            type={pwType} value={confirmVal} autoComplete="new-password"
            onChange={(e) => setConfirmVal(e.target.value)}
          />
          <Checkbox id="showPasswords" label="Show passwords" checked={show} onChange={(e) => setShow(e.target.checked)} />
        </Stack>
        <div className="nsi-actions">
          <Button variant="outline" onClick={onBack}>Back</Button>
          <Button variant="primary" disabled={hasError} onClick={onNext}>Continue</Button>
        </div>
      </CardBody>
    </Card>
  );
}
