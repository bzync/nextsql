import { Heading, Inline, Stack, Stat } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";

export function Activity({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.activity, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <Inline gap="md" wrap>
            <Stat label="Sessions" value={String(data.sessions.rows.length)} />
            <Stat label="Active queries" value={String(data.active_queries.rows.length)} />
            <Stat label="Transactions" value={String(data.transactions.rows.length)} />
            <Stat label="Locks" value={String(data.locks.rows.length)} />
          </Inline>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Sessions</Heading>
            <ResultTable result={data.sessions} />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Active queries</Heading>
            <ResultTable result={data.active_queries} empty="No queries running" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Transactions</Heading>
            <ResultTable result={data.transactions} empty="No open transactions" />
          </Stack>
          <Stack gap="xs">
            <Heading as="h3" size="sm">Locks</Heading>
            <ResultTable result={data.locks} empty="No locks held" />
          </Stack>
        </>
      ) : null}
    </ViewFrame>
  );
}
