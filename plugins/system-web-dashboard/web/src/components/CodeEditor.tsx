import { useCallback, useEffect, useRef, useState } from "react";
import { Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import { API_BASE } from "../api/client";
import { apiClient } from "../api/client";
import type { Environment, Workspace, Disk } from "@teamagentica/api-client";
import WorkspaceSettings from "./WorkspaceSettings";
import TunnelPicker from "./TunnelPicker";

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
  const [newTunnelRefs, setNewTunnelRefs] = useState<string[]>([]);

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

  useEffect(() => {
    if (initialOpenDone.current || !initialWorkspace || workspaces.length === 0) return;
    initialOpenDone.current = true;
    const slug = initialWorkspace.toLowerCase();
    const match = workspaces.find((ws) => slugify(ws.name) === slug);
    if (match) openWorkspace(match);
  }, [initialWorkspace, workspaces]);

  useEffect(() => {
    const pollPorts = async () => {
      const wsTabs = openTabs.filter((t) => t !== LIST_TAB);
      if (wsTabs.length === 0) return;
      const results: Record<string, number[]> = {};
      await Promise.all(
        wsTabs.map(async (id) => {
          try { results[id] = await fetchWorkspacePorts(id); } catch {}
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
        tunnel_refs: newTunnelRefs.length > 0 ? newTunnelRefs : undefined,
      });
      setNewName("");
      setNewGitRepo("");
      setNewTunnelRefs([]);
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
    if (ws.status !== "running" && !startingIds.has(ws.id)) {
      setStartingIds((prev) => new Set(prev).add(ws.id));
      try {
        await apiClient.workspaces.startWorkspace(ws.id);
        for (let i = 0; i < 30; i++) {
          const list = await apiClient.workspaces.list();
          const updated = list.find((w: Workspace) => w.id === ws.id);
          if (updated?.status === "running") break;
          await new Promise((r) => setTimeout(r, 1000));
        }
        await fetchAll();
      } catch (e: unknown) {
        setError(e instanceof Error ? e.message : "Failed to start workspace");
        setStartingIds((prev) => { const next = new Set(prev); next.delete(ws.id); return next; });
        return;
      } finally {
        setStartingIds((prev) => {
          const next = new Set(prev);
          next.delete(ws.id);
          return next;
        });
      }
    }
    if (!openTabs.includes(ws.id)) setOpenTabs((tabs) => [...tabs, ws.id]);
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
        const ws = wsMap.get(newActive);
        onWorkspaceChange?.(ws ? slugify(ws.name) : "");
      }
      return next;
    });
  };

  const iframeSrc = (ws: Workspace) => workspaceIframeSrc(ws.id);

  const handleLaunchDisk = async (diskId: string, diskName: string, envId: string) => {
    setLaunching(true);
    setError(null);
    const slug = diskName.replace(/^ws-[a-f0-9]{8}-/, "") || diskName.replace(/^ws-/, "");
    const displayName = slug.replace(/-/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
    try {
      await apiClient.workspaces.createWorkspace({
        name: displayName,
        environment_id: envId,
        disk_id: diskId,
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

  const diskMap = new Map(disks.map((v: Disk) => [v.name, v]));
  const orphanDisks = disks.filter((v: Disk) => !v.has_workspace && !v.is_empty && v.type !== "shared");
  const wsMap = new Map(workspaces.map((ws) => [ws.id, ws]));

  const envNameMap = new Map(environments.map((e) => [e.plugin_id, e.name]));
  const envIconMap = new Map(environments.filter((e) => e.icon).map((e) => [e.plugin_id, e.icon!]));

  if (loading) {
    return (
      <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
        Loading workspaces...
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col">
      {/* Tab bar */}
      <div className="flex items-center gap-1 border-b bg-muted/30 px-2">
        {openTabs.map((tabId) => {
          const isActive = tabId === activeTab;
          if (tabId === LIST_TAB) {
            return (
              <button
                key={LIST_TAB}
                className={cn(
                  "border-b-2 px-3 py-2 text-sm",
                  isActive ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"
                )}
                onClick={() => { setActiveTab(LIST_TAB); onWorkspaceChange?.(""); }}
              >
                Workspaces
              </button>
            );
          }
          const ws = wsMap.get(tabId);
          if (!ws) return null;
          return (
            <div
              key={tabId}
              className={cn(
                "flex items-center gap-1 border-b-2 px-3 py-2 text-sm cursor-pointer",
                isActive ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground"
              )}
              onClick={() => { setActiveTab(tabId); const w = wsMap.get(tabId); onWorkspaceChange?.(w ? slugify(w.name) : ""); }}
            >
              <span>{ws.name}</span>
              <Button
                size="icon" variant="ghost" className="h-5 w-5"
                onClick={(e) => { e.stopPropagation(); closeTab(tabId); }}
              >
                <X className="h-3 w-3" />
              </Button>
            </div>
          );
        })}
        {!openTabs.includes(LIST_TAB) && (
          <Button
            size="icon" variant="ghost" className="h-7 w-7"
            onClick={() => {
              setOpenTabs((tabs) => [LIST_TAB, ...tabs]);
              setActiveTab(LIST_TAB);
            }}
            title="Manage workspaces"
          >
            <Plus className="h-4 w-4" />
          </Button>
        )}
      </div>

      {error && activeTab === LIST_TAB && (
        <Alert variant="destructive" className="m-3">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {/* List panel */}
      <div className={cn("flex-1 overflow-auto p-4", activeTab !== LIST_TAB && "hidden")}>
        <div className="mb-4 flex flex-wrap gap-2">
          <Button onClick={() => setShowCreate(!showCreate)}>
            <Plus className="mr-1 h-4 w-4" /> New workspace
          </Button>
          <Button variant="outline" onClick={() => setShowDisksPanel(!showDisksPanel)}>
            Shared disk management
          </Button>
        </div>

        {showCreate && (
          <Card className="mb-4 p-4">
            <form onSubmit={handleCreate} className="flex flex-col gap-3">
              <div className="flex flex-col gap-1">
                <Label>Name</Label>
                <Input
                  type="text"
                  placeholder="My Project"
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  autoFocus
                  required
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label>Environment</Label>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 md:grid-cols-3">
                  {environments.map((env) => (
                    <button
                      key={env.plugin_id}
                      type="button"
                      className={cn(
                        "flex flex-col items-start rounded-md border p-3 text-left transition hover:border-primary",
                        newEnvId === env.plugin_id && "border-primary bg-primary/5"
                      )}
                      onClick={() => setNewEnvId(env.plugin_id)}
                    >
                      <span className="font-semibold">{env.name}</span>
                      {env.description && (
                        <span className="text-xs text-muted-foreground">{env.description}</span>
                      )}
                    </button>
                  ))}
                </div>
              </div>
              <div className="flex flex-col gap-1">
                <Label>
                  Git repository <span className="text-xs text-muted-foreground">(optional)</span>
                </Label>
                <Input
                  type="text"
                  placeholder="https://github.com/user/repo"
                  value={newGitRepo}
                  onChange={(e) => setNewGitRepo(e.target.value)}
                />
              </div>
              <div className="flex flex-col gap-1">
                <Label>
                  Tunnels <span className="text-xs text-muted-foreground">(optional)</span>
                </Label>
                <TunnelPicker value={newTunnelRefs} onChange={setNewTunnelRefs} />
              </div>
              <div className="flex gap-2">
                <Button type="submit" disabled={creating || !newName.trim() || !newEnvId}>
                  {creating ? "Creating..." : "Create workspace"}
                </Button>
                <Button type="button" variant="outline" onClick={() => setShowCreate(false)}>
                  Cancel
                </Button>
              </div>
            </form>
          </Card>
        )}

        {/* Workspace card grid */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {workspaces.length === 0 && !showCreate ? (
            <div className="col-span-full rounded-md border border-dashed p-8 text-center text-muted-foreground">
              <p>No workspaces yet.</p>
              <p className="mt-1 text-sm">Create one to start coding in the browser.</p>
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
                <Card
                  key={ws.id}
                  className={cn(
                    "flex cursor-pointer flex-col overflow-hidden transition hover:border-primary",
                    isOpen && "border-primary"
                  )}
                  onClick={() => openWorkspace(ws)}
                >
                  <div className="flex h-24 items-center justify-center bg-muted/40">
                    {envIcon ? (
                      <span
                        className="h-12 w-12 [&_svg]:h-full [&_svg]:w-full"
                        dangerouslySetInnerHTML={{ __html: envIcon }}
                      />
                    ) : (
                      <span className="text-sm text-muted-foreground">{envName || "Workspace"}</span>
                    )}
                  </div>

                  <div className="flex flex-1 flex-col gap-2 p-3">
                    {renamingId === ws.id ? (
                      <form
                        className="flex items-center gap-2"
                        onSubmit={async (e) => {
                          e.preventDefault();
                          if (!renameValue.trim()) return;
                          setError(null);
                          try {
                            await apiClient.workspaces.renameWorkspace(ws.id, renameValue.trim());
                            setRenamingId(null);
                            await fetchAll();
                          } catch (err: unknown) {
                            setError(err instanceof Error ? err.message : "Failed to rename");
                          }
                        }}
                        onClick={(e) => e.stopPropagation()}
                      >
                        <Input
                          type="text"
                          value={renameValue}
                          onChange={(e) => setRenameValue(e.target.value)}
                          className="h-7"
                          autoFocus
                        />
                        <Button type="submit" size="sm" className="h-7">Save</Button>
                        <Button type="button" size="sm" variant="outline" className="h-7" onClick={() => setRenamingId(null)}>
                          Cancel
                        </Button>
                      </form>
                    ) : (
                      <>
                        <div className="flex items-center justify-between gap-2">
                          <span className="truncate font-semibold">{ws.name}</span>
                          {deletingIds.has(ws.id) ? (
                            <Badge variant="destructive">Deleting...</Badge>
                          ) : startingIds.has(ws.id) ? (
                            <Badge className="bg-amber-500 text-white">Starting...</Badge>
                          ) : ws.status !== "running" ? (
                            <Badge variant="outline">Stopped</Badge>
                          ) : (
                            <Badge className="bg-emerald-500 text-white">Running</Badge>
                          )}
                        </div>
                        {envName && <span className="text-xs text-muted-foreground">{envName}</span>}
                        {gitRemote && (
                          <span className="truncate font-mono text-xs text-muted-foreground">{shortRepoName(gitRemote)}</span>
                        )}
                        {tags.length > 0 && (
                          <div className="flex flex-wrap gap-1">
                            {tags.map((tag) => (
                              <Badge key={tag} variant="secondary" className="text-[10px]">{tag}</Badge>
                            ))}
                          </div>
                        )}
                      </>
                    )}

                    <div className="mt-auto flex flex-wrap gap-1" onClick={(e) => e.stopPropagation()}>
                      {deletingIds.has(ws.id) ? null : confirmDelete === ws.id ? (
                        <>
                          <span className="text-xs text-destructive">Delete?</span>
                          <Button size="sm" variant="destructive" className="h-7" onClick={() => handleDelete(ws.id)}>Yes</Button>
                          <Button size="sm" variant="outline" className="h-7" onClick={() => setConfirmDelete(null)}>No</Button>
                        </>
                      ) : renamingId === ws.id ? null : (
                        <>
                          <Button
                            size="sm" className="h-7"
                            disabled={startingIds.has(ws.id) || stoppingIds.has(ws.id)}
                            onClick={() => openWorkspace(ws)}
                          >
                            {startingIds.has(ws.id) ? "Starting..." : isOpen ? "View" : ws.status !== "running" ? "Start" : "Open"}
                          </Button>
                          {ws.status === "running" && (
                            <Button
                              size="sm" variant="outline" className="h-7"
                              disabled={stoppingIds.has(ws.id)}
                              onClick={() => handleStop(ws.id)}
                            >
                              {stoppingIds.has(ws.id) ? "Stopping..." : "Stop"}
                            </Button>
                          )}
                          <Button
                            size="sm" variant="outline" className="h-7"
                            onClick={() => { setRenamingId(ws.id); setRenameValue(ws.name); }}
                          >
                            Rename
                          </Button>
                          <Button
                            size="sm" variant="outline" className="h-7"
                            onClick={() => setSettingsId(ws.id)}
                            title="Workspace settings"
                          >
                            Settings
                          </Button>
                          <Button
                            size="sm" variant="destructive" className="h-7"
                            onClick={() => setConfirmDelete(ws.id)}
                          >
                            Delete
                          </Button>
                        </>
                      )}
                    </div>
                  </div>
                </Card>
              );
            })
          )}
        </div>

        {/* Workspace settings sidebar */}
        {settingsId && wsMap.get(settingsId) && (
          <WorkspaceSettings
            workspaceId={settingsId}
            workspace={wsMap.get(settingsId)!}
            environment={environments.find((e) => e.plugin_id === wsMap.get(settingsId)?.environment)}
            onClose={() => { setSettingsId(null); fetchAll(); }}
          />
        )}

        {/* Shared disks management sheet */}
        <Sheet open={showDisksPanel} onOpenChange={setShowDisksPanel}>
          <SheetContent className="w-[28rem] sm:max-w-[28rem]">
            <SheetHeader>
              <SheetTitle>Shared disks</SheetTitle>
            </SheetHeader>
            <div className="mt-4 flex flex-col gap-4">
              <p className="text-sm text-muted-foreground">
                Shared disks are reusable storage that can be attached to any workspace (credentials, config, shared data).
              </p>

              <div className="flex gap-2">
                <Input
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
                <Button
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
                </Button>
              </div>

              <div className="flex flex-col gap-1">
                {disks.filter((d) => d.type === "shared").length === 0 ? (
                  <div className="rounded-md border border-dashed p-3 text-center text-sm text-muted-foreground">
                    No shared disks yet.
                  </div>
                ) : (
                  disks.filter((d) => d.type === "shared").map((d) => (
                    <div key={d.id} className="flex items-center gap-2 rounded-md border px-2 py-1.5">
                      <span className="flex-1 truncate text-sm">{d.name}</span>
                      <span className="text-xs text-muted-foreground">{formatSize(d.size_bytes)}</span>
                      {confirmDeleteDisk === d.id ? (
                        <>
                          <span className="text-xs text-destructive">Delete?</span>
                          <Button
                            size="sm" variant="destructive" className="h-6"
                            onClick={() => {
                              apiClient.workspaces.deleteDisk(d.type, d.name)
                                .then(() => { setConfirmDeleteDisk(null); fetchAll(); })
                                .catch((err) => setError(err instanceof Error ? err.message : "Failed to delete"));
                            }}
                          >
                            Yes
                          </Button>
                          <Button size="sm" variant="outline" className="h-6" onClick={() => setConfirmDeleteDisk(null)}>No</Button>
                        </>
                      ) : (
                        <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => setConfirmDeleteDisk(d.id)} title="Delete disk">
                          <X className="h-3 w-3" />
                        </Button>
                      )}
                    </div>
                  ))
                )}
              </div>
            </div>
          </SheetContent>
        </Sheet>

        {/* Saved disks */}
        {orphanDisks.length > 0 && (
          <div className="mt-6">
            <h3 className="mb-3 text-sm font-semibold uppercase tracking-wide text-muted-foreground">
              Saved disks
            </h3>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {orphanDisks.map((v) => (
                <Card key={v.name} className="flex flex-col gap-2 p-3">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate font-semibold">{v.name}</span>
                    {confirmDelete === `vol:${v.name}` ? (
                      <div className="flex items-center gap-1">
                        <span className="text-xs text-destructive">Delete?</span>
                        <Button
                          size="sm" variant="destructive" className="h-7"
                          onClick={async () => {
                            try {
                              await apiClient.workspaces.deleteDisk(v.type, v.name);
                              setConfirmDelete(null);
                              await fetchAll();
                            } catch (e: unknown) {
                              setError(e instanceof Error ? e.message : "Failed to delete disk");
                            }
                          }}
                        >
                          Yes
                        </Button>
                        <Button size="sm" variant="outline" className="h-7" onClick={() => setConfirmDelete(null)}>No</Button>
                      </div>
                    ) : (
                      <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => setConfirmDelete(`vol:${v.name}`)} title="Delete disk">
                        <X className="h-3 w-3" />
                      </Button>
                    )}
                  </div>

                  <div className="flex justify-between text-xs text-muted-foreground">
                    <span>{formatSize(v.size_bytes)}</span>
                    {v.created_at && <span>{new Date(v.created_at).toLocaleDateString()}</span>}
                  </div>

                  {v.git_remote && (
                    <div className="truncate font-mono text-xs">{shortRepoName(v.git_remote)}</div>
                  )}

                  {(v.tags?.length ?? 0) > 0 && (
                    <div className="flex flex-wrap gap-1">
                      {v.tags!.filter((t) => t !== "git").map((tag) => (
                        <Badge key={tag} variant="secondary" className="text-[10px]">{tag}</Badge>
                      ))}
                    </div>
                  )}

                  {(v.extensions?.length ?? 0) > 0 && (
                    <div className="flex flex-col gap-1">
                      <span className="text-xs text-muted-foreground">
                        {v.extensions!.length} extension{v.extensions!.length !== 1 ? "s" : ""}
                      </span>
                      <div className="flex flex-wrap gap-1">
                        {v.extensions!.map((ext) => (
                          <Badge key={ext} variant="outline" className="text-[10px]">{ext}</Badge>
                        ))}
                      </div>
                    </div>
                  )}

                  {environments.length > 0 && (
                    <div className="mt-auto">
                      {launchDisk === v.name ? (
                        <div className="flex flex-wrap gap-1">
                          {environments.map((env) => (
                            <Button
                              key={env.plugin_id}
                              size="sm" className="h-7"
                              disabled={launching}
                              onClick={() => handleLaunchDisk(v.id, v.name, env.plugin_id)}
                            >
                              {env.name}
                            </Button>
                          ))}
                          <Button size="sm" variant="outline" className="h-7" onClick={() => setLaunchDisk(null)}>
                            Cancel
                          </Button>
                        </div>
                      ) : (
                        <Button size="sm" className="h-7" onClick={() => setLaunchDisk(v.name)}>
                          Launch
                        </Button>
                      )}
                    </div>
                  )}
                </Card>
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
              className={cn(
                "flex flex-1 flex-col",
                activeTab !== tabId && "hidden"
              )}
            >
              {(detectedPorts[tabId]?.length ?? 0) > 0 && (
                <div className="flex items-center gap-2 border-b bg-muted/30 px-3 py-1.5 text-xs">
                  <span className="font-semibold uppercase tracking-wide text-muted-foreground">Ports:</span>
                  {detectedPorts[tabId].map((port) => (
                    <a
                      key={port}
                      href={workspacePortProxyUrl(tabId, port)}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="rounded-md border px-2 py-0.5 font-mono hover:bg-accent"
                    >
                      {port}
                    </a>
                  ))}
                </div>
              )}
              <iframe
                ref={(el) => { iframeRefs.current[tabId] = el; }}
                src={iframeSrc(ws)}
                className="flex-1 border-0"
                title={`Workspace: ${ws.name}`}
                allow="clipboard-read; clipboard-write"
              />
            </div>
          );
        })}
    </div>
  );
}
