import { useCallback, useEffect, useRef, useState } from "react";
import { API_BASE, apiGet, apiPost, apiPatch, apiDelete } from "../api/client";

const ROUTE = "/api/route/infra-workspace-manager";
interface Environment {
  plugin_id: string;
  name: string;
  description: string;
  image: string;
  port: number;
  icon?: string;
}

interface Workspace {
  id: string;
  name: string;
  environment: string;
  status: string;
  subdomain: string;
  url: string;
  volume_name: string;
}

interface Volume {
  name: string;
  size_bytes: number;
  created_at: string;
  is_empty: boolean;
  has_workspace: boolean;
  tags: string[];
  git_remote: string;
  extensions: string[];
}

const LIST_TAB = "__list__";

export default function CodeEditor() {
  const [environments, setEnvironments] = useState<Environment[]>([]);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [volumes, setVolumes] = useState<Volume[]>([]);
  const [openTabs, setOpenTabs] = useState<string[]>([LIST_TAB]);
  const [activeTab, setActiveTab] = useState<string>(LIST_TAB);
  const [showCreate, setShowCreate] = useState(false);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [launchVolume, setLaunchVolume] = useState<string | null>(null);
  const [launching, setLaunching] = useState(false);

  const [newName, setNewName] = useState("");
  const [newEnvId, setNewEnvId] = useState("");
  const [newGitRepo, setNewGitRepo] = useState("");

  const [detectedPorts, setDetectedPorts] = useState<Record<string, number[]>>({});
  const iframeRefs = useRef<Record<string, HTMLIFrameElement | null>>({});
  const pollRef = useRef<ReturnType<typeof setInterval>>();
  const portPollRef = useRef<ReturnType<typeof setInterval>>();

  const fetchAll = useCallback(async () => {
    try {
      const [envRes, wsRes, volRes] = await Promise.all([
        apiGet<{ environments: Environment[] }>(`${ROUTE}/environments`),
        apiGet<{ workspaces: Workspace[] }>(`${ROUTE}/workspaces`),
        apiGet<{ volumes: Volume[] }>(`${ROUTE}/volumes`),
      ]);
      setEnvironments(envRes.environments || []);
      setWorkspaces(wsRes.workspaces || []);
      setVolumes(volRes.volumes || []);
      setError(null);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to load workspaces");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchAll();
    pollRef.current = setInterval(fetchAll, 10000);
    return () => clearInterval(pollRef.current);
  }, [fetchAll]);

  // Poll portpilot for detected ports on open workspace tabs.
  useEffect(() => {
    const pollPorts = async () => {
      const wsTabs = openTabs.filter((t) => t !== LIST_TAB);
      if (wsTabs.length === 0) return;
      const results: Record<string, number[]> = {};
      await Promise.all(
        wsTabs.map(async (id) => {
          try {
            const resp = await fetch(`${API_BASE}/ws/${id}/ports`, { credentials: "include" });
            if (resp.ok) {
              const data = await resp.json();
              results[id] = (data.ports || []).sort((a: number, b: number) => a - b);
            }
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
      await apiPost(`${ROUTE}/workspaces`, {
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
    try {
      await apiDelete(`${ROUTE}/workspaces/${id}`);
      closeTab(id);
      setConfirmDelete(null);
      await fetchAll();
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to delete workspace");
    }
  };

  const [startingIds, setStartingIds] = useState<Set<string>>(new Set());

  const openWorkspace = async (ws: Workspace) => {
    // Lazy-start: if the workspace is stopped, start it first.
    if (ws.status !== "running" && !startingIds.has(ws.id)) {
      setStartingIds((prev) => new Set(prev).add(ws.id));
      try {
        await apiPost(`${ROUTE}/workspaces/${ws.id}/start`, {});
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
  };

  const closeTab = (tabId: string) => {
    setOpenTabs((tabs) => {
      const next = tabs.filter((t) => t !== tabId);
      if (next.length === 0) next.push(LIST_TAB);
      if (activeTab === tabId) {
        const closedIdx = tabs.indexOf(tabId);
        const newActive = next[Math.min(closedIdx, next.length - 1)];
        setActiveTab(newActive);
      }
      return next;
    });
  };

  const iframeSrc = (ws: Workspace) => {
    return `${API_BASE}/ws/${ws.id}/`;
  };

  const handleLaunchVolume = async (volumeName: string, envId: string) => {
    setLaunching(true);
    setError(null);
    const slug = volumeName.replace(/^ws-[a-f0-9]{8}-/, "") || volumeName.replace(/^ws-/, "");
    const displayName = slug.replace(/-/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
    try {
      await apiPost(`${ROUTE}/workspaces`, {
        name: displayName,
        environment_id: envId,
        volume_name: volumeName,
      });
      setLaunchVolume(null);
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

  // Build volume lookup by name for workspace enrichment.
  const volMap = new Map(volumes.map((v) => [v.name, v]));
  const orphanVolumes = volumes.filter((v) => !v.has_workspace && !v.is_empty);
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
                  onClick={() => setActiveTab(LIST_TAB)}
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
                onClick={() => setActiveTab(tabId)}
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
                const vol = volMap.get(ws.volume_name);
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
                                await apiPatch(`${ROUTE}/workspaces/${ws.id}`, {
                                  name: renameValue.trim(),
                                });
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
                              {startingIds.has(ws.id) ? (
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
                        {confirmDelete === ws.id ? (
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
                              onClick={() => openWorkspace(ws)}
                            >
                              {isOpen ? "View" : "Open"}
                            </button>
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
          </div>

          {/* Saved volumes */}
          {orphanVolumes.length > 0 && (
            <div className="ws-volumes-section">
              <h3 className="ws-volumes-title">Saved Volumes</h3>
              <div className="ws-vol-grid">
                {orphanVolumes.map((v) => (
                  <div key={v.name} className="ws-vol-card">
                    <div className="ws-vol-card-header">
                      <span className="ws-vol-card-name">{v.name}</span>
                      {confirmDelete === `vol:${v.name}` ? (
                        <div className="ws-vol-card-confirm">
                          <span className="ws-confirm-text">Delete?</span>
                          <button
                            className="ws-btn ws-btn-danger ws-btn-sm"
                            onClick={async () => {
                              try {
                                await apiDelete(`${ROUTE}/volumes/${v.name}`);
                                setConfirmDelete(null);
                                await fetchAll();
                              } catch (e: unknown) {
                                setError(
                                  e instanceof Error ? e.message : "Failed to delete volume"
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
                          className="ws-vol-delete"
                          onClick={() => setConfirmDelete(`vol:${v.name}`)}
                          title="Delete volume"
                        >
                          x
                        </button>
                      )}
                    </div>

                    <div className="ws-vol-card-meta">
                      <span>{formatSize(v.size_bytes)}</span>
                      {v.created_at && (
                        <span>{new Date(v.created_at).toLocaleDateString()}</span>
                      )}
                    </div>

                    {v.git_remote && (
                      <div className="ws-vol-card-repo">{shortRepoName(v.git_remote)}</div>
                    )}

                    {v.tags.length > 0 && (
                      <div className="ws-vol-tags">
                        {v.tags.filter((t) => t !== "git").map((tag) => (
                          <span key={tag} className="ws-vol-tag">{tag}</span>
                        ))}
                      </div>
                    )}

                    {v.extensions.length > 0 && (
                      <div className="ws-vol-extensions">
                        <span className="ws-vol-ext-label">
                          {v.extensions.length} extension{v.extensions.length !== 1 ? "s" : ""}
                        </span>
                        <div className="ws-vol-ext-list">
                          {v.extensions.map((ext) => (
                            <span key={ext} className="ws-vol-ext">{ext}</span>
                          ))}
                        </div>
                      </div>
                    )}

                    {environments.length > 0 && (
                      <div className="ws-vol-launch">
                        {launchVolume === v.name ? (
                          <div className="ws-vol-env-list">
                            {environments.map((env) => (
                              <button
                                key={env.plugin_id}
                                className="ws-vol-env-option"
                                disabled={launching}
                                onClick={() => handleLaunchVolume(v.name, env.plugin_id)}
                              >
                                {env.name}
                              </button>
                            ))}
                            <button
                              className="ws-vol-env-option ws-vol-env-cancel"
                              onClick={() => setLaunchVolume(null)}
                            >
                              Cancel
                            </button>
                          </div>
                        ) : (
                          <button
                            className="ws-btn ws-btn-open ws-btn-sm"
                            onClick={() => setLaunchVolume(v.name)}
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
                        href={`${API_BASE}/ws/${tabId}/proxy/${port}/`}
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
      </div>
    </div>
  );
}
