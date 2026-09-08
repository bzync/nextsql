import { Badge, Inline, Stat, Tabs, TabsContent, TabsList, TabsTrigger } from "@bzync/rui";
import { api } from "../api";
import { useReadModel } from "../useReadModel";
import { ResultTable } from "../ResultTable";
import { ViewFrame } from "./ViewFrame";
import { StatGrid } from "./Section";
import { Icon } from "../../shared/icons";

export function Overview({ onUnauthorized }: { onUnauthorized: () => void }) {
  const { data, error, loading } = useReadModel(api.overview, onUnauthorized);
  return (
    <ViewFrame loading={loading} error={error} warnings={data?.warnings}>
      {data ? (
        <>
          <StatGrid>
            <Stat icon={<Icon name="users" />} label="Live sessions" value={String(data.sessions)} />
            <Stat icon={<Icon name="activity" />} label="Active queries" value={String(data.active_queries)} />
            <Stat icon={<Icon name="cluster" />} label="Clustered" value={data.clustered ? "Yes" : "No"} />
          </StatGrid>

          <Tabs defaultValue="storage" className="mt-4">
            <TabsList className="mb-4">
              <TabsTrigger value="storage">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="hard-drive" size={14} />
                  <span>Storage</span>
                  <Badge variant="muted" size="sm">{data.storage.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="replication">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="cluster" size={14} />
                  <span>Replication / HA</span>
                  <Badge variant="muted" size="sm">{data.replication.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
              <TabsTrigger value="capabilities">
                <Inline gap="xs" align="center" wrap={false}>
                  <Icon name="layers" size={14} />
                  <span>Capabilities</span>
                  <Badge variant="muted" size="sm">{data.capabilities.rows.length}</Badge>
                </Inline>
              </TabsTrigger>
            </TabsList>

            <TabsContent value="storage">
              <ResultTable result={data.storage} label="Storage" />
            </TabsContent>
            <TabsContent value="replication">
              <ResultTable result={data.replication} label="Replication / HA" />
            </TabsContent>
            <TabsContent value="capabilities">
              <ResultTable result={data.capabilities} label="Capabilities" />
            </TabsContent>
          </Tabs>
        </>
      ) : null}
    </ViewFrame>
  );
}
