import { Heading, Stack, Text } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";

export function Databases({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.databases, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Text variant="muted">
            {data.hosted ? "Hosted deployment (multi-database)." : "Single-database deployment."}
          </Text>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Storage</Heading>
            <ResultTable result={data.storage} />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Realms</Heading>
            <ResultTable result={data.realms} empty="No realms (deployment is not multi-realm)" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Databases</Heading>
            <ResultTable result={data.databases} empty="Single-database deployment" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Tables</Heading>
            <ResultTable result={data.tables} empty="No user tables" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Table statistics</Heading>
            <ResultTable result={data.table_stats} empty="No statistics collected yet" />
          </Stack>
        </>
      ) : null}
    </ViewFrame>
  );
}
