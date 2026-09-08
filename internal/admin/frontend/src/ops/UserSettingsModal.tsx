import { useState } from "react";
import {
  Avatar,
  Badge,
  Button,
  Card,
  CardBody,
  Inline,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  ModalTitle,
  ModalDescription,
  Stack,
  StatusDot,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Text,
} from "@bzync/rui";
import { Icon } from "../shared/icons";
import { ThemeSelect } from "../shared/ThemeSelect";
import type { Whoami } from "./api";
import { useUserPreferences } from "./userPreferences";

export interface UserSettingsModalProps {
  open: boolean;
  onClose: () => void;
  who: Whoami;
  onSignOut: () => void;
  onSwitchConnection?: () => void;
}

export function UserSettingsModal({
  open,
  onClose,
  who,
  onSignOut,
  onSwitchConnection,
}: UserSettingsModalProps) {
  const [tab, setTab] = useState<string>("profile");
  const { preferences, updatePreferences } = useUserPreferences();
  const [copied, setCopied] = useState(false);

  const copyDiagnosticInfo = async () => {
    const diagnostic = {
      user: who.user,
      realm: who.realm || "default",
      database: who.database || "default",
      timestamp: new Date().toISOString(),
      userAgent: typeof navigator !== "undefined" ? navigator.userAgent : "unknown",
      density: preferences.density,
      pageSize: preferences.defaultPageSize,
      autoRefresh: preferences.autoRefreshInterval,
    };
    try {
      await navigator.clipboard.writeText(JSON.stringify(diagnostic, null, 2));
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // clipboard access denied
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      scrollable
      title="User & Workspace Settings"
      description="Session identity, operator console preferences, and security parameters."
      icon={<Icon name="settings" size={20} />}
      ariaLabel="User & Workspace Settings modal"
    >
      <ModalHeader>
        <Inline gap="md" align="center">
          <Avatar name={who.user} size="md" status="online" />
          <Stack gap="xs">
            <ModalTitle as="h2">User & Workspace Settings</ModalTitle>
            <ModalDescription>
              Connected as <strong className="font-semibold text-foreground">{who.user}</strong> · Database: <code className="text-xs font-mono">{who.database || "default"}</code> · Realm: <code className="text-xs font-mono">{who.realm || "default"}</code>
            </ModalDescription>
          </Stack>
        </Inline>
      </ModalHeader>

      <ModalBody scrollable>
        <Tabs defaultValue="profile" value={tab} onValueChange={setTab}>
          <TabsList className="mb-4 overflow-x-auto">
            <TabsTrigger value="profile" icon={<Icon name="user" size={14} />}>
              Identity & Session
            </TabsTrigger>
            <TabsTrigger value="preferences" icon={<Icon name="sliders" size={14} />}>
              Console Preferences
            </TabsTrigger>
            <TabsTrigger value="security" icon={<Icon name="shield" size={14} />}>
              Security & Scope
            </TabsTrigger>
          </TabsList>

          <TabsContent value="profile">
            <Stack gap="md">
              <Card variant="bordered">
                <CardBody>
                  <Stack gap="sm">
                    <Text size="sm" weight="semibold">Authenticated Principal</Text>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-sm">
                      <div className="flex flex-col gap-1 p-2.5 rounded bg-surface-muted border border-border">
                        <span className="text-xs text-muted-foreground uppercase font-semibold tracking-wider">Username</span>
                        <span className="font-mono font-medium text-foreground">{who.user}</span>
                      </div>
                      <div className="flex flex-col gap-1 p-2.5 rounded bg-surface-muted border border-border">
                        <span className="text-xs text-muted-foreground uppercase font-semibold tracking-wider">Authorization Role</span>
                        <span className="inline-flex items-center gap-1.5 font-medium">
                          <Badge variant="info" size="sm">SUPERUSER / ADMIN</Badge>
                        </span>
                      </div>
                      <div className="flex flex-col gap-1 p-2.5 rounded bg-surface-muted border border-border">
                        <span className="text-xs text-muted-foreground uppercase font-semibold tracking-wider">Active Realm</span>
                        <span className="font-mono text-foreground">{who.realm || "default"}</span>
                      </div>
                      <div className="flex flex-col gap-1 p-2.5 rounded bg-surface-muted border border-border">
                        <span className="text-xs text-muted-foreground uppercase font-semibold tracking-wider">Active Database</span>
                        <span className="font-mono text-foreground">{who.database || "default"}</span>
                      </div>
                    </div>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Stack gap="sm">
                    <Text size="sm" weight="semibold">Session Diagnostics</Text>
                    <Text size="sm" variant="muted">
                      Export session parameters and client state for diagnostics or incident response.
                    </Text>
                    <Inline gap="sm" align="center">
                      <Button
                        variant="outline"
                        size="sm"
                        icon={<Icon name={copied ? "check" : "copy"} size={14} />}
                        onClick={copyDiagnosticInfo}
                      >
                        {copied ? "Copied diagnostic JSON" : "Copy Session Info"}
                      </Button>
                      {onSwitchConnection ? (
                        <Button
                          variant="outline"
                          size="sm"
                          icon={<Icon name="refresh" size={14} />}
                          onClick={() => {
                            onClose();
                            onSwitchConnection();
                          }}
                        >
                          Switch Realm / DB…
                        </Button>
                      ) : null}
                    </Inline>
                  </Stack>
                </CardBody>
              </Card>
            </Stack>
          </TabsContent>

          <TabsContent value="preferences">
            <Stack gap="md">
              <Card variant="bordered">
                <CardBody>
                  <Stack gap="md">
                    <div>
                      <Text size="sm" weight="semibold">Appearance & Color Theme</Text>
                      <Text size="xs" variant="muted">
                        Select your preferred interface theme. Automatically syncs with system appearance if set to System.
                      </Text>
                    </div>
                    <div className="max-w-xs">
                      <ThemeSelect />
                    </div>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Stack gap="md">
                    <div>
                      <Text size="sm" weight="semibold">Table Density & Formatting</Text>
                      <Text size="xs" variant="muted">
                        Compact density maximizes visible data for dense monitoring; comfortable provides spacious padding.
                      </Text>
                    </div>
                    <Inline gap="sm">
                      <Button
                        size="sm"
                        variant={preferences.density === "compact" ? "primary" : "outline"}
                        onClick={() => updatePreferences({ density: "compact" })}
                      >
                        Compact (Operator Default)
                      </Button>
                      <Button
                        size="sm"
                        variant={preferences.density === "comfortable" ? "primary" : "outline"}
                        onClick={() => updatePreferences({ density: "comfortable" })}
                      >
                        Comfortable
                      </Button>
                    </Inline>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Stack gap="md">
                    <div>
                      <Text size="sm" weight="semibold">Default Table Page Size</Text>
                      <Text size="xs" variant="muted">
                        Number of rows displayed per page across all catalog and operations tables.
                      </Text>
                    </div>
                    <Inline gap="xs" wrap>
                      {[10, 25, 50, 100].map((size) => (
                        <Button
                          key={size}
                          size="sm"
                          variant={preferences.defaultPageSize === size ? "primary" : "outline"}
                          onClick={() => updatePreferences({ defaultPageSize: size })}
                        >
                          {size} rows
                        </Button>
                      ))}
                    </Inline>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Stack gap="md">
                    <div>
                      <Text size="sm" weight="semibold">Monitor Auto-Refresh Interval</Text>
                      <Text size="xs" variant="muted">
                        Periodically refresh activity, query stats, and node health signals automatically.
                      </Text>
                    </div>
                    <Inline gap="xs" wrap>
                      {[
                        { label: "Off", value: 0 },
                        { label: "5s", value: 5 },
                        { label: "15s", value: 15 },
                        { label: "30s", value: 30 },
                        { label: "60s", value: 60 },
                      ].map((opt) => (
                        <Button
                          key={opt.value}
                          size="sm"
                          variant={preferences.autoRefreshInterval === opt.value ? "primary" : "outline"}
                          onClick={() => updatePreferences({ autoRefreshInterval: opt.value })}
                        >
                          {opt.label}
                        </Button>
                      ))}
                    </Inline>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Switch
                    label="Confirm destructive actions"
                    description="Prompt with verification modal before executing table DROP, index rebuilds, or server restart."
                    checked={preferences.confirmDestructive}
                    onCheckedChange={(checked) => updatePreferences({ confirmDestructive: checked })}
                  />
                </CardBody>
              </Card>
            </Stack>
          </TabsContent>

          <TabsContent value="security">
            <Stack gap="md">
              <Card variant="bordered">
                <CardBody>
                  <Stack gap="sm">
                    <Text size="sm" weight="semibold">Security Baseline & Cryptography</Text>
                    <div className="flex flex-col gap-2 text-sm">
                      <div className="flex items-center justify-between p-2 rounded bg-surface-muted border border-border">
                        <span className="text-foreground">Authentication Protocol</span>
                        <Badge variant="success" size="sm">Argon2id + Ed25519 Token</Badge>
                      </div>
                      <div className="flex items-center justify-between p-2 rounded bg-surface-muted border border-border">
                        <span className="text-foreground">Audit Trail Integrity</span>
                        <Inline gap="xs" align="center">
                          <StatusDot status="online" />
                          <span className="text-xs font-mono">NSAC Hash-Chain Active</span>
                        </Inline>
                      </div>
                      <div className="flex items-center justify-between p-2 rounded bg-surface-muted border border-border">
                        <span className="text-foreground">Session Protection</span>
                        <span className="text-xs font-mono text-muted-foreground">HTTP-only Cookie + CSRF</span>
                      </div>
                    </div>
                  </Stack>
                </CardBody>
              </Card>

              <Card variant="bordered">
                <CardBody>
                  <Stack gap="sm">
                    <Text size="sm" weight="semibold" className="text-error-600 dark:text-error-400">
                      Sign Out of Session
                    </Text>
                    <Text size="xs" variant="muted">
                      Terminate the current authenticated session cookie and return to sign-in.
                    </Text>
                    <div>
                      <Button
                        variant="outline"
                        size="sm"
                        icon={<Icon name="logout" size={14} />}
                        onClick={() => {
                          onClose();
                          onSignOut();
                        }}
                      >
                        Sign Out
                      </Button>
                    </div>
                  </Stack>
                </CardBody>
              </Card>
            </Stack>
          </TabsContent>
        </Tabs>
      </ModalBody>

      <ModalFooter>
        <Inline gap="sm" justify="between" className="w-full">
          <Text size="xs" variant="muted">
            Preferences save automatically to your browser storage.
          </Text>
          <Button variant="primary" size="sm" onClick={onClose}>
            Done
          </Button>
        </Inline>
      </ModalFooter>
    </Modal>
  );
}
