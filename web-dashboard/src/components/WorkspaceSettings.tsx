import { useCallback, useEffect, useState } from "react";
import type { WorkspaceDisk, WorkspaceOptions, Workspace, Environment, Disk } from "@teamagentica/api-client";
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

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const val = bytes / Math.pow(1024, i);
  return `${val < 10 ? val.toFixed(1) : Math.round(val)} ${units[i]}`;
}

export default function WorkspaceSettings({ workspaceId, workspace: ws, environment: env, onClose }: Props) {
  const [tab, setTab] = useState<Tab>("overview");
  const [restarting, setRestarting] = useState(false);
  const [options, setOptions] = useState<WorkspaceOptions | null>(null);
  const [optionsLoading, setOptionsLoading] = useState(true);
  const [optionsDirty, setOptionsDirty] = useState(false);

  const [agentPlugins, setAgentPlugins] = useState<Plugin[]>([]);
  const [sharedDisks, setSharedDisks] = useState<Disk[]>([]);
  const [diskDetails, setDiskDetails] = useState<Map<string, Disk>>(new Map());

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
    // Fetch all disk details for size/metadata display.
    Promise.all([
      apiClient.workspaces.listDisks("workspace"),
      apiClient.workspaces.listDisks("shared"),
    ]).then(([wDisks, sDisks]) => {
      const map = new Map<string, Disk>();
      for (const d of [...wDisks, ...sDisks]) map.set(d.id, d);
      setDiskDetails(map);
    }).catch(() => {});
  }, []);

  // Local form state.
  const [envOverrides, setEnvOverrides] = useState<Record<string, string>>({});
  const [disks, setDisks] = useState<WorkspaceDisk[]>([]);
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
      // Prefer unified disks field, fall back to deprecated extra_disks.
      const raw = options.disks || options.extra_disks;
      const parsed = raw ? JSON.parse(raw) : [];
      if (Array.isArray(parsed)) {
        // Ensure legacy extra_disks entries get a type.
        setDisks(parsed.map((d: WorkspaceDisk) => ({ ...d, type: d.type || "shared" })));
      } else {
        setDisks([]);
      }
    } catch { setDisks([]); }
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

  // Disk helpers.
  const [newDiskId, setNewDiskId] = useState("");
  const [newDiskTarget, setNewDiskTarget] = useState("");
  const [newDiskRO, setNewDiskRO] = useState(false);

  const addDisk = () => {
    if (!newDiskId || !newDiskTarget.trim()) return;
    const disk = sharedDisks.find((d) => d.id === newDiskId);
    setDisks((prev) => [
      ...prev,
      { disk_id: newDiskId, name: disk?.name || newDiskId, type: "shared", target: newDiskTarget.trim(), read_only: newDiskRO },
    ]);
    setNewDiskId("");
    setNewDiskTarget("");
    setNewDiskRO(false);
  };

  const removeDisk = (idx: number) => {
    setDisks((prev) => prev.filter((_, i) => i !== idx));
  };

  // Split disks by type for display.
  const workspaceDisks = disks.filter((d) => d.type === "workspace");
  const sharedMountedDisks = disks.filter((d) => d.type === "shared");

  // Disks already attached — filter from dropdown.
  const attachedDiskIds = new Set(disks.map((d) => d.disk_id));
  const availableDisks = sharedDisks.filter((d) => !attachedDiskIds.has(d.id));

  const handleSave = useCallback(() => {
    // Auto-add pending form entries before saving.
    let disksToSave = disks;
    if (newDiskId && newDiskTarget.trim()) {
      const disk = sharedDisks.find((d) => d.id === newDiskId);
      disksToSave = [...disksToSave, { disk_id: newDiskId, name: disk?.name || newDiskId, type: "shared", target: newDiskTarget.trim(), read_only: newDiskRO }];
      setDisks(disksToSave);
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
      disks: disksToSave,
      agent_plugin: agentPlugin,
      agent_model: agentModel,
    }).then((updated) => {
      setOptions(updated);
      setOptionsDirty(true);
    }).catch(() => {});
  }, [workspaceId, envOverrides, disks, agentPlugin, agentModel, newDiskId, newDiskTarget, newDiskRO, sharedDisks, newEnvKey, newEnvVal]);

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
                  <hr className="wss-divider" />
                  <label className="wss-label">Disks</label>
                  {disks.length > 0 ? disks.map((d, i) => {
                    const detail = diskDetails.get(d.disk_id);
                    return (
                      <div key={`overview-disk-${i}`} className="wss-disk-card">
                        <span className="wss-disk-type-badge">{d.type}</span>
                        <span className="wss-mono">{d.name}</span>
                        <span className="wss-mount-arrow">&rarr;</span>
                        <span className="wss-mono">{d.target}</span>
                        {d.read_only && <span className="wss-mount-ro active">RO</span>}
                        {detail && <span className="wss-disk-size">{formatBytes(detail.size_bytes)}</span>}
                      </div>
                    );
                  }) : (
                    <div className="wss-empty-hint">No disks configured</div>
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
                  <p className="wss-hint">Manage disks attached to this workspace. Changes take effect on restart.</p>
                  <div className="wss-mount-list">
                    {workspaceDisks.map((d, i) => (
                      <div key={`wdisk-${i}-${d.disk_id}`} className="wss-mount-row">
                        <span className="wss-disk-type-badge">workspace</span>
                        <span className="wss-mount-name">{d.name}</span>
                        <span className="wss-mount-arrow">&rarr;</span>
                        <span className="wss-mono wss-mount-path">{d.target}</span>
                      </div>
                    ))}
                    {sharedMountedDisks.map((d) => {
                      const globalIdx = disks.indexOf(d);
                      return (
                        <div key={`sdisk-${globalIdx}-${d.disk_id}`} className="wss-mount-row">
                          <span className="wss-disk-type-badge">shared</span>
                          <span className="wss-mount-name">{d.name}</span>
                          <span className="wss-mount-arrow">&rarr;</span>
                          <span className="wss-mono wss-mount-path">{d.target}</span>
                          <span className={`wss-mount-ro${d.read_only ? " active" : ""}`}>
                            {d.read_only ? "RO" : "RW"}
                          </span>
                          <button className="wss-btn-icon" onClick={() => removeDisk(globalIdx)} title="Remove">x</button>
                        </div>
                      );
                    })}
                    {disks.length === 0 && (
                      <div className="wss-empty-hint">No disks attached.</div>
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
                      <button className="wss-btn wss-btn-sm" onClick={addDisk} disabled={!newDiskId || !newDiskTarget.trim()}>Add</button>
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
