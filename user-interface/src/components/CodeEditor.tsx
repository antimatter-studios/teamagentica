import { useCallback, useEffect, useRef, useState } from "react";
import { apiGet, apiPost, apiPatch, apiDelete } from "../api/client";
import { getStoredToken } from "../api/auth";

const apiHost =
  import.meta.env.VITE_TEAMAGENTICA_KERNEL_HOST || "api.teamagentica.localhost";
const baseDomain = apiHost.replace(/^[^.]+\./, "");

const ROUTE = "/api/route/infra-workspace-manager";

interface Environment {
  plugin_id: string;
  name: string;
  description: string;
  image: string;
  port: number;
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

  const token = getStoredToken();
  const pollRef = useRef<ReturnType<typeof setInterval>>();

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

  const openWorkspace = (ws: Workspace) => {
    if (!openTabs.includes(ws.id)) {
      setOpenTabs((tabs) => [...tabs, ws.id]);
    }
    setActiveTab(ws.id);
  };

  const closeTab = (tabId: string) => {
    setOpenTabs((tabs) => {
      const next = tabs.filter((t) => t !== tabId);
      // Ensure at least the list tab remains.
      if (next.length === 0) next.push(LIST_TAB);
      // If closing the active tab, switch to the nearest neighbor.
      if (activeTab === tabId) {
        const closedIdx = tabs.indexOf(tabId);
        const newActive = next[Math.min(closedIdx, next.length - 1)];
        setActiveTab(newActive);
      }
      return next;
    });
  };

  const iframeSrc = (ws: Workspace) => {
    const host = `//${ws.subdomain}.${baseDomain}`;
    return token ? `${host}/?tkn=${encodeURIComponent(token)}` : `${host}/`;
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

  const orphanVolumes = volumes.filter((v) => !v.has_workspace && !v.is_empty);

  // Build a map of workspace ID → workspace for quick lookup.
  const wsMap = new Map(workspaces.map((ws) => [ws.id, ws]));

  if (loading) {
    return (
      <div className="code-editor-container">
        <div className="ws-loading">Loading workspaces…</div>
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
                  ×
                </button>
              </div>
            );
          })}
          {/* Quick-open: show list tab if not already open */}
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

        {/* List panel — visible when list tab is active */}
        <div
          className="ws-list-panel"
          style={{ display: activeTab === LIST_TAB ? "flex" : "none" }}
        >
          <div className="ws-list-header">
            <button
              className="ws-btn ws-btn-create"
              onClick={() => setShowCreate(!showCreate)}
            >
              + New
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
                    {creating ? "Creating…" : "Create Workspace"}
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

          {/* Workspace list */}
          <div className="ws-list">
            {workspaces.length === 0 && !showCreate ? (
              <div className="ws-empty">
                <p>No workspaces yet.</p>
                <p className="ws-empty-hint">
                  Create one to start coding in the browser.
                </p>
              </div>
            ) : (
              workspaces.map((ws) => (
                <div
                  key={ws.id}
                  className={`ws-list-row${openTabs.includes(ws.id) ? " ws-list-row-open" : ""}`}
                >
                  <div
                    className="ws-list-info"
                    onClick={() => openWorkspace(ws)}
                    style={{ cursor: "pointer" }}
                  >
                    <div className="ws-list-details">
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
                                err instanceof Error
                                  ? err.message
                                  : "Failed to rename"
                              );
                            }
                          }}
                        >
                          <input
                            type="text"
                            value={renameValue}
                            onChange={(e) => setRenameValue(e.target.value)}
                            className="ws-input ws-input-rename"
                            autoFocus
                            onClick={(e) => e.stopPropagation()}
                          />
                          <button
                            type="submit"
                            className="ws-btn ws-btn-open ws-btn-sm"
                          >
                            Save
                          </button>
                          <button
                            type="button"
                            className="ws-btn ws-btn-cancel ws-btn-sm"
                            onClick={(e) => {
                              e.stopPropagation();
                              setRenamingId(null);
                            }}
                          >
                            Cancel
                          </button>
                        </form>
                      ) : (
                        <>
                          <span className="ws-list-name">{ws.name}</span>
                          <span className="ws-list-meta">
                            {ws.subdomain}.{baseDomain}
                          </span>
                        </>
                      )}
                    </div>
                  </div>
                  <div className="ws-list-actions">
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
                          onClick={(e) => {
                            e.stopPropagation();
                            openWorkspace(ws);
                          }}
                        >
                          {openTabs.includes(ws.id) ? "View" : "Open"}
                        </button>
                        <button
                          className="ws-btn ws-btn-rename ws-btn-sm"
                          onClick={(e) => {
                            e.stopPropagation();
                            setRenamingId(ws.id);
                            setRenameValue(ws.name);
                          }}
                        >
                          Rename
                        </button>
                        <button
                          className="ws-btn ws-btn-danger ws-btn-sm"
                          onClick={(e) => {
                            e.stopPropagation();
                            setConfirmDelete(ws.id);
                          }}
                        >
                          Delete
                        </button>
                      </>
                    )}
                  </div>
                </div>
              ))
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
                          ×
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

        {/* Workspace iframes — each open tab gets a persistent iframe */}
        {openTabs
          .filter((t) => t !== LIST_TAB)
          .map((tabId) => {
            const ws = wsMap.get(tabId);
            if (!ws) return null;
            return (
              <div
                key={tabId}
                className="ws-content"
                style={{ display: activeTab === tabId ? "block" : "none" }}
              >
                <iframe
                  src={iframeSrc(ws)}
                  className="code-editor-iframe"
                  title={`Workspace: ${ws.name}`}
                  allow="clipboard-read; clipboard-write"
                />
              </div>
            );
          })}
      </div>
    </div>
  );
}
