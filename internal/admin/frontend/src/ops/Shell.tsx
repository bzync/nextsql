import { useCallback, useEffect, useState } from "react";
import {
  AppShell,
  AppShellBody,
  AppShellMain,
  Avatar,
  Badge,
  Breadcrumb,
  Button,
  Container,
  IconButton,
  Inline,
  Kbd,
  Link,
  NavigationLink,
  PageHeader,
  ScrollArea,
  Separator,
  StatusDot,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Tooltip,
  Topbar,
  TopbarTitle,
} from "@bzync/rui";
import type { Whoami } from "./api";
import { useServerConnection } from "./useServerConnection";
import { AdminSearchModal } from "./AdminSearchModal";
import { UserSettingsModal } from "./UserSettingsModal";
import { useUserPreferences } from "./userPreferences";
import { Mark, Wordmark } from "../shared/Brand";
import { Icon, type IconName } from "../shared/icons";
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

type ViewId =
  | "overview"
  | "databases"
  | "activity"
  | "security"
  | "cluster"
  | "backups"
  | "maintenance"
  | "configuration"
  | "diagnostics"
  | "studio";

type ViewMeta = {
  id: ViewId;
  label: string;
  icon: IconName;
  description: string;
};

const VIEWS: Record<ViewId, ViewMeta> = {
  overview: { id: "overview", label: "Overview", icon: "overview", description: "Sessions, storage, and cluster posture for this node." },
  activity: { id: "activity", label: "Activity", icon: "activity", description: "Live sessions, running statements, transactions, and locks." },
  databases: { id: "databases", label: "Databases", icon: "database", description: "Databases, tables, and collected statistics." },
  studio: { id: "studio", label: "Studio", icon: "studio", description: "Query editor, schema explorer, and result inspector." },
  cluster: { id: "cluster", label: "Cluster", icon: "cluster", description: "Replication health and leader, drain, and maintenance actions." },
  backups: { id: "backups", label: "Backups", icon: "archive", description: "Verified backups and the offline restore command." },
  maintenance: { id: "maintenance", label: "Maintenance", icon: "wrench", description: "ANALYZE, index rebuild, and storage reclamation." },
  security: { id: "security", label: "Security", icon: "shield", description: "Users, roles, grants, TLS, keys, and the audit chain." },
  configuration: { id: "configuration", label: "Configuration", icon: "sliders", description: "Running nextsql.conf values. Edits apply on the next restart." },
  diagnostics: { id: "diagnostics", label: "Diagnostics", icon: "diagnostics", description: "Process metrics, server-log tail, and diagnostic bundle." },
};

const NAV_GROUPS: { id: string; label: string; icon: IconName; items: ViewId[] }[] = [
  { id: "monitor", label: "Monitor", icon: "pulse", items: ["overview", "activity"] },
  { id: "data", label: "Data", icon: "database", items: ["databases", "studio"] },
  { id: "platform", label: "Platform", icon: "cluster", items: ["cluster", "backups", "maintenance"] },
  { id: "admin", label: "Admin", icon: "shield", items: ["security", "configuration", "diagnostics"] },
];

const NAV_ITEMS: ViewMeta[] = NAV_GROUPS.flatMap((group) => group.items.map((id) => VIEWS[id]));

const VIEW_LABELS = Object.fromEntries(Object.values(VIEWS).map((view) => [view.id, view.label])) as Record<string, string>;

// Per-viewer convenience only: whether the vertical rail is collapsed. Never a
// credential or any server state — safe in localStorage, and every access is
// guarded because a private window or locked-down browser throws on read.
const SIDEBAR_KEY = "nextsql-admin-sidebar-collapsed";

function readSidebarCollapsed(): boolean {
  try {
    return window.localStorage.getItem(SIDEBAR_KEY) === "1";
  } catch {
    return false;
  }
}

function writeSidebarCollapsed(collapsed: boolean) {
  try {
    window.localStorage.setItem(SIDEBAR_KEY, collapsed ? "1" : "0");
  } catch {
    /* storage unavailable — the toggle still works for this session */
  }
}

function PanelIcon({ collapsed }: { collapsed: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <rect x="3" y="4" width="18" height="16" rx="2" stroke="currentColor" strokeWidth="1.8" />
      <line x1="9" y1="4" x2="9" y2="20" stroke="currentColor" strokeWidth="1.8" />
      {!collapsed ? <rect x="3.9" y="4.9" width="4.2" height="14.2" rx="1" fill="currentColor" /> : null}
    </svg>
  );
}

export function Shell({
  who,
  onSignOut,
  onConnectionChanged,
}: {
  who: Whoami;
  onSignOut: () => void;
  onConnectionChanged: (next: { realm: string; database: string }) => void;
}) {
  const hashRouter = useHashRoute("overview");
  const tab = (VIEW_LABELS[hashRouter.section] ? hashRouter.section : "overview") as ViewId;
  const setRoute = hashRouter.navigate;
  const [searchOpen, setSearchOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const view = VIEWS[tab];
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey((key) => key + 1), []);
  const onUnauthorized = onSignOut;
  const { status: serverConnection, checking: connectionChecking, refresh: refreshConnection } = useServerConnection(onUnauthorized);
  const { preferences } = useUserPreferences();

  const [sidebarCollapsed, setSidebarCollapsed] = useState(readSidebarCollapsed);
  const toggleSidebar = useCallback(() => {
    setSidebarCollapsed((current) => {
      const next = !current;
      writeSidebarCollapsed(next);
      return next;
    });
  }, []);

  useEffect(() => {
    if (preferences.autoRefreshInterval > 0) {
      const timer = setInterval(() => {
        refresh();
      }, preferences.autoRefreshInterval * 1000);
      return () => clearInterval(timer);
    }
  }, [preferences.autoRefreshInterval, refresh]);

  useEffect(() => {
    const onKey = (event: globalThis.KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && !event.shiftKey) {
        if (event.key.toLowerCase() === "k") {
          if (tab !== "studio") {
            event.preventDefault();
            setSearchOpen((open) => !open);
          }
        } else if (event.key === ",") {
          event.preventDefault();
          setSettingsOpen((open) => !open);
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tab]);

  useEffect(() => {
    document.title = `${view.label} · NextSQL Admin`;
  }, [view.label]);

  return (
    <>
      <Link className="nsa-skip" href="#admin-main">Skip to Admin content</Link>
      <Tabs defaultValue="overview" value={tab} onValueChange={setRoute} orientation="horizontal">
        <AppShell fixed className={`nsm-shell${sidebarCollapsed ? " nsm-shell--icons" : ""}`}>
          <aside className={`nsm-sidebar${sidebarCollapsed ? " nsm-sidebar--icons" : ""}`}>
            <div className="nsm-sidebar-header">
              {sidebarCollapsed ? (
                <Mark size={22} decorative />
              ) : (
                <Wordmark />
              )}
            </div>
            <div className="nsm-sidebar-search-wrap">
              <button
                type="button"
                className="nsm-sidebar-search-trigger"
                onClick={() => setSearchOpen(true)}
                aria-label="Search NextSQL Admin (Ctrl+K)"
                title="Search NextSQL Admin (Ctrl+K)"
              >
                <Icon name="search" size={14} />
                {!sidebarCollapsed ? <span>Search…</span> : null}
                {!sidebarCollapsed ? <Kbd keys={["Ctrl", "K"]} size="sm" /> : null}
              </button>
            </div>
            <ScrollArea className="nsm-sidebar-nav" orientation="vertical" keyboardNavigable={false}>
              <nav aria-label="Operations">
                {NAV_GROUPS.map((group, index) => (
                  <div key={group.id} className="nsm-nav-group">
                    {index > 0 ? <Separator /> : null}
                    <p className="nsm-nav-group-label">
                      <Icon name={group.icon} size={12} className="nsm-nav-group-icon" />
                      <span>{group.label}</span>
                    </p>
                    {group.items.map((id) => {
                      const item = (
                        <NavigationLink
                          key={id}
                          id={id}
                          className="nsm-nav-item"
                          label={VIEWS[id].label}
                          icon={<Icon name={VIEWS[id].icon} size={18} className="nsm-nav-icon" />}
                          active={tab === id}
                          onSelect={setRoute}
                        />
                      );
                      return sidebarCollapsed ? (
                        <Tooltip key={id} content={VIEWS[id].label} position="right">
                          {item}
                        </Tooltip>
                      ) : item;
                    })}
                  </div>
                ))}
              </nav>
            </ScrollArea>
            <div className="nsm-sidebar-footer">
              <button
                type="button"
                className="nsm-sidebar-user-btn"
                onClick={() => setSettingsOpen(true)}
                title={`Signed in as ${who.user}. Click to open User Settings`}
                aria-label={`Signed in as ${who.user}. Click to open User Settings`}
              >
                <Avatar name={who.user} size="sm" status="online" className="shrink-0" />
                {!sidebarCollapsed ? (
                  <div className="nsm-sidebar-user-text">
                    <span className="nsm-sidebar-user-name">{who.user}</span>
                    <span className="nsm-sidebar-user-sub font-mono">{who.database || "default"}</span>
                  </div>
                ) : null}
                {!sidebarCollapsed ? (
                  <Icon name="settings" size={14} className="nsm-sidebar-user-gear" />
                ) : null}
              </button>
              <div className="nsm-sidebar-footer-actions">
                {sidebarCollapsed ? (
                  <>
                    <Tooltip content="User settings" position="right">
                      <IconButton label="User settings" onClick={() => setSettingsOpen(true)}>
                        <Icon name="settings" size={16} />
                      </IconButton>
                    </Tooltip>
                    <Tooltip content="Sign out" position="right">
                      <IconButton label="Sign out" onClick={onSignOut}>
                        <Icon name="logout" size={16} />
                      </IconButton>
                    </Tooltip>
                  </>
                ) : (
                  <Inline gap="xs" align="center" className="w-full">
                    <Button
                      variant="outline"
                      size="sm"
                      icon={<Icon name="settings" size={14} />}
                      onClick={() => setSettingsOpen(true)}
                      className="flex-1 justify-center"
                    >
                      Settings
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      icon={<Icon name="logout" size={14} />}
                      onClick={onSignOut}
                      title="Sign out"
                      aria-label="Sign out"
                    />
                  </Inline>
                )}
              </div>
            </div>
          </aside>
          <AppShellBody className="nsm-body">
            <Topbar className="nsm-topbar">
              <Inline gap="sm" align="center" wrap={false}>
                <IconButton
                  className="nsm-sidebar-toggle"
                  label={sidebarCollapsed ? "Expand navigation sidebar" : "Collapse navigation to icons"}
                  aria-pressed={sidebarCollapsed}
                  onClick={toggleSidebar}
                >
                  <PanelIcon collapsed={sidebarCollapsed} />
                </IconButton>
                <TopbarTitle as="div" className="nsm-topbar-title">
                  {view.label}
                </TopbarTitle>
              </Inline>
              <button
                type="button"
                className="nsm-topbar-search"
                onClick={() => setSearchOpen(true)}
                aria-label="Search NextSQL Admin (Ctrl+K)"
              >
                <Icon name="search" size={14} />
                <span>Search…</span>
                <Kbd keys={["Ctrl", "K"]} size="sm" className="nsm-search-kbd" />
              </button>
              <Inline gap="sm" align="center" wrap={false}>
                {serverConnection && !serverConnection.connected ? (
                  <StatusDot status="offline" label="nextsqld unreachable" />
                ) : null}
                <button
                  type="button"
                  className="nsm-topbar-profile-btn"
                  onClick={() => setSettingsOpen(true)}
                  title={`Signed in as ${who.user}. Click to open User Settings`}
                  aria-label={`User Settings: ${who.user}`}
                >
                  <Avatar name={who.user} size="xs" status="online" className="shrink-0" />
                  <span className="nsm-topbar-profile-user font-mono">{who.user}</span>
                  <Badge variant="muted" size="sm" className="hidden xl:inline-flex font-mono">
                    {who.database || "default"}
                  </Badge>
                  <Icon name="settings" size={13} className="text-muted-foreground ml-1" />
                </button>
                <Tooltip content="User settings">
                  <IconButton label="User settings" onClick={() => setSettingsOpen(true)}>
                    <Icon name="settings" size={16} />
                  </IconButton>
                </Tooltip>
                <ThemeSelect />
                <Button
                  className="nsm-topbar-signout"
                  variant="outline"
                  size="sm"
                  icon={<Icon name="logout" size={14} />}
                  onClick={onSignOut}
                >
                  Sign out
                </Button>
              </Inline>
            </Topbar>
            <TabsList className="nsm-mobile-nav" aria-label="Operations">
              {NAV_ITEMS.map((item) => (
                <TabsTrigger key={item.id} value={item.id} className="nsm-mobile-tab">
                  <Icon name={item.icon} size={15} />
                  <span>{item.label}</span>
                </TabsTrigger>
              ))}
            </TabsList>
            <AppShellMain id="admin-main" scrollable className="nsm-main" aria-label={`${view.label} — NextSQL Admin`}>
              <Container size="full" className="nsm-content">
                <PageHeader
                  breadcrumbs={(
                    <Breadcrumb
                      items={[
                        { label: "NextSQL Admin" },
                        { label: tab === "studio" ? "Studio" : "Operations" },
                        { label: view.label, icon: <Icon name={view.icon} size={14} /> },
                      ]}
                    />
                  )}
                  eyebrow={tab === "studio" ? "Studio" : "Operations"}
                  title={<span className="nsm-page-title"><Icon name={view.icon} size={20} />{view.label}</span>}
                  description={view.description}
                  actions={(
                    <Inline gap="sm">
                      <Button variant="ghost" size="sm" icon={<Icon name="refresh" size={14} />} onClick={refresh}>Refresh</Button>
                      <Button variant="ghost" size="sm" icon={<Icon name="settings" size={14} />} onClick={() => setSettingsOpen(true)}>Settings</Button>
                      <Button className="nsm-mobile-signout" variant="outline" size="sm" icon={<Icon name="logout" size={14} />} onClick={onSignOut}>Sign out</Button>
                    </Inline>
                  )}
                />
                <TabsContent value="overview">
                  <Overview key={`o-${refreshKey}`} onUnauthorized={onUnauthorized} />
                </TabsContent>
                <TabsContent value="databases">
                  <Databases key={`d-${refreshKey}`} who={who} onUnauthorized={onUnauthorized} />
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
                    serverConnection={serverConnection}
                    connectionChecking={connectionChecking}
                    onRetryConnection={refreshConnection}
                    routeTable={hashRouter.params.table}
                    onSelectRouteTable={(table) => {
                      hashRouter.setParam("table", table);
                    }}
                  />
                </TabsContent>
              </Container>
            </AppShellMain>
          </AppShellBody>
        </AppShell>
      </Tabs>
      <AdminSearchModal
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        onNavigate={(target) => {
          setRoute(target);
        }}
        who={who}
        onSignOut={onSignOut}
        onOpenSettings={() => setSettingsOpen(true)}
      />
      <UserSettingsModal
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        who={who}
        onSignOut={onSignOut}
      />
    </>
  );
}
