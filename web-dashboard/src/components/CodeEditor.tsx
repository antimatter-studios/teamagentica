import { useCallback, useEffect, useRef, useState } from "react";
import { API_BASE } from "../api/client";
import { apiClient } from "../api/client";
import type { Environment, Workspace, Disk } from "@teamagentica/api-client";
import WorkspaceSettings from "./WorkspaceSettings";

const workspaceIframeSrc = (id: string) => `${API_BASE}/ws/${id}/`;
const workspacePortProxyUrl = (id: string, port: number) => `${API_BASE}/ws/${id}/proxy/${port}/`;

async function fetchWorkspacePorts(workspaceId: string): Promise<number[]> {
  const res = await fetch(`${API_BASE}/ws/${workspaceId}/ports`, { credentials: "include" });
  if (!res.ok) return [];
  const data = await res.json();
  return (data.ports || []).sort((a: number, b: number) => a - b);
}

const LIST_TAB = "__list__";

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

interface CodeEditorProps {
  initialWorkspace?: string;
  onWorkspaceChange?: (name: string) => void;
}

export default function CodeEditor({ initialWorkspace, onWorkspaceChange }: CodeEditorProps) {
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [disks, setDisks] = useState<Disk[]>([]);
  const [openTabs, setOpenTabs] = useState<string[]>([LIST_TAB]);
  const [activeTab, setActiveTab] = useState<string>(LIST_TAB);
  const [showCreate, setShowCreate] = useState(false);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [launchDisk, setLaunchDisk] = useState<string | null>(null);
  const [launching, setLaunching] = useState(false);
  const [settingsId, setSettingsId] = useState<string | null>(null);
  const [showDisksPanel, setShowDisksPanel] = useState(false);
  const [newDiskName, setNewDiskName] = useState("");
  const [creatingDisk, setCreatingDisk] = useState(false);
  const [confirmDeleteDisk, setConfirmDeleteDisk] = useState<string | null>(null);

  const [newName, setNewName] = useState("");
  const [newEnvId, setNewEnvId] = useState("");
  const [newGitRepo, setNewGitRepo] = useState("");

  const [detectedPorts, setDetectedPorts] = useState<Record<string, number[]>>({});
  const iframeRefs = useRef<Record<string, HTMLIFrameElement | null>>({});
  const pollRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);
  const portPollRef = useRef<ReturnType<typeof setInterval> | undefined>(undefined);

  const fetchAll = useCallback(async () => {
    try {
      const [envs, wss, vols] = await Promise.all([
        apiClient.workspaces.listEnvironments(),
        apiClient.workspaces.listWorkspaces(),
        apiClient.workspaces.listDisks(),
      ]);
      setEnvironments(envs);
      setWorkspaces(wss);
      setDisks(vols);
      setError(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load workspaces");
    } finally {
      setLoading(false);
    }
  }, []);

  const initialOpenDone = useRef(false);

  useEffect(() => {
    fetchAll();
    pollRef.current = setInterval(fetchAll, 10000);
    return () => clearInterval(pollRef.current);
  }, [fetchAll]);

  // Auto-open workspace from URL on first load.
  useEffect(() => {
    if (initialOpenDone.current || !initialWorkspace || workspaces.length === 0) return;
    initialOpenDone.current = true;
    const slug = initialWorkspace.toLowerCase();
    const match = workspaces.find((ws) => slugify(ws.name) === slug);
    if (match) {
      openWorkspace(match);
    }
  }, [initialWorkspace, workspaces]);

  // Poll portpilot for detected ports on open workspace tabs.
  useEffect(() => {
    const pollPorts = async () => {
      const wsTabs = openTabs.filter((t) => t !== LIST_TAB);
      if (wsTabs.length === 0) return;
      const results: Record<string, number[]> = {};
      await Promise.all(
        wsTabs.map(async (id) => {
          try {
            results[id] = await fetchWorkspacePorts(id);
          } catch {
            // Container may not be ready yet — ignore.
          }
        })
      );
      setDetectedPorts((prev) => ({ ...prev, ...results }));
    };
    pollPorts();
    portPollRef.current = setInterval(pollPorts, 3000);
    return () => clearInterval(portPollRef.current);
  }, [openTabs]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim() || !newEnvId) return;
    setCreating(true);
    setError(null);
    try {
      await apiClient.workspaces.createWorkspace({
        name: newName.trim(),
        environment_id: newEnvId,
        git_repo: newGitRepo.trim() || undefined,
      });
      setNewName("");
      setNewGitRepo("");
      setShowCreate(false);
      await fetchAll();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create workspace");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (id: string) => {
    setDeletingIds((prev) => new Set(prev).add(id));
    setConfirmDelete(null);
    try {
      await apiClient.workspaces.deleteWorkspace(id);
      closeTab(id);
      await fetchAll();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to delete workspace");
    } finally {
      setDeletingIds((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }
  };

  const [startingIds, setStartingIds] = useState<Set<string>>(new Set());
  const [stoppingIds, setStoppingIds] = useState<Set<string>>(new Set());
  const [deletingIds, setDeletingIds] = useState<Set<string>>(new Set());

  const handleStop = async (id: string) => {
    setStoppingIds((prev) => new Set(prev).add(id));
    try {
      await apiClient.workspaces.stopWorkspace(id);
      closeTab(id);
      await fetchAll();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to stop workspace");
    } finally {
      setStoppingIds((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
    }
  };

  const openWorkspace = async (ws: Workspace) => {
    // Lazy-start: if the workspace is stopped, start it first.
    if (ws.status !== "running" && !startingIds.has(ws.id)) {
      setStartingIds((prev) => new Set(prev).add(ws.id));
      try {
        await apiClient.workspaces.startWorkspace(ws.id);
        await fetchAll();
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : "Failed to start workspace");
      } finally {
        setStartingIds((prev) => {
          const next = new Set(prev);
          next.delete(ws.id);
          return next;
        });
      }
    }
    if (!openTabs.includes(ws.id)) {
      setOpenTabs((tabs) => [...tabs, ws.id]);
    }
    setActiveTab(ws.id);
    onWorkspaceChange?.(slugify(ws.name));
  };

  const closeTab = (tabId: string) => {
    setOpenTabs((tabs) => {
      const next = tabs.filter((t) => t !== tabId);
      if (next.length === 0) next.push(LIST_TAB);
      if (activeTab === tabId) {
        const closedIdx = tabs.indexOf(tabId);
        const newActive = next[Math.min(closedIdx, next.length - 1)];
        setActiveTab(newActive);
        // Update URL: show workspace name or clear if back to list.
        const ws = wsMap.get(newActive);
        onWorkspaceChange?.(ws ? slugify(ws.name) : "");
      }
      return next;
    });
  };

  const iframeSrc = (ws: Workspace) => {
    return workspaceIframeSrc(ws.id);
  };

  const handleLaunchDisk = async (diskName: string, envId: string) => {
    setLaunching(true);
    setError(null);
    const slug = diskName.replace(/^ws-[a-f0-9]{8}-/, "") || diskName.replace(/^ws-/, "");
    const displayName = slug.replace(/-/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
    try {
      await apiClient.workspaces.createWorkspace({
        name: displayName,
        environment_id: envId,
        disk_name: diskName,
      });
      setLaunchDisk(null);
      await fetchAll();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to launch workspace");
    } finally {
      setLaunching(false);
    }
  };

  const formatSize = (bytes: number) => {
    if (bytes < 1024) return `${bytes}B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)}KB`;
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
    return `${(bytes / 1024 / 1024 / 1024).toFixed(1)}GB`;
  };

  const shortRepoName = (url: string) => {
    return url.replace(/\.git$/, "").replace(/^.*[:/]([^/]+\/[^/]+)$/, "$1");
  };

  // Build disk lookup by name for workspace enrichment.
  const diskMap = new Map(disks.map((v: Disk) => [v.name, v]));
  const orphanDisks = disks.filter((v: Disk) => !v.has_workspace && !v.is_empty && v.type !== "shared");
  const wsMap = new Map(workspaces.map((ws) => [ws.id, ws]));

  // Find environment name from workspace.
  const envNameMap = new Map(environments.map((e) => [e.plugin_id, e.name]));
  const envIconMap = new Map(environments.filter((e) => e.icon).map((e) => [e.plugin_id, e.icon!]));

  if (loading) {
    return (
      <div className="code-editor-container">
        <div className="ws-loading">Loading workspaces...</div>
      </div>
    );
  }

  return (
    <div className="code-editor-container">
      <div className="ws-wrapper">
        {/* Tab bar */}
        <div className="ws-tab-bar">
          {openTabs.map((tabId) => {
            const isActive = tabId === activeTab;
            if (tabId === LIST_TAB) {
              return (
                <div
                  key={LIST_TAB}
                  className={`ws-tab${isActive ? " ws-tab-active" : ""}`}
                  onClick={() => { setActiveTab(LIST_TAB); onWorkspaceChange?.(""); }}
                >
                  <span className="ws-tab-label">Workspaces</span>
                </div>
              );
            }
            const ws = wsMap.get(tabId);
            if (!ws) return null;
            return (
              <div
                key={tabId}
                className={`ws-tab${isActive ? " ws-tab-active" : ""}`}
                onClick={() => { setActiveTab(tabId); const w = wsMap.get(tabId); onWorkspaceChange?.(w ? slugify(w.name) : ""); }}
              >
                <span className="ws-tab-label">{ws.name}</span>
                <button
                  className="ws-tab-close"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(tabId);
                  }}
                >
                  x
                </button>
              </div>
            );
          })}
          {!openTabs.includes(LIST_TAB) && (
            <button
              className="ws-tab ws-tab-add"
              onClick={() => {
                setOpenTabs((tabs) => [LIST_TAB, ...tabs]);
                setActiveTab(LIST_TAB);
              }}
              title="Manage workspaces"
            >
              +
            </button>
          )}
          <div className="ws-tab-spacer" />
          <div
            className={`ws-tab${showDisksPanel ? " ws-tab-active" : ""}`}
            onClick={() => setShowDisksPanel(!showDisksPanel)}
          >
            <span className="ws-tab-label">Disks</span>
          </div>
        </div>

        {error && activeTab === LIST_TAB && (
          <div className="ws-error">{error}</div>
        )}

        {/* List panel */}
        <div
          className="ws-list-panel"
          style={{ display: activeTab === LIST_TAB ? "flex" : "none" }}
        >
          <div className="ws-list-header">
            <button
              className="ws-btn ws-btn-create"
              onClick={() => setShowCreate(!showCreate)}
            >
              + New Workspace
            </button>
          </div>

          {/* Create form */}
          {showCreate && (
            <div className="ws-create-panel">
              <form onSubmit={handleCreate} className="ws-create-form">
                <div className="ws-create-field">
                  <label className="ws-create-label">Name</label>
                  <input
                    type="text"
                    placeholder="My Project"
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    className="ws-input"
                    autoFocus
                    required
                  />
                </div>
                <div className="ws-create-field">
                  <label className="ws-create-label">Environment</label>
                  <div className="ws-env-grid">
                    {environments.map((env) => (
                      <button
                        key={env.plugin_id}
                        type="button"
                        className={`ws-env-card${newEnvId === env.plugin_id ? " ws-env-card-active" : ""}`}
                        onClick={() => setNewEnvId(env.plugin_id)}
                      >
                        <span className="ws-env-card-name">{env.name}</span>
                        {env.description && (
                          <span className="ws-env-card-desc">{env.description}</span>
                        )}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="ws-create-field">
                  <label className="ws-create-label">
                    Git Repository <span className="ws-create-optional">optional</span>
                  </label>
                  <input
                    type="text"
                    placeholder="https://github.com/user/repo"
                    value={newGitRepo}
                    onChange={(e) => setNewGitRepo(e.target.value)}
                    className="ws-input"
                  />
                </div>
                <div className="ws-create-actions">
                  <button
                    type="submit"
                    className="ws-btn ws-btn-create"
                    disabled={creating || !newName.trim() || !newEnvId}
                  >
                    {creating ? "Creating..." : "Create Workspace"}
                  </button>
                  <button
                    type="button"
                    className="ws-btn ws-btn-cancel"
                    onClick={() => setShowCreate(false)}
                  >
                    Cancel
                  </button>
                </div>
              </form>
            </div>
          )}

          {/* Workspace card grid */}
          <div className="ws-card-grid">
            {workspaces.length === 0 && !showCreate ? (
              <div className="ws-empty">
                <p>No workspaces yet.</p>
                <p className="ws-empty-hint">
                  Create one to start coding in the browser.
                </p>
              </div>
            ) : (
              workspaces.map((ws) => {
                const vol = diskMap.get(ws.disk_name);
                const tags = vol?.tags?.filter((t) => t !== "git") || [];
                const gitRemote = vol?.git_remote;
                const envName = envNameMap.get(ws.environment);
                const envIcon = envIconMap.get(ws.environment);
                const isOpen = openTabs.includes(ws.id);

                return (
                  <div
                    key={ws.id}
                    className={`ws-card${isOpen ? " ws-card-open" : ""}`}
                    onClick={() => openWorkspace(ws)}
                  >
                    {/* Environment icon placeholder */}
                    <div className="ws-card-preview">
                      <div className="ws-card-no-preview">
                        {envIcon ? (
                          <span
                            className="ws-card-env-icon"
                            dangerouslySetInnerHTML={{ __html: envIcon }}
                          />
                        ) : (
                          <span className="ws-card-no-preview-text">{envName || "Workspace"}</span>
                        )}
                      </div>
                    </div>

                    {/* Card body */}
                    <div className="ws-card-body">
                      <div className="ws-card-info">
                        {renamingId === ws.id ? (
                          <form
                            className="ws-rename-form"
                            onSubmit={async (e) => {
                              e.preventDefault();
                              if (!renameValue.trim()) return;
                              setError(null);
                              try {
                                await apiClient.workspaces.renameWorkspace(ws.id, renameValue.trim());
                                setRenamingId(null);
                                await fetchAll();
                              } catch (err: unknown) {
                                setError(
                                  err instanceof Error ? err.message : "Failed to rename"
                                );
                              }
                            }}
                            onClick={(e) => e.stopPropagation()}
                          >
                            <input
                              type="text"
                              value={renameValue}
                              onChange={(e) => setRenameValue(e.target.value)}
                              className="ws-input ws-input-rename"
                              autoFocus
                            />
                            <button type="submit" className="ws-btn ws-btn-open ws-btn-sm">
                              Save
                            </button>
                            <button
                              type="button"
                              className="ws-btn ws-btn-cancel ws-btn-sm"
                              onClick={() => setRenamingId(null)}
                            >
                              Cancel
                            </button>
                          </form>
                        ) : (
                          <>
                            <div className="ws-card-name-row">
                              <span className="ws-card-name">{ws.name}</span>
                              {deletingIds.has(ws.id) ? (
                                <span className="ws-card-status ws-card-status-deleting">Deleting...</span>
                              ) : startingIds.has(ws.id) ? (
                                <span className="ws-card-status ws-card-status-starting">Starting...</span>
                              ) : ws.status !== "running" ? (
                                <span className="ws-card-status ws-card-status-stopped">Stopped</span>
                              ) : null}
                            </div>
                            {envName && (
                              <span className="ws-card-env">{envName}</span>
                            )}
                            {gitRemote && (
                              <span className="ws-card-repo">{shortRepoName(gitRemote)}</span>
                            )}
                            {tags.length > 0 && (
                              <div className="ws-card-tags">
                                {tags.map((tag) => (
                                  <span key={tag} className="ws-card-tag">{tag}</span>
                                ))}
                              </div>
                            )}
                          </>
                        )}
                      </div>

                      {/* Actions */}
                      <div className="ws-card-actions" onClick={(e) => e.stopPropagation()}>
                        {deletingIds.has(ws.id) ? (
                          <span className="ws-card-status ws-card-status-deleting">Deleting...</span>
                        ) : confirmDelete === ws.id ? (
                          <>
                            <span className="ws-confirm-text">Delete?</span>
                            <button
                              className="ws-btn ws-btn-danger ws-btn-sm"
                              onClick={() => handleDelete(ws.id)}
                            >
                              Yes
                            </button>
                            <button
                              className="ws-btn ws-btn-cancel ws-btn-sm"
                              onClick={() => setConfirmDelete(null)}
                            >
                              No
                            </button>
                          </>
                        ) : renamingId === ws.id ? null : (
                          <>
                            <button
                              className="ws-btn ws-btn-open ws-btn-sm"
                              disabled={startingIds.has(ws.id) || stoppingIds.has(ws.id)}
                              onClick={() => openWorkspace(ws)}
                            >
                              {startingIds.has(ws.id) ? "Starting..." : isOpen ? "View" : ws.status !== "running" ? "Start" : "Open"}
                            </button>
                            {ws.status === "running" && (
                              <button
                                className="ws-btn ws-btn-cancel ws-btn-sm"
                                disabled={stoppingIds.has(ws.id)}
                                onClick={() => handleStop(ws.id)}
                              >
                                {stoppingIds.has(ws.id) ? "Stopping..." : "Stop"}
                              </button>
                            )}
                            <button
                              className="ws-btn ws-btn-rename ws-btn-sm"
                              onClick={() => {
                                setRenamingId(ws.id);
                                setRenameValue(ws.name);
                              }}
                            >
                              Rename
                            </button>
                            <button
                              className="ws-btn ws-btn-settings ws-btn-sm"
                              onClick={() => setSettingsId(ws.id)}
                              title="Workspace settings"
                            >
                              Settings
                            </button>
                            <button
                              className="ws-btn ws-btn-danger ws-btn-sm"
                              onClick={() => setConfirmDelete(ws.id)}
                            >
                              Delete
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  </div>
                );
              })
            )}

            {/* Workspace settings sidebar */}
            {settingsId && wsMap.get(settingsId) && (
              <WorkspaceSettings
                workspaceId={settingsId}
                workspace={wsMap.get(settingsId)!}
                environment={environments.find((e) => e.plugin_id === wsMap.get(settingsId)?.environment)}
                onClose={() => { setSettingsId(null); fetchAll(); }}
              />
            )}

            {/* Shared disks management sidebar */}
            {showDisksPanel && (
              <div className="wss-overlay">
                <div className="wss-panel">
                  <div className="wss-header">
                    <h2 className="wss-title">Shared Disks</h2>
                    <button className="wss-btn wss-btn-close" onClick={() => setShowDisksPanel(false)}>Close</button>
                  </div>
                  <div className="wss-content">
                    <div className="wss-section">
                      <p className="wss-hint">Shared disks are reusable storage that can be attached to any workspace (credentials, config, shared data).</p>

                      {/* Create new shared disk */}
                      <div className="wss-mount-add">
                        <input
                          className="wss-input"
                          style={{ flex: 1 }}
                          placeholder="Disk name (e.g. git-config)"
                          value={newDiskName}
                          onChange={(e) => setNewDiskName(e.target.value)}
                          onKeyDown={async (e) => {
                            if (e.key === "Enter" && newDiskName.trim() && !creatingDisk) {
                              setCreatingDisk(true);
                              try {
                                await apiClient.workspaces.createDisk(newDiskName.trim(), "shared");
                                setNewDiskName("");
                                await fetchAll();
                              } catch (err) {
                                setError(err instanceof Error ? err.message : "Failed to create disk");
                              }
                              setCreatingDisk(false);
                            }
                          }}
                        />
                        <button
                          className="wss-btn wss-btn-primary wss-btn-sm"
                          disabled={creatingDisk || !newDiskName.trim()}
                          onClick={async () => {
                            setCreatingDisk(true);
                            try {
                              await apiClient.workspaces.createDisk(newDiskName.trim(), "shared");
                              setNewDiskName("");
                              await fetchAll();
                            } catch (err) {
                              setError(err instanceof Error ? err.message : "Failed to create disk");
                            }
                            setCreatingDisk(false);
                          }}
                        >
                          {creatingDisk ? "Creating..." : "Create"}
                        </button>
                      </div>

                      {/* List shared disks */}
                      <div className="wss-mount-list">
                        {disks.filter((d) => d.type === "shared").length === 0 ? (
                          <div className="wss-empty-hint">No shared disks yet.</div>
                        ) : (
                          disks.filter((d) => d.type === "shared").map((d) => (
                            <div key={d.id} className="wss-mount-row">
                              <span className="wss-mount-name" style={{ flex: 1 }}>{d.name}</span>
                              <span className="wss-disk-size">{formatSize(d.size_bytes)}</span>
                              {confirmDeleteDisk === d.id ? (
                                <>
                                  <span className="wss-restart-hint">Delete?</span>
                                  <button
                                    className="wss-btn-icon"
                                    onClick={() => {
                                      apiClient.workspaces.deleteDisk(d.type, d.name)
                                        .then(() => { setConfirmDeleteDisk(null); fetchAll(); })
                                        .catch((err) => setError(err instanceof Error ? err.message : "Failed to delete"));
                                    }}
                                    title="Confirm delete"
                                  >
                                    Yes
                                  </button>
                                  <button className="wss-btn-icon" onClick={() => setConfirmDeleteDisk(null)}>No</button>
                                </>
                              ) : (
                                <button className="wss-btn-icon" onClick={() => setConfirmDeleteDisk(d.id)} title="Delete disk">x</button>
                              )}
                            </div>
                          ))
                        )}
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Saved disks */}
          {orphanDisks.length > 0 && (
            <div className="ws-disks-section">
              <h3 className="ws-disks-title">Saved Disks</h3>
              <div className="ws-disk-grid">
                {orphanDisks.map((v) => (
                  <div key={v.name} className="ws-disk-card">
                    <div className="ws-disk-card-header">
                      <span className="ws-disk-card-name">{v.name}</span>
                      {confirmDelete === `vol:${v.name}` ? (
                        <div className="ws-disk-card-confirm">
                          <span className="ws-confirm-text">Delete?</span>
                          <button
                            className="ws-btn ws-btn-danger ws-btn-sm"
                            onClick={async () => {
                              try {
                                await apiClient.workspaces.deleteDisk(v.type, v.name);
                                setConfirmDelete(null);
                                await fetchAll();
                              } catch (e: unknown) {
                                setError(
                                  e instanceof Error ? e.message : "Failed to delete disk"
                                );
                              }
                            }}
                          >
                            Yes
                          </button>
                          <button
                            className="ws-btn ws-btn-cancel ws-btn-sm"
                            onClick={() => setConfirmDelete(null)}
                          >
                            No
                          </button>
                        </div>
                      ) : (
                        <button
                          className="ws-disk-delete"
                          onClick={() => setConfirmDelete(`vol:${v.name}`)}
                          title="Delete disk"
                        >
                          x
                        </button>
                      )}
                    </div>

                    <div className="ws-disk-card-meta">
                      <span>{formatSize(v.size_bytes)}</span>
                      {v.created_at && (
                        <span>{new Date(v.created_at).toLocaleDateString()}</span>
                      )}
                    </div>

                    {v.git_remote && (
                      <div className="ws-disk-card-repo">{shortRepoName(v.git_remote)}</div>
                    )}

                    {(v.tags?.length ?? 0) > 0 && (
                      <div className="ws-disk-tags">
                        {v.tags!.filter((t) => t !== "git").map((tag) => (
                          <span key={tag} className="ws-disk-tag">{tag}</span>
                        ))}
                      </div>
                    )}

                    {(v.extensions?.length ?? 0) > 0 && (
                      <div className="ws-disk-extensions">
                        <span className="ws-disk-ext-label">
                          {v.extensions!.length} extension{v.extensions!.length !== 1 ? "s" : ""}
                        </span>
                        <div className="ws-disk-ext-list">
                          {v.extensions!.map((ext) => (
                            <span key={ext} className="ws-disk-ext">{ext}</span>
                          ))}
                        </div>
                      </div>
                    )}

                    {environments.length > 0 && (
                      <div className="ws-disk-launch">
                        {launchDisk === v.name ? (
                          <div className="ws-disk-env-list">
                            {environments.map((env) => (
                              <button
                                key={env.plugin_id}
                                className="ws-disk-env-option"
                                disabled={launching}
                                onClick={() => handleLaunchDisk(v.name, env.plugin_id)}
                              >
                                {env.name}
                              </button>
                            ))}
                            <button
                              className="ws-disk-env-option ws-disk-env-cancel"
                              onClick={() => setLaunchDisk(null)}
                            >
                              Cancel
                            </button>
                          </div>
                        ) : (
                          <button
                            className="ws-btn ws-btn-open ws-btn-sm"
                            onClick={() => setLaunchDisk(v.name)}
                          >
                            Launch
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Workspace iframes */}
        {openTabs
          .filter((t) => t !== LIST_TAB)
          .map((tabId) => {
            const ws = wsMap.get(tabId);
            if (!ws) return null;
            return (
              <div
                key={tabId}
                className="ws-content"
                style={{ display: activeTab === tabId ? "flex" : "none", flexDirection: "column" }}
              >
                {(detectedPorts[tabId]?.length ?? 0) > 0 && (
                  <div className="ws-port-bar">
                    <span className="ws-port-label">Ports:</span>
                    {detectedPorts[tabId].map((port) => (
                      <a
                        key={port}
                        href={workspacePortProxyUrl(tabId, port)}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="ws-port-link"
                      >
                        {port}
                      </a>
                    ))}
                  </div>
                )}
                <iframe
                  ref={(el) => { iframeRefs.current[tabId] = el; }}
                  src={iframeSrc(ws)}
                  className="code-editor-iframe"
                  title={`Workspace: ${ws.name}`}
                  allow="clipboard-read; clipboard-write"
                  style={{ flex: 1 }}
                />
              </div>
            );
          })}

      </div>{/* end ws-wrapper */}
    </div>
  );
}
