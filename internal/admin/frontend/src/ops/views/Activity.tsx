import { Badge, Inline, Stat, Tabs, TabsContent, TabsList, TabsTrigger } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import { StatGrid } from "./Section";
import { Icon } from "../../shared/icons";

export function Activity({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.activity, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <StatGrid>
            <Stat icon={<Icon name="users" />} label="Sessions" value={String(data.sessions.rows.length)} />
            <Stat icon={<Icon name="activity" />} label="Active queries" value={String(data.active_queries.rows.length)} />
            <Stat icon={<Icon name="clock" />} label="Transactions" value={String(data.transactions.rows.length)} />
            <Stat icon={<Icon name="lock" />} label="Locks" value={String(data.locks.rows.length)} />
          </StatGrid>

          <Tabs defaultValue="sessions" className="mt-4">
            <TabsList className="mb-4">
              <TabsTrigger value="sessions">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="users" size={14} />
                  <span>Sessions</span>
                  <Badge variant="muted" size="sm">{data.sessions.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="queries">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="activity" size={14} />
                  <span>Active queries</span>
                  <Badge variant={data.active_queries.rows.length ? "warning" : "muted"} size="sm">
                    {data.active_queries.rows.length}
                  </Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="transactions">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="clock" size={14} />
                  <span>Transactions</span>
                  <Badge variant="muted" size="sm">{data.transactions.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="locks">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="lock" size={14} />
                  <span>Locks</span>
                  <Badge variant={data.locks.rows.length ? "warning" : "muted"} size="sm">
                    {data.locks.rows.length}
                  </Badge>
                </Inline>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="sessions">
              <ResultTable result={data.sessions} label="Sessions" />
            </TabsContent>
            <TabsContent value="queries">
              <ResultTable result={data.active_queries} empty="No queries running" label="Active queries" />
            </TabsContent>
            <TabsContent value="transactions">
              <ResultTable result={data.transactions} empty="No open transactions" label="Open transactions" />
            </TabsContent>
            <TabsContent value="locks">
              <ResultTable result={data.locks} empty="No locks held" label="Held locks" />
            </TabsContent>
          </Tabs>
        </>
      ) : null}
    </ViewFrame>
  );
}
