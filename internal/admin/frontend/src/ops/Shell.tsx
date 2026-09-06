import { useCallback, useEffect, useState } from "react";
import {
  AppShell,
  AppShellBody,
  AppShellMain,
  Button,
  Container,
  Inline,
  Link,
  PageHeader,
  Sidebar,
  Stack,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
  Topbar,
  TopbarTitle,
  type NavigationItem,
} from "@bzync/rui";
import type { Whoami } from "./api";
import { Wordmark } from "../shared/Brand";
import { ThemeSelect } from "../shared/ThemeSelect";
import { useHashRoute } from "../shared/router";
import { StudioWorkspace } from "../studio/StudioWorkspace";
import { Overview } from "./views/Overview";
import { Databases } from "./views/Databases";
import { Activity } from "./views/Activity";
import { Security } from "./views/Security";
import { Cluster } from "./views/Cluster";
import { Maintenance } from "./views/Maintenance";
import { Backups } from "./views/Backups";
import { Configuration } from "./views/Configuration";
import { Diagnostics } from "./views/Diagnostics";

const VIEW_LABELS: Record<string, string> = {
  overview: "Overview",
  databases: "Databases",
  activity: "Activity",
  security: "Security",
  cluster: "Cluster",
  backups: "Backups",
  maintenance: "Maintenance",
  configuration: "Configuration",
  diagnostics: "Diagnostics",
  studio: "Studio",
};

const NAV_ITEMS: NavigationItem[] = Object.entries(VIEW_LABELS).map(([id, label]) => ({ id, label }));

export function Shell({
  who,
  onSignOut,
  onConnectionChanged,
}: {
  who: Whoami;
  onSignOut: () => void;
  onConnectionChanged: (next: { realm: string; database: string }) => void;
}) {
  const [route, setRoute] = useHashRoute("overview");
  const tab = VIEW_LABELS[route] ? route : "overview";
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey((key) => key + 1), []);
  const onUnauthorized = onSignOut;

  const identity = [who.user, who.database && `db:${who.database}`, who.realm && `realm:${who.realm}`]
    .filter(Boolean)
    .join(" · ");

  useEffect(() => {
    document.title = `${VIEW_LABELS[tab]} · NextSQL Admin`;
  }, [tab]);

  return (
    <>
      <Link className="nsa-skip" href="#admin-main">Skip to Admin content</Link>
      <Tabs defaultValue="overview" value={tab} onValueChange={setRoute} orientation="horizontal">
        <AppShell fixed className="nsm-shell">
          <Sidebar
            className="nsm-sidebar"
            items={NAV_ITEMS}
            activeId={tab}
            onSelect={setRoute}
            ariaLabel="Operations"
            header={(
              <Stack gap="xs">
                <Wordmark />
                <Text variant="muted" size="sm">Operations workspace</Text>
              </Stack>
            )}
            footer={(
              <Stack gap="sm">
                <Text variant="muted" size="sm" title={identity}>{identity}</Text>
                <Button variant="outline" size="sm" onClick={onSignOut}>Sign out</Button>
              </Stack>
            )}
          />
          <AppShellBody className="nsm-body">
            <Topbar className="nsm-topbar">
              <TopbarTitle as="div">NextSQL Admin</TopbarTitle>
              <ThemeSelect />
            </Topbar>
            <TabsList className="nsm-mobile-nav" aria-label="Operations">
              {NAV_ITEMS.map((item) => (
                <TabsTrigger key={item.id} value={item.id}>{item.label}</TabsTrigger>
              ))}
            </TabsList>
            <AppShellMain id="admin-main" className="nsm-main" aria-label={`${VIEW_LABELS[tab]} — NextSQL Admin`}>
              <Container size="full" className="nsm-content">
                <PageHeader
                  title={VIEW_LABELS[tab]}
                  description={tab === "studio" ? "Database development workspace" : "Operations workspace"}
                  actions={(
                    <Inline gap="sm">
                      <Button variant="ghost" size="sm" onClick={refresh}>Refresh</Button>
                      <Button className="nsm-mobile-signout" variant="outline" size="sm" onClick={onSignOut}>Sign out</Button>
                    </Inline>
                  )}
                />
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
                <TabsContent value="studio">
                  <StudioWorkspace
                    key={`studio-${refreshKey}-${who.realm}-${who.database}`}
                    who={who}
                    onUnauthorized={onUnauthorized}
                    onConnectionChanged={onConnectionChanged}
                  />
                </TabsContent>
              </Container>
            </AppShellMain>
          </AppShellBody>
        </AppShell>
      </Tabs>
    </>
  );
}
