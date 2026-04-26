import { useEffect, useRef, useState, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  ChevronRight,
  Download,
  Eye,
  File as FileIco,
  FileText,
  Film,
  FolderIcon,
  HardDrive,
  Image as ImageIco,
  Music,
  Package,
  RefreshCw,
  RotateCcw,
  Settings as SettingsIco,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { useFileStore } from "../stores/fileStore";
import { useUploadStore } from "../stores/uploadStore";
import { apiClient } from "../api/client";
import ConfirmDialog from "./ConfirmDialog";
import { formatBytes, filenameFromKey, folderName } from "@teamagentica/api-client";
import UploadQueue from "./UploadQueue";
import FileInfoPanel from "./FileInfoPanel";
import FileViewer from "./FileViewer";
import FileOpsPanel from "./FileOpsPanel";
import FolderTree from "./FolderTree";
import { findPreview } from "./previews/registry";

function FileTypeIcon({ contentType, filename, className }: { contentType: string; filename: string; className?: string }) {
  if (contentType.startsWith("image/")) return <ImageIco className={className} />;
  if (contentType.startsWith("video/")) return <Film className={className} />;
  if (contentType.startsWith("audio/")) return <Music className={className} />;
  if (contentType === "application/pdf") return <FileText className={className} />;
  if (contentType.startsWith("text/")) return <FileText className={className} />;
  const ext = filename.split(".").pop()?.toLowerCase() || "";
  if (["zip", "tar", "gz", "rar", "7z"].includes(ext)) return <Package className={className} />;
  if (["json", "yaml", "yml", "xml", "toml"].includes(ext)) return <SettingsIco className={className} />;
  return <FileIco className={className} />;
}

function friendlyType(filename: string): string {
  const ext = filename.split(".").pop()?.toLowerCase() || "";
  if (!ext || ext === filename.toLowerCase()) return "file";
  return ext.toUpperCase();
}

async function downloadFile(pluginId: string, key: string) {
  const isFolder = key.endsWith("/");
  const blob = isFolder
    ? await apiClient.files.fetchZip(pluginId, key)
    : await apiClient.files.fetchBlob(pluginId, key);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = isFolder ? folderName(key) + ".zip" : filenameFromKey(key);
  document.body.appendChild(a); a.click();
  document.body.removeChild(a); URL.revokeObjectURL(url);
}

interface FileBrowserProps {
  initialPath?: string;
  onPathChange?: (subpath: string) => void;
  onTitleChange?: (segment: string) => void;
}

export default function FileBrowser({ initialPath, onPathChange, onTitleChange }: FileBrowserProps) {
  const {
    providers,
    selectedProvider,
    prefix,
    folders,
    files,
    loading,
    error,
    loadProviders,
    selectProvider,
    browse,
    deleteFile,
    refresh,
    selectedFile,
    selectFile,
    viewingFile,
    viewFile,
    trashMode,
    setTrashMode,
    browseTrash,
    restoreFile,
    emptyTrash,
    sidebarVersion,
  } = useFileStore(
    useShallow((s) => ({
      providers: s.providers,
      selectedProvider: s.selectedProvider,
      prefix: s.prefix,
      folders: s.folders,
      files: s.files,
      loading: s.loading,
      error: s.error,
      loadProviders: s.loadProviders,
      selectProvider: s.selectProvider,
      browse: s.browse,
      deleteFile: s.deleteFile,
      refresh: s.refresh,
      selectedFile: s.selectedFile,
      selectFile: s.selectFile,
      viewingFile: s.viewingFile,
      viewFile: s.viewFile,
      trashMode: s.trashMode,
      setTrashMode: s.setTrashMode,
      browseTrash: s.browseTrash,
      restoreFile: s.restoreFile,
      emptyTrash: s.emptyTrash,
      sidebarVersion: s.sidebarVersion,
    }))
  );

  const duplicateFile = useFileStore((s) => s.duplicateFile);
  const renameFile = useFileStore((s) => s.renameFile);
  const addCopyItem = useFileStore((s) => s.addCopyItem);
  const addMoveItem = useFileStore((s) => s.addMoveItem);
  const copyItems = useFileStore((s) => s.copyItems);
  const moveItems = useFileStore((s) => s.moveItems);
  const enqueue = useUploadStore((s) => s.enqueue);

  const hasOps = copyItems.length > 0 || moveItems.length > 0;

  const createFolder = useFileStore((s) => s.createFolder);
  const createFile = useFileStore((s) => s.createFile);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [deleteModal, setDeleteModal] = useState<{ key: string; permanent?: boolean } | null>(null);
  const [emptyTrashModal, setEmptyTrashModal] = useState(false);
  const [restoreModal, setRestoreModal] = useState<string | null>(null);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number; key: string } | null>(null);
  const [renamingKey, setRenamingKey] = useState<string | null>(null);
  const [creating, setCreating] = useState<"folder" | "file" | null>(null);
  const renameInputRef = useRef<HTMLInputElement>(null);
  const createInputRef = useRef<HTMLInputElement>(null);
  const restoredFromUrl = useRef(false);

  const buildSubpath = useCallback((providerId: string, pfx: string, trash?: boolean, fileKey?: string) => {
    const trashSegment = trash ? ".trash/" : "";
    const base = pfx ? `${providerId}/${trashSegment}${pfx}` : `${providerId}/${trashSegment}`;
    return fileKey ? `${base}${filenameFromKey(fileKey)}` : base;
  }, []);

  const folderHref = useCallback((pfx: string, providerId?: string, trash?: boolean) => {
    const pid = providerId || selectedProvider?.id || "";
    return `/files/${buildSubpath(pid, pfx, trash)}`;
  }, [buildSubpath, selectedProvider]);

  const notifyPath = useCallback((providerId: string, pfx: string, trash?: boolean, fileKey?: string) => {
    onPathChange?.(buildSubpath(providerId, pfx, trash, fileKey));
  }, [onPathChange, buildSubpath]);

  const handleBrowse = useCallback((pfx: string) => {
    if (trashMode) browseTrash(pfx);
    else browse(pfx);
    if (selectedProvider) notifyPath(selectedProvider.id, pfx, trashMode);
  }, [browse, browseTrash, trashMode, selectedProvider, notifyPath]);

  const handleSelectProvider = useCallback((p: Parameters<typeof selectProvider>[0]) => {
    selectProvider(p);
    notifyPath(p.id, "");
  }, [selectProvider, notifyPath]);

  useEffect(() => { loadProviders(); }, [loadProviders]);

  useEffect(() => {
    if (restoredFromUrl.current || providers.length === 0 || !initialPath) return;
    restoredFromUrl.current = true;
    const slashIdx = initialPath.indexOf("/");
    const providerId = slashIdx === -1 ? initialPath : initialPath.slice(0, slashIdx);
    let urlPrefix = slashIdx === -1 ? "" : initialPath.slice(slashIdx + 1);
    const isTrash = urlPrefix.startsWith(".trash/");
    if (isTrash) urlPrefix = urlPrefix.slice(".trash/".length);

    let pendingFile: string | null = null;
    if (urlPrefix && !urlPrefix.endsWith("/")) {
      const lastSlash = urlPrefix.lastIndexOf("/");
      const filename = lastSlash === -1 ? urlPrefix : urlPrefix.slice(lastSlash + 1);
      if (filename.includes(".")) {
        pendingFile = filename;
        urlPrefix = lastSlash === -1 ? "" : urlPrefix.slice(0, lastSlash + 1);
      }
    }

    const provider = providers.find((p) => p.id === providerId);
    if (provider) {
      selectProvider(provider);
      const doBrowse = async () => {
        if (isTrash) {
          setTrashMode(true);
          if (urlPrefix) await browseTrash(urlPrefix);
        } else if (urlPrefix) {
          await browse(urlPrefix);
        }
        if (pendingFile) {
          const state = useFileStore.getState();
          const match = state.files.find((f) => filenameFromKey(f.key) === pendingFile);
          if (match) viewFile(match);
        }
      };
      doBrowse();
    }
  }, [providers, initialPath, selectProvider, browse, browseTrash, setTrashMode, viewFile]);

  useEffect(() => {
    if (!onTitleChange) return;
    if (viewingFile) onTitleChange(filenameFromKey(viewingFile.key));
    else if (selectedFile) onTitleChange(filenameFromKey(selectedFile.key));
    else if (prefix) {
      const parts = prefix.replace(/\/+$/, "").split("/");
      onTitleChange(parts[parts.length - 1]);
    } else if (selectedProvider) {
      onTitleChange(selectedProvider.name || selectedProvider.id);
    } else {
      onTitleChange("");
    }
  }, [viewingFile, selectedFile, prefix, selectedProvider, onTitleChange]);

  const handleUpload = () => fileInputRef.current?.click();

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0 && selectedProvider) {
      enqueue(selectedProvider.id, prefix, e.target.files);
      e.target.value = "";
    }
  };

  const handleDelete = (key: string) => setDeleteModal({ key, permanent: false });

  const handleDownload = (key: string) => {
    if (!selectedProvider) return;
    downloadFile(selectedProvider.id, key);
  };

  const canPreview = useCallback((f: { content_type: string; key: string }) => {
    return !!findPreview(f.content_type || "", filenameFromKey(f.key));
  }, []);

  const handleContextMenu = useCallback((e: React.MouseEvent, key: string) => {
    e.preventDefault();
    e.stopPropagation();
    setContextMenu({ x: e.clientX, y: e.clientY, key });
  }, []);

  const closeContextMenu = useCallback(() => setContextMenu(null), []);

  const handleRenameSubmit = useCallback((key: string, newName: string) => {
    const trimmed = newName.trim();
    if (trimmed && trimmed !== filenameFromKey(key)) renameFile(key, trimmed);
    setRenamingKey(null);
  }, [renameFile]);

  const createSubmittedRef = useRef(false);
  const handleCreateSubmit = useCallback((name: string) => {
    if (createSubmittedRef.current) return;
    createSubmittedRef.current = true;
    const trimmed = name.trim();
    if (trimmed && creating) {
      if (creating === "folder") createFolder(trimmed);
      else createFile(trimmed);
    }
    setCreating(null);
  }, [creating, createFolder, createFile]);

  useEffect(() => {
    if (!renamingKey || !renameInputRef.current) return;
    const input = renameInputRef.current;
    input.focus();
    const dotIdx = input.value.lastIndexOf(".");
    input.setSelectionRange(0, dotIdx > 0 ? dotIdx : input.value.length);
  }, [renamingKey]);

  useEffect(() => {
    createSubmittedRef.current = false;
    if (!creating || !createInputRef.current) return;
    createInputRef.current.focus();
    if (creating === "file") createInputRef.current.setSelectionRange(0, 8);
  }, [creating]);

  useEffect(() => {
    if (!contextMenu) return;
    const handler = () => setContextMenu(null);
    window.addEventListener("click", handler);
    return () => window.removeEventListener("click", handler);
  }, [contextMenu]);

  const breadcrumbs = prefix ? prefix.replace(/\/$/, "").split("/") : [];

  return (
    <div className="flex h-full w-full">
      {/* Sidebar */}
      <aside className="flex w-64 shrink-0 flex-col border-r">
        <div className="flex items-center gap-2 border-b p-3">
          <HardDrive className="h-4 w-4" />
          <span className="text-sm font-semibold uppercase tracking-wide">Storage</span>
        </div>
        {providers.length === 0 ? (
          <div className="p-4 text-center text-xs text-muted-foreground">No storage providers found</div>
        ) : (
          <div className="flex flex-col gap-1 overflow-y-auto p-2">
            {providers.map((p) => (
              <FolderTree
                key={p.id}
                provider={p}
                isSelected={selectedProvider?.id === p.id}
                activePath={selectedProvider?.id === p.id && !trashMode ? prefix : ""}
                trashActive={selectedProvider?.id === p.id && trashMode}
                refreshVersion={sidebarVersion}
                onSelectProvider={handleSelectProvider}
                onNavigate={(pfx) => {
                  if (trashMode) setTrashMode(false);
                  if (selectedProvider?.id !== p.id) selectProvider(p);
                  browse(pfx);
                  notifyPath(p.id, pfx);
                }}
                onTrashClick={() => {
                  if (selectedProvider?.id !== p.id) handleSelectProvider(p);
                  setTrashMode(true);
                  notifyPath(p.id, "", true);
                }}
              />
            ))}
          </div>
        )}
      </aside>

      {/* Main panel */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {!selectedProvider ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
            <HardDrive className="h-8 w-8" />
            <p>Select a storage provider</p>
          </div>
        ) : (
          <>
            {/* Toolbar */}
            <div className="flex flex-wrap items-center justify-between gap-2 border-b p-2">
              <div className="flex flex-wrap items-center gap-1 text-sm">
                <a
                  href={folderHref("")}
                  className="rounded px-2 py-1 font-medium hover:bg-accent"
                  onClick={(e) => {
                    e.preventDefault();
                    if (trashMode) {
                      setTrashMode(false);
                      if (selectedProvider) notifyPath(selectedProvider.id, "");
                    } else handleBrowse("");
                  }}
                >
                  {selectedProvider?.name || selectedProvider?.id || "Disk"}
                </a>
                {trashMode && (
                  <>
                    <ChevronRight className="h-3 w-3 text-muted-foreground" />
                    <a
                      href={folderHref("", undefined, true)}
                      className="rounded px-2 py-1 font-medium text-amber-500 hover:bg-accent"
                      onClick={(e) => { e.preventDefault(); handleBrowse(""); }}
                    >
                      Trash
                    </a>
                  </>
                )}
                {breadcrumbs.map((part, i) => {
                  const crumbPath = breadcrumbs.slice(0, i + 1).join("/") + "/";
                  return (
                    <span key={crumbPath} className="flex items-center gap-1">
                      <ChevronRight className="h-3 w-3 text-muted-foreground" />
                      <a
                        href={folderHref(crumbPath, undefined, trashMode)}
                        className="rounded px-2 py-1 hover:bg-accent"
                        onClick={(e) => { e.preventDefault(); handleBrowse(crumbPath); }}
                      >
                        {part}
                      </a>
                    </span>
                  );
                })}
              </div>
              <div className="flex items-center gap-1">
                <Button variant="ghost" size="icon" onClick={refresh} title="Refresh" className="h-8 w-8">
                  <RefreshCw className="h-4 w-4" />
                </Button>
                {!trashMode && (
                  <>
                    <Button variant="outline" size="sm" onClick={() => setCreating("folder")} title="New Folder">
                      + Folder
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => setCreating("file")} title="New File">
                      + File
                    </Button>
                    <Button variant="default" size="sm" onClick={handleUpload}>
                      <Upload className="mr-1 h-4 w-4" /> Upload
                    </Button>
                    <input
                      ref={fileInputRef}
                      type="file"
                      multiple
                      className="hidden"
                      onChange={handleFileChange}
                    />
                  </>
                )}
                {trashMode && (
                  <Button variant="destructive" size="sm" onClick={() => setEmptyTrashModal(true)}>
                    Empty trash
                  </Button>
                )}
              </div>
            </div>

            {viewingFile ? (
              <FileViewer
                file={viewingFile}
                pluginId={selectedProvider.id}
                onClose={() => {
                  viewFile(null);
                  notifyPath(selectedProvider.id, prefix, trashMode);
                }}
              />
            ) : (
              <div className="flex-1 overflow-auto">
                {error && (
                  <Alert variant="destructive" className="m-3">
                    <AlertDescription>{error}</AlertDescription>
                  </Alert>
                )}

                {loading ? (
                  <div className="p-8 text-center text-sm text-muted-foreground">Loading...</div>
                ) : folders.length === 0 && files.length === 0 ? (
                  <div className="p-8 text-center text-sm text-muted-foreground">Empty directory</div>
                ) : (
                  <div className="flex flex-col">
                    {/* Header */}
                    <div className="grid grid-cols-[1fr_8rem_6rem_12rem_8rem] items-center gap-2 border-b bg-muted/30 px-3 py-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                      <span>Name</span>
                      <span>Size</span>
                      <span>Type</span>
                      <span>Modified</span>
                      <span></span>
                    </div>

                    {creating && (
                      <div className="grid grid-cols-[1fr_8rem_6rem_12rem_8rem] items-center gap-2 border-b bg-primary/5 px-3 py-1.5 text-sm">
                        <span className="flex items-center gap-2">
                          {creating === "folder" ? <FolderIcon className="h-4 w-4" /> : <FileIco className="h-4 w-4" />}
                          <Input
                            ref={createInputRef}
                            className="h-7"
                            defaultValue={creating === "file" ? "untitled.txt" : ""}
                            placeholder={creating === "folder" ? "Folder name" : "File name"}
                            onKeyDown={(e) => {
                              if (e.key === "Enter") handleCreateSubmit(e.currentTarget.value);
                              if (e.key === "Escape") setCreating(null);
                            }}
                          />
                        </span>
                        <span className="text-muted-foreground">-</span>
                        <span className="text-muted-foreground">{creating}</span>
                        <span className="text-muted-foreground">-</span>
                        <span className="flex justify-end">
                          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setCreating(null)}>
                            <X className="h-3.5 w-3.5" />
                          </Button>
                        </span>
                      </div>
                    )}

                    {/* Folders */}
                    {folders.map((f) => (
                      <a
                        key={f}
                        href={folderHref(f, undefined, trashMode)}
                        className="grid grid-cols-[1fr_8rem_6rem_12rem_8rem] items-center gap-2 border-b px-3 py-1.5 text-sm hover:bg-accent"
                        onClick={(e) => { e.preventDefault(); if (renamingKey !== f) handleBrowse(f); }}
                        onContextMenu={(e) => handleContextMenu(e, f)}
                      >
                        <span className="flex items-center gap-2 truncate">
                          {renamingKey === f ? (
                            <Input
                              ref={renameInputRef}
                              className="h-7"
                              defaultValue={folderName(f)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") handleRenameSubmit(f, e.currentTarget.value);
                                if (e.key === "Escape") setRenamingKey(null);
                              }}
                              onBlur={(e) => handleRenameSubmit(f, e.currentTarget.value)}
                              onClick={(e) => e.stopPropagation()}
                            />
                          ) : (
                            <>
                              <FolderIcon className="h-4 w-4 shrink-0 text-amber-500" />
                              <span className="truncate">{folderName(f)}/</span>
                            </>
                          )}
                        </span>
                        <span className="text-muted-foreground">-</span>
                        <span className="text-muted-foreground">folder</span>
                        <span className="text-muted-foreground">-</span>
                        <span></span>
                      </a>
                    ))}

                    {/* Files */}
                    {files.map((f) => (
                      <div
                        key={f.key}
                        className={cn(
                          "grid cursor-pointer grid-cols-[1fr_8rem_6rem_12rem_8rem] items-center gap-2 border-b px-3 py-1.5 text-sm hover:bg-accent",
                          selectedFile?.key === f.key && "bg-accent"
                        )}
                        onClick={() => { if (!hasOps) selectFile(selectedFile?.key === f.key ? null : f); }}
                        onContextMenu={(e) => handleContextMenu(e, f.key)}
                      >
                        <span className="flex items-center gap-2 truncate">
                          {renamingKey === f.key ? (
                            <Input
                              ref={renameInputRef}
                              className="h-7"
                              defaultValue={filenameFromKey(f.key)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") handleRenameSubmit(f.key, e.currentTarget.value);
                                if (e.key === "Escape") setRenamingKey(null);
                              }}
                              onBlur={(e) => handleRenameSubmit(f.key, e.currentTarget.value)}
                              onClick={(e) => e.stopPropagation()}
                            />
                          ) : (
                            <>
                              <FileTypeIcon
                                contentType={f.content_type || ""}
                                filename={filenameFromKey(f.key)}
                                className="h-4 w-4 shrink-0 text-muted-foreground"
                              />
                              <span className="truncate">{filenameFromKey(f.key)}</span>
                            </>
                          )}
                        </span>
                        <span className="text-muted-foreground">{formatBytes(f.size)}</span>
                        <span className="text-muted-foreground">{friendlyType(filenameFromKey(f.key))}</span>
                        <span className="truncate text-muted-foreground">
                          {f.last_modified
                            ? new Date(f.last_modified).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
                            : "-"}
                        </span>
                        <span className="flex justify-end gap-1">
                          {trashMode ? (
                            <>
                              <Button
                                size="icon" variant="ghost" className="h-7 w-7"
                                onClick={(e) => { e.stopPropagation(); setRestoreModal(f.key); }}
                                title="Restore"
                              >
                                <RotateCcw className="h-3.5 w-3.5" />
                              </Button>
                              <Button
                                size="icon" variant="ghost" className="h-7 w-7 text-destructive"
                                onClick={(e) => { e.stopPropagation(); setDeleteModal({ key: f.key, permanent: true }); }}
                                title="Delete permanently"
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </>
                          ) : (
                            <>
                              {canPreview(f) && (
                                <Button
                                  size="icon" variant="ghost" className="h-7 w-7"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    viewFile(f);
                                    if (selectedProvider) notifyPath(selectedProvider.id, prefix, false, f.key);
                                  }}
                                  title="View"
                                >
                                  <Eye className="h-3.5 w-3.5" />
                                </Button>
                              )}
                              <Button
                                size="icon" variant="ghost" className="h-7 w-7"
                                onClick={(e) => { e.stopPropagation(); handleDownload(f.key); }}
                                title="Download"
                              >
                                <Download className="h-3.5 w-3.5" />
                              </Button>
                              <Button
                                size="icon" variant="ghost" className="h-7 w-7 text-destructive"
                                onClick={(e) => { e.stopPropagation(); handleDelete(f.key); }}
                                title="Delete"
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            </>
                          )}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </>
        )}
      </div>

      {contextMenu && (
        <DropdownMenu open onOpenChange={(open) => { if (!open) closeContextMenu(); }}>
          <DropdownMenuTrigger asChild>
            <span
              style={{ position: "fixed", top: contextMenu.y, left: contextMenu.x, width: 1, height: 1 }}
            />
          </DropdownMenuTrigger>
          <DropdownMenuContent onClick={(e) => e.stopPropagation()}>
            <DropdownMenuItem onClick={() => { setRenamingKey(contextMenu.key); closeContextMenu(); }}>
              Rename
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { duplicateFile(contextMenu.key); closeContextMenu(); }}>
              Duplicate
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { addCopyItem(contextMenu.key); closeContextMenu(); }}>
              Copy
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { addMoveItem(contextMenu.key); closeContextMenu(); }}>
              Move
            </DropdownMenuItem>
            <DropdownMenuItem onClick={() => { handleDownload(contextMenu.key); closeContextMenu(); }}>
              Download
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              className="text-destructive"
              onClick={() => { handleDelete(contextMenu.key); closeContextMenu(); }}
            >
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}

      {/* Right side panel */}
      <aside className={cn("w-80 shrink-0 border-l", !(hasOps || (selectedFile && selectedProvider)) && "hidden")}>
        {hasOps ? (
          <FileOpsPanel />
        ) : (
          selectedFile && selectedProvider && (
            <FileInfoPanel
              file={selectedFile}
              pluginId={selectedProvider.id}
              onClose={() => selectFile(null)}
            />
          )
        )}
      </aside>

      <UploadQueue />
      {deleteModal && (
        <ConfirmDialog
          title={deleteModal.permanent ? "Permanently delete" : "Delete"}
          onConfirm={() => {
            if (deleteModal.permanent) emptyTrash(deleteModal.key);
            else deleteFile(deleteModal.key);
            setDeleteModal(null);
          }}
          onCancel={() => setDeleteModal(null)}
        >
          Are you sure you want to {deleteModal.permanent ? "permanently delete" : "delete"}{" "}
          <strong>"{filenameFromKey(deleteModal.key)}"</strong>?
          {deleteModal.permanent && " This cannot be undone."}
        </ConfirmDialog>
      )}

      {emptyTrashModal && (
        <ConfirmDialog
          title="Empty Trash"
          onConfirm={() => { emptyTrash(); setEmptyTrashModal(false); }}
          onCancel={() => setEmptyTrashModal(false)}
        >
          Are you sure you want to permanently delete all items in the trash? This cannot be undone.
        </ConfirmDialog>
      )}

      {restoreModal && (
        <ConfirmDialog
          title="Restore file"
          variant="primary"
          onConfirm={() => { restoreFile(restoreModal); setRestoreModal(null); }}
          onCancel={() => setRestoreModal(null)}
        >
          Do you want to restore <strong>"{filenameFromKey(restoreModal)}"</strong>?
        </ConfirmDialog>
      )}

    </div>
  );
}
