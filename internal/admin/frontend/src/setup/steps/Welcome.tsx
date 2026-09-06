import { Button, Card, CardBody, DescriptionDetails, DescriptionItem, DescriptionList, DescriptionTerm, Inline, Stack, Text } from "@bzync/rui";
import type { Hello } from "../api";
import { StepHeader } from "../components/StepHeader";

export function Welcome({ hello, onNext }: { hello: Hello | null; onNext: () => void }) {
  return (
    <Card variant="elevated">
      <CardBody>
        <StepHeader
          level="h1" size="lg"
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
        <Inline justify="end" style={{ marginTop: 20 }}>
          <Button variant="primary" onClick={onNext}>Get started</Button>
        </Inline>
      </CardBody>
    </Card>
  );
}
