import { useCallback, useEffect, useState } from "react";
import type { ExtraDisk, WorkspaceOptions, Workspace, Environment, Disk } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import type { Plugin } from "@teamagentica/api-client";
import SaveButton from "./SaveButton";

type Tab = "overview" | "environment" | "disks" | "agent";

interface Props {
  workspaceId: string;
  workspace: Workspace;
  environment?: Environment;
  onClose: () => void;
}

export default function WorkspaceSettings({ workspaceId, workspace: ws, environment: env, onClose }: Props) {
  const [tab, setTab] = useState<Tab>("overview");
  const [restarting, setRestarting] = useState(false);
  const [options, setOptions] = useState<WorkspaceOptions | null>(null);
  const [optionsLoading, setOptionsLoading] = useState(true);
  const [optionsDirty, setOptionsDirty] = useState(false);

  const [agentPlugins, setAgentPlugins] = useState<Plugin[]>([]);
  const [sharedDisks, setSharedDisks] = useState<Disk[]>([]);

  useEffect(() => {
    setOptionsLoading(true);
    apiClient.workspaces.getWorkspaceOptions(workspaceId)
      .then((opts) => { setOptions(opts); setOptionsLoading(false); })
      .catch(() => setOptionsLoading(false));
  }, [workspaceId]);

  useEffect(() => {
    apiClient.plugins.list().then((plugins) => {
      setAgentPlugins(plugins.filter((p) => p.capabilities?.includes("agent:chat") && p.enabled));
    }).catch(() => {});
    apiClient.workspaces.listDisks("shared").then(setSharedDisks).catch(() => {});
  }, []);

  // Local form state.
  const [envOverrides, setEnvOverrides] = useState<Record<string, string>>({});
  const [extraDisks, setExtraDisks] = useState<ExtraDisk[]>([]);
  const [agentPlugin, setAgentPlugin] = useState("");
  const [agentModel, setAgentModel] = useState("");

  // Sync local state when options load.
  useEffect(() => {
    if (!options) return;
    try {
      const parsed = options.env_overrides ? JSON.parse(options.env_overrides) : {};
      setEnvOverrides(parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed : {});
    } catch { setEnvOverrides({}); }
    try {
      const parsed = options.extra_disks ? JSON.parse(options.extra_disks) : [];
      setExtraDisks(Array.isArray(parsed) ? parsed : []);
    } catch { setExtraDisks([]); }
    setAgentPlugin(options.agent_plugin || "");
    setAgentModel(options.agent_model || "");
  }, [options]);

  // Env override helpers.
  const [newEnvKey, setNewEnvKey] = useState("");
  const [newEnvVal, setNewEnvVal] = useState("");

  const addEnvOverride = () => {
    if (!newEnvKey.trim()) return;
    setEnvOverrides((prev) => ({ ...prev, [newEnvKey.trim()]: newEnvVal }));
    setNewEnvKey("");
    setNewEnvVal("");
  };

  const removeEnvOverride = (key: string) => {
    setEnvOverrides((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  // Extra disk helpers.
  const [newDiskId, setNewDiskId] = useState("");
  const [newDiskTarget, setNewDiskTarget] = useState("");
  const [newDiskRO, setNewDiskRO] = useState(false);

  const addExtraDisk = () => {
    if (!newDiskId || !newDiskTarget.trim()) return;
    const disk = sharedDisks.find((d) => d.id === newDiskId);
    setExtraDisks((prev) => [
      ...prev,
      { disk_id: newDiskId, name: disk?.name || newDiskId, target: newDiskTarget.trim(), read_only: newDiskRO },
    ]);
    setNewDiskId("");
    setNewDiskTarget("");
    setNewDiskRO(false);
  };

  const removeExtraDisk = (idx: number) => {
    setExtraDisks((prev) => prev.filter((_, i) => i !== idx));
  };

  // Disks already attached — filter from dropdown.
  const attachedDiskIds = new Set(extraDisks.map((d) => d.disk_id));
  const availableDisks = sharedDisks.filter((d) => !attachedDiskIds.has(d.id));

  const handleSave = useCallback(() => {
    // Auto-add pending form entries before saving.
    let disksToSave = extraDisks;
    if (newDiskId && newDiskTarget.trim()) {
      const disk = sharedDisks.find((d) => d.id === newDiskId);
      disksToSave = [...disksToSave, { disk_id: newDiskId, name: disk?.name || newDiskId, target: newDiskTarget.trim(), read_only: newDiskRO }];
      setExtraDisks(disksToSave);
      setNewDiskId("");
      setNewDiskTarget("");
      setNewDiskRO(false);
    }
    let envToSave = envOverrides;
    if (newEnvKey.trim()) {
      envToSave = { ...envToSave, [newEnvKey.trim()]: newEnvVal };
      setEnvOverrides(envToSave);
      setNewEnvKey("");
      setNewEnvVal("");
    }

    apiClient.workspaces.updateWorkspaceOptions(workspaceId, {
      env_overrides: envToSave,
      extra_disks: disksToSave,
      agent_plugin: agentPlugin,
      agent_model: agentModel,
    }).then((updated) => {
      setOptions(updated);
      setOptionsDirty(true);
    }).catch(() => {});
  }, [workspaceId, envOverrides, extraDisks, agentPlugin, agentModel, newDiskId, newDiskTarget, newDiskRO, sharedDisks, newEnvKey, newEnvVal]);

  const handleRestart = useCallback(async () => {
    setRestarting(true);
    try {
      await apiClient.workspaces.restartWorkspace(workspaceId);
      setOptionsDirty(false);
    } catch { /* */ }
    setRestarting(false);
  }, [workspaceId]);

  return (
    <div className="wss-overlay">
      <div className="wss-panel">
        <div className="wss-header">
          <h2 className="wss-title">{ws.name}</h2>
          <button className="wss-btn wss-btn-close" onClick={onClose}>Close</button>
        </div>
        <div className="wss-toolbar">
          <span className={`wss-status wss-status-${ws.status}`}>{ws.status}</span>
          {env && <span className="wss-env-badge">{env.name}</span>}
          <div className="wss-toolbar-spacer" />
          <button className="wss-btn wss-btn-secondary" onClick={handleRestart} disabled={restarting}>
            {restarting ? "Restarting..." : "Restart"}
          </button>
        </div>

        <div className="wss-tabs">
          {(["overview", "environment", "disks", "agent"] as Tab[]).map((t) => (
            <button
              key={t}
              className={`wss-tab${tab === t ? " wss-tab-active" : ""}`}
              onClick={() => setTab(t)}
            >
              {t === "overview" ? "Overview" : t === "environment" ? "Environment" : t === "disks" ? "Disks" : "Agent"}
            </button>
          ))}
        </div>

        <div className="wss-content">
          {optionsLoading ? (
            <div className="wss-loading">Loading options...</div>
          ) : (
            <>
              {/* Overview Tab */}
              {tab === "overview" && (
                <div className="wss-section">
                  <div className="wss-field">
                    <label className="wss-label">Workspace ID</label>
                    <span className="wss-value wss-mono">{ws.id}</span>
                  </div>
                  <div className="wss-field">
                    <label className="wss-label">Subdomain</label>
                    <span className="wss-value wss-mono">{ws.subdomain}</span>
                  </div>
                  <div className="wss-field">
                    <label className="wss-label">Environment</label>
                    <span className="wss-value">{env?.name || ws.environment}</span>
                  </div>
                  <div className="wss-field">
                    <label className="wss-label">Status</label>
                    <span className="wss-value">{ws.status}</span>
                  </div>
                  {ws.url && (
                    <div className="wss-field">
                      <label className="wss-label">URL</label>
                      <a href={ws.url} target="_blank" rel="noopener noreferrer" className="wss-link">{ws.url}</a>
                    </div>
                  )}
                  {options?.sidecar_id && (
                    <div className="wss-field">
                      <label className="wss-label">Agent Sidecar</label>
                      <span className="wss-value wss-mono">{options.sidecar_id}</span>
                    </div>
                  )}
                </div>
              )}

              {/* Environment Tab */}
              {tab === "environment" && (
                <div className="wss-section">
                  <p className="wss-hint">Override environment variables. Merged on top of defaults on restart.</p>
                  <div className="wss-kv-list">
                    {Object.entries(envOverrides).filter(([key]) => key !== "").map(([key, val]) => (
                      <div key={`env-${key}`} className="wss-kv-row">
                        <span className="wss-kv-key">{key}</span>
                        <span className="wss-kv-eq">=</span>
                        <input
                          className="wss-input wss-kv-val"
                          value={val}
                          onChange={(e) => setEnvOverrides((prev) => ({ ...prev, [key]: e.target.value }))}
                        />
                        <button className="wss-btn-icon" onClick={() => removeEnvOverride(key)} title="Remove">x</button>
                      </div>
                    ))}
                  </div>
                  <div className="wss-kv-add">
                    <input
                      className="wss-input"
                      placeholder="KEY"
                      value={newEnvKey}
                      onChange={(e) => setNewEnvKey(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && addEnvOverride()}
                    />
                    <span className="wss-kv-eq">=</span>
                    <input
                      className="wss-input"
                      placeholder="value"
                      value={newEnvVal}
                      onChange={(e) => setNewEnvVal(e.target.value)}
                      onKeyDown={(e) => e.key === "Enter" && addEnvOverride()}
                    />
                    <button className="wss-btn wss-btn-sm" onClick={addEnvOverride}>Add</button>
                  </div>
                  <div className="wss-save-row">
                    <SaveButton onClick={handleSave} className="wss-btn wss-btn-save" />
                    {optionsDirty && <span className="wss-restart-hint">Restart required to apply</span>}
                  </div>
                </div>
              )}

              {/* Disks Tab */}
              {tab === "disks" && (
                <div className="wss-section">
                  <p className="wss-hint">Attach shared disks to this workspace. Use $HOME in paths — it resolves per environment. Changes take effect on restart.</p>
                  <div className="wss-mount-list">
                    {extraDisks.map((d, i) => (
                      <div key={`disk-${i}-${d.disk_id}`} className="wss-mount-row">
                        <span className="wss-mount-name">{d.name}</span>
                        <span className="wss-mount-arrow">&rarr;</span>
                        <span className="wss-mono wss-mount-path">{d.target}</span>
                        <span className={`wss-mount-ro${d.read_only ? " active" : ""}`}>
                          {d.read_only ? "RO" : "RW"}
                        </span>
                        <button className="wss-btn-icon" onClick={() => removeExtraDisk(i)} title="Remove">x</button>
                      </div>
                    ))}
                    {extraDisks.length === 0 && (
                      <div className="wss-empty-hint">No extra disks attached.</div>
                    )}
                  </div>
                  {availableDisks.length > 0 && (
                    <div className="wss-mount-add">
                      <select
                        className="wss-select"
                        value={newDiskId}
                        onChange={(e) => setNewDiskId(e.target.value)}
                      >
                        <option value="">Select disk...</option>
                        {availableDisks.map((d) => (
                          <option key={`avail-${d.id}-${d.name}`} value={d.id}>{d.name}</option>
                        ))}
                      </select>
                      <span className="wss-mount-arrow">&rarr;</span>
                      <input
                        className="wss-input"
                        placeholder="$HOME/.config/git"
                        value={newDiskTarget}
                        onChange={(e) => setNewDiskTarget(e.target.value)}
                      />
                      <label className="wss-checkbox">
                        <input type="checkbox" checked={newDiskRO} onChange={(e) => setNewDiskRO(e.target.checked)} />
                        RO
                      </label>
                      <button className="wss-btn wss-btn-sm" onClick={addExtraDisk} disabled={!newDiskId || !newDiskTarget.trim()}>Add</button>
                    </div>
                  )}
                  <div className="wss-save-row">
                    <SaveButton onClick={handleSave} className="wss-btn wss-btn-save" />
                    {optionsDirty && <span className="wss-restart-hint">Restart required to apply</span>}
                  </div>
                </div>
              )}

              {/* Agent Tab */}
              {tab === "agent" && (
                <div className="wss-section">
                  <p className="wss-hint">Attach an agent sidecar to this workspace.{agentPlugin && <> Chat with it using <strong>@{ws.subdomain}-{agentPlugin}</strong>.</>}</p>
                  <div className="wss-field">
                    <label className="wss-label">Agent Plugin</label>
                    <select
                      className="wss-select"
                      value={agentPlugin}
                      onChange={(e) => setAgentPlugin(e.target.value)}
                    >
                      <option value="">None</option>
                      {agentPlugins.map((p) => (
                        <option key={`agent-${p.id}`} value={p.id}>{p.name || p.id}</option>
                      ))}
                    </select>
                  </div>
                  {agentPlugin && (
                    <div className="wss-field">
                      <label className="wss-label">Model</label>
                      <select
                        className="wss-select"
                        value={agentModel}
                        onChange={(e) => setAgentModel(e.target.value)}
                      >
                        <option value="">Default</option>
                        <option value="claude-opus-4-6">Claude Opus</option>
                        <option value="claude-sonnet-4-6">Claude Sonnet</option>
                        <option value="claude-haiku-4-5-20251001">Claude Haiku</option>
                      </select>
                    </div>
                  )}
                  {options?.sidecar_id && (
                    <div className="wss-field">
                      <label className="wss-label">Active Sidecar</label>
                      <span className="wss-value wss-mono">{options.sidecar_id}</span>
                    </div>
                  )}
                  <div className="wss-save-row">
                    <SaveButton onClick={handleSave} className="wss-btn wss-btn-save" />
                    {optionsDirty && <span className="wss-restart-hint">Restart required to apply</span>}
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
