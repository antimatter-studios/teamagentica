import { useEffect, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { Activity, AlertTriangle, AtSign, CheckCircle2, ChevronRight, Layers, Users } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { useAuthStore } from "../stores/authStore";
import { usePluginStore } from "../stores/pluginStore";
import { useEventStore, type DebugEvent, type EventLogEntry } from "../stores/eventStore";
import { apiClient } from "../api/client";
import { parseCapabilities } from "@teamagentica/api-client";
import type { Plugin, AliasInfo } from "@teamagentica/api-client";

export default function Dashboard() {
  const { user, users } = useAuthStore(
    useShallow((s) => ({ user: s.user, users: s.users }))
  );
  const logout = useAuthStore((s) => s.logout);
  const fetchUsers = useAuthStore((s) => s.fetchUsers);

  const plugins = usePluginStore((s) => s.plugins);
  const fetchPlugins = usePluginStore((s) => s.fetch);

  const auditEvents = useEventStore((s) => s.auditEvents);
  const eventLogEvents = useEventStore((s) => s.eventLogEvents);
  const eventsConnected = useEventStore((s) => s.connected);

  const [aliases, setAliases] = useState<AliasInfo[]>([]);

  useEffect(() => {
    fetchUsers();
    fetchPlugins();
    apiClient.aliases.list()
      .then((aliases) => setAliases(aliases))
      .catch(() => {});
  }, []);

  if (!user) {
    return (
      <div className="flex h-full w-full items-center justify-center p-6">
        <Card className="flex flex-col items-center gap-3 p-6">
          <p className="text-sm text-destructive">Session expired or user not found</p>
          <Button onClick={logout}>Return to login</Button>
        </Card>
      </div>
    );
  }

  const isAdmin = user.role === "admin";
  const running = plugins.filter((p) => p.status === "running");
  const errored = plugins.filter((p) => p.status === "error" || p.status === "unhealthy");
  const recentEvents = auditEvents.slice(-20).reverse();

  return (
    <div className="h-full w-full overflow-auto p-4">
      <main className="flex w-full flex-col gap-6">
        {/* Summary stats */}
        <section>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <StatCard
              label="Plugins online"
              value={String(running.length)}
              sub={`${plugins.length} installed`}
              tone="success"
            />
            <StatCard
              label="Aliases"
              value={String(aliases.length)}
              sub={`across ${new Set(aliases.map((a) => a.plugin_id || "admin")).size} source${aliases.length === 1 ? "" : "s"}`}
              tone="accent"
            />
            {errored.length > 0 ? (
              <StatCard
                label="Errors"
                value={String(errored.length)}
                sub={errored.map((p) => p.name).join(", ")}
                tone="error"
              />
            ) : (
              <StatCard label="Status" value="OK" sub="no errors" tone="success" />
            )}
            <StatCard
              label="Events"
              value={eventsConnected ? "Live" : "Off"}
              sub={`${auditEvents.length} in buffer`}
              tone={eventsConnected ? "success" : "muted"}
            />
          </div>
        </section>

        {/* Plugin fleet */}
        {isAdmin && plugins.length > 0 && (
          <Section icon={<Layers className="h-4 w-4" />} title="Plugin fleet">
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {plugins.map((p) => (
                <PluginCard key={p.id} plugin={p} />
              ))}
            </div>
          </Section>
        )}

        {/* Active aliases */}
        {aliases.length > 0 && (
          <Section icon={<AtSign className="h-4 w-4" />} title="Active aliases" count={aliases.length}>
            <div className="flex flex-wrap gap-2">
              {aliases.map((a) => (
                <Card key={a.name} className="flex flex-row items-center gap-2 p-2">
                  <span className="font-mono text-sm font-medium">@{a.name}</span>
                  <ChevronRight className="h-3 w-3 text-muted-foreground" />
                  <span className="text-sm text-muted-foreground">{a.target}</span>
                  {a.capabilities && a.capabilities.length > 0 && (
                    <span className="flex flex-wrap gap-1">
                      {a.capabilities.map((c) => (
                        <Badge key={c} variant="secondary" className="text-[10px]">
                          {c}
                        </Badge>
                      ))}
                    </span>
                  )}
                </Card>
              ))}
            </div>
          </Section>
        )}

        {/* Two-column: Activity + Event Log */}
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <Section
            icon={<Activity className="h-4 w-4" />}
            title="Recent activity"
            live={eventsConnected}
          >
            {recentEvents.length === 0 ? (
              <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
                No events yet.
              </div>
            ) : (
              <ScrollArea className="h-80 rounded-md border">
                <div className="flex flex-col divide-y">
                  {recentEvents.map((evt, i) => (
                    <EventRow key={i} event={evt} />
                  ))}
                </div>
              </ScrollArea>
            )}
          </Section>

          {isAdmin && (
            <Section
              icon={<ChevronRight className="h-4 w-4" />}
              title="Event log"
              count={eventLogEvents.length}
              live={eventsConnected}
            >
              {eventLogEvents.length === 0 ? (
                <div className="rounded-md border border-dashed p-6 text-center text-sm text-muted-foreground">
                  No inter-plugin events recorded yet.
                </div>
              ) : (
                <ScrollArea className="h-80 rounded-md border">
                  <div className="flex flex-col divide-y">
                    {eventLogEvents.slice(0, 50).map((entry) => (
                      <EventLogRow key={entry.id} entry={entry} />
                    ))}
                  </div>
                </ScrollArea>
              )}
            </Section>
          )}
        </div>

        {/* Operators */}
        {isAdmin && users.length > 0 && (
          <Section icon={<Users className="h-4 w-4" />} title="Operators" count={users.length}>
            <div className="flex flex-wrap gap-2">
              {users.map((u) => (
                <Card key={u.id} className="flex flex-row items-center gap-2 p-2">
                  <span className="text-sm">{u.display_name || u.email}</span>
                  <Badge variant={u.role === "admin" ? "default" : "secondary"} className="uppercase">
                    {u.role}
                  </Badge>
                </Card>
              ))}
            </div>
          </Section>
        )}
      </main>
    </div>
  );
}

function Section({
  icon,
  title,
  count,
  live,
  children,
}: {
  icon: React.ReactNode;
  title: string;
  count?: number;
  live?: boolean;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="flex items-center gap-2 text-sm font-semibold uppercase tracking-wide text-muted-foreground">
        {icon}
        {title}
        {count !== undefined && <Badge variant="secondary">{count}</Badge>}
        {live && <span className="ml-auto h-2 w-2 animate-pulse rounded-full bg-emerald-500" />}
      </h2>
      {children}
    </section>
  );
}

const TONE_CLASSES: Record<string, { border: string; text: string; icon: React.ReactNode }> = {
  success: { border: "border-emerald-500/40", text: "text-emerald-500", icon: <CheckCircle2 className="h-4 w-4" /> },
  error: { border: "border-destructive/40", text: "text-destructive", icon: <AlertTriangle className="h-4 w-4" /> },
  accent: { border: "border-primary/40", text: "text-primary", icon: <Activity className="h-4 w-4" /> },
  muted: { border: "border-muted", text: "text-muted-foreground", icon: <Activity className="h-4 w-4" /> },
};

function StatCard({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub: string;
  tone: "success" | "error" | "accent" | "muted";
}) {
  const t = TONE_CLASSES[tone];
  return (
    <Card className={cn("border-l-4 p-4", t.border)}>
      <CardHeader className="flex flex-row items-center justify-between p-0 pb-2 space-y-0">
        <CardTitle className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {label}
        </CardTitle>
        <span className={t.text}>{t.icon}</span>
      </CardHeader>
      <CardContent className="p-0">
        <div className={cn("text-2xl font-bold", t.text)}>{value}</div>
        <div className="mt-1 truncate text-xs text-muted-foreground">{sub}</div>
      </CardContent>
    </Card>
  );
}

function PluginCard({ plugin }: { plugin: Plugin }) {
  const caps = parseCapabilities(plugin);
  const dotClass = (() => {
    switch (plugin.status) {
      case "running": return "bg-emerald-500";
      case "starting": return "bg-amber-500 animate-pulse";
      case "error":
      case "unhealthy": return "bg-destructive";
      default: return "bg-muted-foreground";
    }
  })();

  return (
    <Card className="p-3">
      <div className="flex items-center gap-2">
        <span className={cn("h-2 w-2 rounded-full", dotClass)} />
        <span className="flex-1 truncate text-sm font-medium">{plugin.name}</span>
        <span className="text-xs text-muted-foreground">v{plugin.version}</span>
      </div>
      {caps.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {caps.slice(0, 3).map((c) => (
            <Badge key={c} variant="secondary" className="text-[10px]">{c}</Badge>
          ))}
        </div>
      )}
    </Card>
  );
}

function EventRow({ event }: { event: DebugEvent }) {
  const time = event.timestamp ? new Date(event.timestamp).toLocaleTimeString() : "";
  const tone = event.type?.includes("error")
    ? "text-destructive"
    : event.type === "dispatch"
    ? "text-primary"
    : event.type?.startsWith("alias")
    ? "text-amber-500"
    : "text-foreground";

  return (
    <div className="flex items-center gap-2 px-2 py-1 text-xs">
      <span className="w-20 shrink-0 font-mono text-muted-foreground">{time}</span>
      <span className={cn("font-mono font-medium", tone)}>{event.type}</span>
      {event.plugin_id && (
        <Badge variant="outline" className="text-[10px]">{event.plugin_id}</Badge>
      )}
      {event.detail && (
        <span className="truncate text-muted-foreground">{event.detail.slice(0, 80)}</span>
      )}
    </div>
  );
}

const STATUS_VARIANT: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  delivered: "default",
  dispatched: "default",
  queued: "secondary",
  failed: "destructive",
  evicted: "outline",
};

function EventLogRow({ entry }: { entry: EventLogEntry }) {
  const time = entry.created_at ? new Date(entry.created_at).toLocaleTimeString() : "";
  return (
    <div className="flex items-center gap-2 px-2 py-1 text-xs">
      <span className="w-20 shrink-0 font-mono text-muted-foreground">{time}</span>
      <span className="font-mono font-medium">{entry.event_type}</span>
      <span className="flex items-center gap-1">
        <span className="text-muted-foreground">{entry.source_plugin_id}</span>
        <ChevronRight className="h-3 w-3 text-muted-foreground" />
        <span className="text-muted-foreground">{entry.target_plugin_id}</span>
      </span>
      <Badge variant={STATUS_VARIANT[entry.status] || "outline"} className="text-[10px]">
        {entry.status}
      </Badge>
      {entry.detail && (
        <span className="truncate text-muted-foreground">{entry.detail.slice(0, 60)}</span>
      )}
    </div>
  );
}
