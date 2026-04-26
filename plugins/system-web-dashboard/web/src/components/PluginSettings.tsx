import { useEffect, useState, useCallback, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import { Loader2, RotateCw, Inbox } from "lucide-react";
import { apiClient } from "../api/client";
import { parseCapabilities } from "@teamagentica/api-client";
import type { Plugin, ConfigSchemaField } from "@teamagentica/api-client";
import { usePluginStore } from "../stores/pluginStore";
import PluginConfigForm from "./PluginConfigForm";
import PluginAliasPanel from "./PluginAliasPanel";
import PluginLogsInline from "./PluginLogsInline";
import PluginPricing from "./PluginPricing";
import PluginTools from "./PluginTools";
import PluginSystemPrompt from "./PluginSystemPrompt";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

type DetailTab = "config" | "aliases" | "logs" | "pricing" | "tools" | "system-prompt";

// Plugin group metadata — matches the catalog group ordering.
const GROUP_META: { id: string; name: string; order: number }[] = [
  { id: "agents", name: "AI Agents", order: 1 },
  { id: "messaging", name: "Messaging", order: 2 },
  { id: "tools", name: "Tools", order: 3 },
  { id: "storage", name: "Storage", order: 4 },
  { id: "network", name: "Network", order: 5 },
  { id: "infrastructure", name: "Infrastructure", order: 6 },
  { id: "system", name: "System", order: 7 },
  { id: "workspace", name: "Workspaces", order: 8 },
];

// Map plugin ID prefix → group ID.
const PREFIX_TO_GROUP: Record<string, string> = {
  "agent-": "agents",
  "messaging-": "messaging",
  "tool-": "tools",
  "storage-": "storage",
  "network-": "network",
  "infra-": "infrastructure",
  "workspace-": "workspace",
  "system-": "system",
};

function pluginGroup(p: Plugin): string {
  for (const [prefix, group] of Object.entries(PREFIX_TO_GROUP)) {
    if (p.id.startsWith(prefix)) return group;
  }
  return "other";
}

function groupedPlugins(plugins: Plugin[]): { id: string; name: string; plugins: Plugin[] }[] {
  const byGroup = new Map<string, Plugin[]>();
  for (const p of plugins) {
    const g = pluginGroup(p);
    if (!byGroup.has(g)) byGroup.set(g, []);
    byGroup.get(g)!.push(p);
  }

  const sections: { id: string; name: string; plugins: Plugin[] }[] = [];
  for (const gm of GROUP_META) {
    const entries = byGroup.get(gm.id);
    if (entries && entries.length > 0) {
      entries.sort((a, b) => a.name.localeCompare(b.name));
      sections.push({ id: gm.id, name: gm.name, plugins: entries });
      byGroup.delete(gm.id);
    }
  }
  // Any remaining groups not in metadata.
  for (const [id, entries] of byGroup) {
    entries.sort((a, b) => a.name.localeCompare(b.name));
    sections.push({ id, name: id.charAt(0).toUpperCase() + id.slice(1), plugins: entries });
  }
  return sections;
}

function statusDotClass(status: string, enabled: boolean): string {
  if (!enabled) return "bg-muted-foreground";
  switch (status) {
    case "running":
    case "enabled": return "bg-green-500";
    case "starting": return "bg-amber-500 animate-pulse";
    case "error":
    case "unhealthy": return "bg-red-500";
    default: return "bg-muted-foreground";
  }
}

function statusLabelClass(status: string, enabled: boolean): string {
  if (!enabled) return "text-muted-foreground";
  switch (status) {
    case "running":
    case "enabled": return "text-green-600 dark:text-green-400";
    case "starting": return "text-amber-600 dark:text-amber-400";
    case "error":
    case "unhealthy": return "text-red-600 dark:text-red-400";
    default: return "text-muted-foreground";
  }
}

interface Props {
  initialPluginId?: string;
  onPluginChange?: (pluginId: string) => void;
}

export default function PluginSettings({ initialPluginId, onPluginChange }: Props) {
  const { plugins, loading, error } = usePluginStore(
    useShallow((s) => ({ plugins: s.plugins, loading: s.loading, error: s.error }))
  );
  const fetch = usePluginStore((s) => s.fetch);
  const enable = usePluginStore((s) => s.enable);
  const disable = usePluginStore((s) => s.disable);
  const restart = usePluginStore((s) => s.restart);
  const uninstall = usePluginStore((s) => s.uninstall);
  const [actionError, setActionError] = useState("");
  const [selectedId, setSelectedId] = useState<string | null>(initialPluginId || null);
  const [confirmUninstall, setConfirmUninstall] = useState<string | null>(null);
  const [detailTab, setDetailTab] = useState<DetailTab>("config");
  const [hasPricing, setHasPricing] = useState(false);
  const [hasTools, setHasTools] = useState(false);
  const [hasSystemPrompt, setHasSystemPrompt] = useState(false);

  useEffect(() => {
    fetch();
  }, [fetch]);

  // Select plugin and sync URL.
  const selectPlugin = useCallback((id: string) => {
    setSelectedId(id);
    onPluginChange?.(id);
  }, [onPluginChange]);

  // Auto-select first plugin when list loads and nothing selected.
  useEffect(() => {
    if (plugins.length === 0) return;
    if (selectedId && plugins.find((p) => p.id === selectedId)) return;
    selectPlugin(plugins[0].id);
  }, [plugins, selectedId, selectPlugin]);

  const selected = plugins.find((p) => p.id === selectedId) || null;
  const groups = useMemo(() => groupedPlugins(plugins), [plugins]);

  // Fetch live config schema to check for aliases field.
  const [liveSchema, setLiveSchema] = useState<Record<string, ConfigSchemaField>>({});
  useEffect(() => {
    if (!selected || selected.status !== "running") {
      setLiveSchema({});
      return;
    }
    apiClient.plugins.getConfigSchema(selected.id).then(setLiveSchema).catch(() => setLiveSchema({}));
  }, [selected?.id, selected?.status]);

  const hasAliases = Object.values(liveSchema).some((f) => f.type === "aliases");

  // Probe pricing endpoint when selected plugin changes.
  const probePricing = useCallback(async (pluginId: string, status: string) => {
    if (status !== "running") {
      setHasPricing(false);
      return;
    }
    try {
      await apiClient.plugins.getPricing(pluginId);
      setHasPricing(true);
    } catch {
      setHasPricing(false);
    }
  }, []);

  // Probe tools endpoint when selected plugin changes.
  const probeTools = useCallback(async (pluginId: string, status: string) => {
    if (status !== "running") {
      setHasTools(false);
      return;
    }
    try {
      await apiClient.plugins.getTools(pluginId);
      setHasTools(true);
    } catch {
      setHasTools(false);
    }
  }, []);


  // Probe system-prompt endpoint when selected plugin changes.
  const probeSystemPrompt = useCallback(async (pluginId: string, status: string) => {
    if (status !== "running") {
      setHasSystemPrompt(false);
      return;
    }
    try {
      await apiClient.plugins.getSystemPrompt(pluginId);
      setHasSystemPrompt(true);
    } catch {
      setHasSystemPrompt(false);
    }
  }, []);

  useEffect(() => {
    if (selected) {
      const caps = parseCapabilities(selected);
      const isAgent = caps.some((c) => c.startsWith("agent:"));
      probeTools(selected.id, selected.status);
      if (isAgent) {
        probePricing(selected.id, selected.status);
        probeSystemPrompt(selected.id, selected.status);
      } else {
        setHasPricing(false);
        setHasSystemPrompt(false);
      }
      if ((detailTab === "pricing" || detailTab === "tools" || detailTab === "system-prompt") && selected.status !== "running") {
        setDetailTab("config");
      }
      if (detailTab === "aliases" && !hasAliases) {
        setDetailTab("config");
      }
    } else {
      setHasPricing(false);
      setHasTools(false);
      setHasSystemPrompt(false);
    }
  }, [selected?.id, selected?.status, probePricing, probeTools, probeSystemPrompt]);

  async function handleAction(action: () => Promise<void>) {
    setActionError("");
    try {
      await action();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Action failed");
    }
  }

  async function handleUninstall(id: string) {
    setConfirmUninstall(null);
    await handleAction(() => uninstall(id));
  }

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-16">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        <p className="text-sm text-muted-foreground">LOADING PLUGINS...</p>
      </div>
    );
  }

  const availableTabs: DetailTab[] = selected ? [
    "config",
    ...(hasAliases ? ["aliases" as DetailTab] : []),
    ...(hasPricing ? ["pricing" as DetailTab] : []),
    ...(hasTools ? ["tools" as DetailTab] : []),
    ...(hasSystemPrompt ? ["system-prompt" as DetailTab] : []),
    ...(selected.image ? ["logs" as DetailTab] : []),
  ] : [];

  return (
    <div className="grid grid-cols-[280px_1fr] gap-4 h-full">
      {/* Left sidebar */}
      <aside className="flex flex-col gap-1 border-r pr-3 overflow-y-auto">
        <div className="flex items-center gap-2 px-2 py-2 text-xs font-semibold tracking-wide text-muted-foreground">
          <span>PLUGINS</span>
          {plugins.length > 0 && (
            <Badge variant="secondary" className="ml-auto">{plugins.length}</Badge>
          )}
        </div>

        {plugins.length === 0 && !error && (
          <div className="px-2 py-4 text-sm text-muted-foreground">
            No plugins installed.
          </div>
        )}

        <nav className="flex flex-col gap-3">
          {groups.map((g) => (
            <div key={g.id} className="flex flex-col gap-0.5">
              <div className="flex items-center justify-between px-2 py-1 text-xs font-semibold tracking-wide text-muted-foreground">
                <span>{g.name}</span>
                <Badge variant="outline">{g.plugins.length}</Badge>
              </div>
              {g.plugins.map((p) => (
                <button
                  type="button"
                  key={p.id}
                  className={cn(
                    "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent text-left",
                    selectedId === p.id && "bg-accent text-accent-foreground",
                    !p.enabled && "opacity-60",
                    p.enabled && (p.status === "error" || p.status === "unhealthy") && "text-red-600 dark:text-red-400"
                  )}
                  onClick={() => {
                    selectPlugin(p.id);
                    setConfirmUninstall(null);
                    setActionError("");
                  }}
                >
                  <span className={cn("inline-block h-2 w-2 rounded-full shrink-0", statusDotClass(p.status, p.enabled))} />
                  <span className="flex-1 truncate">{p.name}</span>
                  {p.status === "running" && (
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-6 w-6"
                      title="Restart"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleAction(() => restart(p.id));
                      }}
                    >
                      <RotateCw className="h-3 w-3" />
                    </Button>
                  )}
                  <span className={cn("text-[10px] font-semibold tracking-wide", statusLabelClass(p.status, p.enabled))}>
                    {!p.enabled ? "DISABLED" : p.status.toUpperCase()}
                  </span>
                </button>
              ))}
            </div>
          ))}
        </nav>
      </aside>

      {/* Right content */}
      <main className="flex flex-col gap-4 min-w-0 overflow-y-auto">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {actionError && (
          <Alert variant="destructive">
            <AlertDescription>{actionError}</AlertDescription>
          </Alert>
        )}

        {!selected ? (
          <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
            <Inbox className="h-10 w-10 text-muted-foreground opacity-60" />
            <p className="text-sm text-muted-foreground">Select a plugin from the sidebar.</p>
          </div>
        ) : (
          <>
            {/* Plugin header */}
            <div className="flex flex-col gap-3">
              <div className="flex items-center gap-3 flex-wrap">
                <span className={cn("inline-block h-3 w-3 rounded-full", statusDotClass(selected.status, selected.enabled))} />
                <h2 className="text-xl font-semibold">{selected.name}</h2>
                <Badge variant="outline">v{selected.version}</Badge>
                <span className={cn("text-xs font-semibold tracking-wide", statusLabelClass(selected.status, selected.enabled))}>
                  {!selected.enabled ? "DISABLED" : selected.status.toUpperCase()}
                </span>
              </div>

              {selected.image && (
                <div className="text-xs text-muted-foreground font-mono">{selected.image}</div>
              )}

              {parseCapabilities(selected).length > 0 && (
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-xs font-semibold tracking-wide text-muted-foreground">CAPABILITIES</span>
                  {parseCapabilities(selected).map((cap) => (
                    <Badge variant="secondary" key={cap}>{cap}</Badge>
                  ))}
                </div>
              )}

              {/* Action buttons */}
              <div className="flex items-center gap-2 flex-wrap">
                <Button
                  variant={selected.enabled ? "outline" : "default"}
                  size="sm"
                  onClick={() =>
                    handleAction(() =>
                      selected.enabled ? disable(selected.id) : enable(selected.id)
                    )
                  }
                >
                  {selected.enabled ? "DISABLE" : "ENABLE"}
                </Button>

                {selected.status === "running" && (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => handleAction(() => restart(selected.id))}
                  >
                    RESTART
                  </Button>
                )}

                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => setConfirmUninstall(selected.id)}
                >
                  UNINSTALL
                </Button>
              </div>

              {confirmUninstall === selected.id && (
                <Alert variant="destructive">
                  <AlertDescription className="flex items-center justify-between gap-3 flex-wrap">
                    <span>Uninstall "{selected.name}"? This cannot be undone.</span>
                    <div className="flex gap-2">
                      <Button
                        variant="destructive"
                        size="sm"
                        onClick={() => handleUninstall(selected.id)}
                      >
                        CONFIRM
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setConfirmUninstall(null)}
                      >
                        CANCEL
                      </Button>
                    </div>
                  </AlertDescription>
                </Alert>
              )}
            </div>

            <Separator />

            {/* Content area */}
            <Tabs value={detailTab} onValueChange={(v) => setDetailTab(v as DetailTab)}>
              <TabsList>
                {availableTabs.map((tab) => (
                  <TabsTrigger key={tab} value={tab}>
                    {tab.toUpperCase()}
                  </TabsTrigger>
                ))}
              </TabsList>

              <TabsContent value="config" className="mt-4">
                <PluginConfigForm
                  key={selected.id}
                  plugin={selected}
                  onSaved={() => fetch()}
                />
              </TabsContent>
              <TabsContent value="aliases" className="mt-4">
                <PluginAliasPanel
                  key={selected.id}
                  plugin={selected}
                  onSaved={() => fetch()}
                />
              </TabsContent>
              <TabsContent value="pricing" className="mt-4">
                <PluginPricing
                  key={selected.id}
                  pluginId={selected.id}
                />
              </TabsContent>
              <TabsContent value="tools" className="mt-4">
                <PluginTools
                  key={selected.id}
                  pluginId={selected.id}
                />
              </TabsContent>
              <TabsContent value="system-prompt" className="mt-4">
                <PluginSystemPrompt
                  key={selected.id}
                  pluginId={selected.id}
                />
              </TabsContent>
              <TabsContent value="logs" className="mt-4">
                <PluginLogsInline
                  key={selected.id}
                  pluginId={selected.id}
                />
              </TabsContent>
            </Tabs>
          </>
        )}
      </main>
    </div>
  );
}
