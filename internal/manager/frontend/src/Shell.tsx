import { useCallback, useState } from "react";
import {
  AppShell,
  AppShellBody,
  AppShellHeader,
  AppShellMain,
  Button,
  Inline,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
} from "@bzync/rui";
import type { Whoami } from "./api";
import { Overview } from "./views/Overview";
import { Databases } from "./views/Databases";
import { Activity } from "./views/Activity";
import { Security } from "./views/Security";
import { Cluster } from "./views/Cluster";
import { Maintenance } from "./views/Maintenance";
import { Backups } from "./views/Backups";
import { Configuration } from "./views/Configuration";
import { Diagnostics } from "./views/Diagnostics";

export function Shell({ who, onSignOut }: { who: Whoami; onSignOut: () => void }) {
  const [tab, setTab] = useState("overview");
  // Remount a view on tab change / manual refresh so it re-fetches.
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);
  const onUnauthorized = onSignOut;

  const identity = [who.user, who.database && `db:${who.database}`, who.realm && `realm:${who.realm}`]
    .filter(Boolean)
    .join("  ·  ");

  return (
    <AppShell fixed>
      <AppShellBody>
        <AppShellHeader sticky>
          <Inline justify="between" align="center" style={{ width: "100%" }}>
            <Text as="span" style={{ fontWeight: 600 }}>NextSQL Manager</Text>
            <Inline gap="md" align="center">
              <Text variant="muted" size="sm">{identity}</Text>
              <Button variant="ghost" size="sm" onClick={refresh}>Refresh</Button>
              <Button variant="outline" size="sm" onClick={onSignOut}>Sign out</Button>
            </Inline>
          </Inline>
        </AppShellHeader>
        <AppShellMain scrollable>
          <div style={{ maxWidth: 1040, margin: "0 auto", padding: 20 }}>
            <Tabs defaultValue="overview" value={tab} onValueChange={setTab}>
              <TabsList>
                <TabsTrigger value="overview">Overview</TabsTrigger>
                <TabsTrigger value="databases">Databases</TabsTrigger>
                <TabsTrigger value="activity">Activity</TabsTrigger>
                <TabsTrigger value="security">Security</TabsTrigger>
                <TabsTrigger value="cluster">Cluster</TabsTrigger>
                <TabsTrigger value="backups">Backups</TabsTrigger>
                <TabsTrigger value="maintenance">Maintenance</TabsTrigger>
                <TabsTrigger value="configuration">Configuration</TabsTrigger>
                <TabsTrigger value="diagnostics">Diagnostics</TabsTrigger>
              </TabsList>
              <TabsContent value="overview">
                <Overview key={`o-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="databases">
                <Databases key={`d-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="activity">
                <Activity key={`a-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="security">
                <Security key={`s-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="cluster">
                <Cluster key={`c-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="backups">
                <Backups key={`bk-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="maintenance">
                <Maintenance key={`m-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="configuration">
                <Configuration key={`cfg-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
              <TabsContent value="diagnostics">
                <Diagnostics key={`diag-${refreshKey}`} onUnauthorized={onUnauthorized} />
              </TabsContent>
            </Tabs>
          </div>
        </AppShellMain>
      </AppShellBody>
    </AppShell>
  );
}
