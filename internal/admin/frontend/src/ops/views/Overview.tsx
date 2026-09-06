import { Heading, Inline, Stack, Stat } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";

export function Overview({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.overview, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Inline gap="md" wrap>
            <Stat label="Live sessions" value={String(data.sessions)} />
            <Stat label="Active queries" value={String(data.active_queries)} />
            <Stat label="Clustered" value={data.clustered ? "Yes" : "No"} />
          </Inline>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Storage</Heading>
            <ResultTable result={data.storage} />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Replication / HA</Heading>
            <ResultTable result={data.replication} />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Capabilities</Heading>
            <ResultTable result={data.capabilities} />
          </Stack>
        </>
      ) : null}
    </ViewFrame>
  );
}
