import { Button, Card, CardBody, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm, Stack, Text } from "@bzync/rui";
import type { Hello } from "../api";
import { StepHeader } from "../components/StepHeader";

export function Welcome({ hello, onNext }: { hello: Hello | null; onNext: () => void }) {
  return (
    <Card variant="bordered">
      <CardBody>
        <StepHeader
          level="h1" size="lg"
          kicker="First-run installer"
          title="NextSQL Setup"
          description="This wizard creates a new, encrypted-by-default NextSQL database on this machine. Nothing is written to disk until you confirm the summary screen."
        />
        <Stack gap="sm" style={{ marginTop: 20 }}>
          {hello ? (
            <DescriptionList columns={2} density="compact">
              <DescriptionItem>
                <DescriptionTerm>Version</DescriptionTerm>
                <DescriptionDetails>{hello.nextsql_version}</DescriptionDetails>
              </DescriptionItem>
              <DescriptionItem>
                <DescriptionTerm>Platform</DescriptionTerm>
                <DescriptionDetails>{hello.defaults.os}{hello.defaults.elevated ? " (elevated)" : ""}</DescriptionDetails>
              </DescriptionItem>
            </DescriptionList>
          ) : (
            <Text variant="muted">Detecting…</Text>
          )}
        </Stack>
        <div className="nsi-actions">
          <Button variant="primary" onClick={onNext}>Get started</Button>
        </div>
      </CardBody>
    </Card>
  );
}
