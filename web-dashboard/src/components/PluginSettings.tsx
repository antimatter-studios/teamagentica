import { useEffect, useState, useCallback, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
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
import PluginDiscordCommands from "./PluginDiscordCommands";

type DetailTab = "config" | "aliases" | "logs" | "pricing" | "tools" | "system-prompt" | "commands";

// Plugin group metadata — matches the catalog group ordering.
const GROUP_META: { id: string; name: string; order: number }[] = [
  { id: "agents", name: "AI Agents", order: 1 },
  { id: "messaging", name: "Messaging", order: 2 },
  { id: "tools", name: "Tools", order: 3 },
  { id: "storage", name: "Storage", order: 4 },
  { id: "network", name: "Network", order: 5 },
  { id: "infrastructure", name: "Infrastructure", order: 6 },
  { id: "system", name: "System", order: 7 },
  { id: "user", name: "User", order: 8 },
];

// Map plugin ID prefix → group ID.
const PREFIX_TO_GROUP: Record<string, string> = {
  "agent-": "agents",
  "messaging-": "messaging",
  "tool-": "tools",
  "storage-": "storage",
  "network-": "network",
  "infra-": "infrastructure",
  "user-": "user",
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
      sections.push({ id: gm.id, name: gm.name, plugins: entries });
      byGroup.delete(gm.id);
    }
  }
  // Any remaining groups not in metadata.
  for (const [id, entries] of byGroup) {
    sections.push({ id, name: id.charAt(0).toUpperCase() + id.slice(1), plugins: entries });
  }
  return sections;
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
  const [hasCommands, setHasCommands] = useState(false);

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
    // Selected plugin missing or none selected — pick first.
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

  // Probe discord-commands endpoint when selected plugin changes.
  const probeCommands = useCallback(async (pluginId: string, status: string) => {
    if (status !== "running") {
      setHasCommands(false);
      return;
    }
    try {
      await apiClient.plugins.getDiscordCommands(pluginId);
      setHasCommands(true);
    } catch {
      setHasCommands(false);
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
      const isDiscord = caps.includes("messaging:discord");
      // Any running plugin can expose tools via /mcp.
      probeTools(selected.id, selected.status);
      if (isAgent) {
        probePricing(selected.id, selected.status);
        probeSystemPrompt(selected.id, selected.status);
      } else {
        setHasPricing(false);
        setHasSystemPrompt(false);
      }
      if (isDiscord) {
        probeCommands(selected.id, selected.status);
      } else {
        setHasCommands(false);
      }
      // Reset to config tab if current tab won't be available.
      if ((detailTab === "pricing" || detailTab === "tools" || detailTab === "system-prompt" || detailTab === "commands") && selected.status !== "running") {
        setDetailTab("config");
      }
      if (detailTab === "aliases" && !hasAliases) {
        setDetailTab("config");
      }
    } else {
      setHasPricing(false);
      setHasTools(false);
      setHasSystemPrompt(false);
      setHasCommands(false);
    }
  }, [selected?.id, selected?.status, probePricing, probeTools, probeSystemPrompt, probeCommands]);

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

  function statusClass(status: string): string {
    switch (status) {
      case "running":
      case "enabled": return "status-running";
      case "starting": return "status-starting";
      case "error":
      case "unhealthy": return "status-error";
      default: return "status-stopped";
    }
  }

  if (loading) {
    return (
      <div className="plugin-loading">
        <div className="spinner large" />
        <p>LOADING PLUGINS...</p>
      </div>
    );
  }

  return (
    <div className="plugin-layout">
      {/* ── Left sidebar ── */}
      <aside className="plugin-sidebar">
        <div className="plugin-sidebar-header">
          <span className="section-icon">[=]</span>
          PLUGINS
          {plugins.length > 0 && (
            <span className="section-count">{plugins.length}</span>
          )}
        </div>

        {plugins.length === 0 && !error && (
          <div className="plugin-sidebar-empty">
            No plugins installed.
          </div>
        )}

        <nav className="plugin-sidebar-list">
          {groups.map((g) => (
            <div key={g.id} className="plugin-sidebar-group">
              <div className="plugin-sidebar-group-header">
                <span className="plugin-sidebar-group-name">{g.name}</span>
                <span className="marketplace-sidebar-count">{g.plugins.length}</span>
              </div>
              {g.plugins.map((p) => (
                <div
                  key={p.id}
                  className={`plugin-sidebar-item${selectedId === p.id ? " active" : ""}`}
                  onClick={() => {
                    selectPlugin(p.id);
                    setConfirmUninstall(null);
                    setActionError("");
                  }}
                >
                  <span className={`plugin-status-dot ${statusClass(p.status)}`} />
                  <span className="plugin-sidebar-name">{p.name}</span>
                  {p.status === "running" && (
                    <button
                      className="plugin-sidebar-restart"
                      title="Restart"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleAction(() => restart(p.id));
                      }}
                    >
                      ↻
                    </button>
                  )}
                  <span className={`plugin-sidebar-status ${statusClass(p.status)}`}>
                    {p.status.toUpperCase()}
                  </span>
                </div>
              ))}
            </div>
          ))}
        </nav>
      </aside>

      {/* ── Right content ── */}
      <main className="plugin-detail">
        {error && <div className="form-error">{error}</div>}
        {actionError && <div className="form-error">{actionError}</div>}

        {!selected ? (
          <div className="plugin-detail-empty">
            <div className="plugin-empty-icon">[~]</div>
            <p>Select a plugin from the sidebar.</p>
          </div>
        ) : (
          <>
            {/* ── Plugin header ── */}
            <div className="plugin-detail-header">
              <div className="plugin-detail-title-row">
                <span className={`plugin-status-dot large ${statusClass(selected.status)}`} />
                <h2 className="plugin-detail-name">{selected.name}</h2>
                <span className="plugin-version">v{selected.version}</span>
                <span className={`plugin-status-label ${statusClass(selected.status)}`}>
                  {selected.status.toUpperCase()}
                </span>
              </div>

              {selected.image && (
                <div className="plugin-detail-meta">
                  <span className="plugin-image">{selected.image}</span>
                </div>
              )}

              {parseCapabilities(selected).length > 0 && (
                <div className="plugin-capabilities">
                  {parseCapabilities(selected).map((cap) => (
                    <span className="capability-tag" key={cap}>{cap}</span>
                  ))}
                </div>
              )}

              {/* ── Action buttons ── */}
              <div className="plugin-detail-actions">
                <button
                  className={`plugin-action-btn ${selected.enabled ? "btn-warning" : "btn-success"}`}
                  onClick={() =>
                    handleAction(() =>
                      selected.enabled ? disable(selected.id) : enable(selected.id)
                    )
                  }
                >
                  {selected.enabled ? "DISABLE" : "ENABLE"}
                </button>

                {selected.status === "running" && (
                  <button
                    className="plugin-action-btn"
                    onClick={() => handleAction(() => restart(selected.id))}
                  >
                    RESTART
                  </button>
                )}

                <button
                  className="plugin-action-btn btn-danger"
                  onClick={() => setConfirmUninstall(selected.id)}
                >
                  UNINSTALL
                </button>

                {(["config", ...(hasAliases ? ["aliases"] : [] as DetailTab[]), ...(hasPricing ? ["pricing"] : []), ...(hasTools ? ["tools"] : []), ...(hasSystemPrompt ? ["system-prompt"] : []), ...(hasCommands ? ["commands"] : []), ...(!selected.image ? [] : ["logs"])] as DetailTab[]).map((tab) => (
                  <button
                    key={tab}
                    className={`plugin-action-btn${detailTab === tab ? " btn-active" : ""}`}
                    onClick={() => setDetailTab(tab)}
                  >
                    {tab.toUpperCase()}
                  </button>
                ))}
              </div>

              {confirmUninstall === selected.id && (
                <div className="uninstall-confirm">
                  <span className="uninstall-confirm-text">
                    Uninstall "{selected.name}"? This cannot be undone.
                  </span>
                  <div className="uninstall-confirm-actions">
                    <button
                      className="plugin-action-btn btn-danger"
                      onClick={() => handleUninstall(selected.id)}
                    >
                      CONFIRM
                    </button>
                    <button
                      className="plugin-action-btn"
                      onClick={() => setConfirmUninstall(null)}
                    >
                      CANCEL
                    </button>
                  </div>
                </div>
              )}
            </div>

            {/* ── Content area ── */}
            <div className="plugin-detail-content">
              {detailTab === "config" && (
                <PluginConfigForm
                  key={selected.id}
                  plugin={selected}
                  onSaved={() => fetch()}
                />
              )}
              {detailTab === "aliases" && (
                <PluginAliasPanel
                  key={selected.id}
                  plugin={selected}
                  onSaved={() => fetch()}
                />
              )}
              {detailTab === "pricing" && (
                <PluginPricing
                  key={selected.id}
                  pluginId={selected.id}
                />
              )}
              {detailTab === "tools" && (
                <PluginTools
                  key={selected.id}
                  pluginId={selected.id}
                />
              )}
              {detailTab === "system-prompt" && (
                <PluginSystemPrompt
                  key={selected.id}
                  pluginId={selected.id}
                />
              )}
              {detailTab === "commands" && (
                <PluginDiscordCommands
                  key={selected.id}
                  pluginId={selected.id}
                />
              )}
              {detailTab === "logs" && (
                <PluginLogsInline
                  key={selected.id}
                  pluginId={selected.id}
                />
              )}
            </div>
          </>
        )}
      </main>
    </div>
  );
}
