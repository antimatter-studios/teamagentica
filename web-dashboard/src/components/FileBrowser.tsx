import { useEffect, useRef, useState, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import { useFileStore } from "../stores/fileStore";
import { useUploadStore } from "../stores/uploadStore";
import { apiClient } from "../api/client";
import ConfirmDialog from "./ConfirmDialog";
import { formatBytes, filenameFromKey, folderName } from "@teamagentica/api-client";

/** Simple file-type icon based on content_type or filename extension. */
function fileIcon(contentType: string, filename: string): string {
  if (contentType.startsWith("image/")) return "🖼";
  if (contentType.startsWith("video/")) return "🎬";
  if (contentType.startsWith("audio/")) return "🎵";
  if (contentType === "application/pdf") return "📄";
  if (contentType.startsWith("text/")) return "📝";
  const ext = filename.split(".").pop()?.toLowerCase() || "";
  if (["zip", "tar", "gz", "rar", "7z"].includes(ext)) return "📦";
  if (["json", "yaml", "yml", "xml", "toml"].includes(ext)) return "⚙";
  if (["js", "ts", "go", "py", "rs", "c", "cpp", "java", "sh"].includes(ext)) return "📜";
  return "📄";
}

async function downloadFile(pluginId: string, key: string) {
  const isFolder = key.endsWith("/");
  const blob = isFolder
    ? await apiClient.files.fetchZip(pluginId, key)
    : await apiClient.files.fetchBlob(pluginId, key);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = isFolder
    ? folderName(key) + ".zip"
    : filenameFromKey(key);
  document.body.appendChild(a); a.click();
  document.body.removeChild(a); URL.revokeObjectURL(url);
}
import UploadQueue from "./UploadQueue";
import FileInfoPanel from "./FileInfoPanel";
import FileViewer from "./FileViewer";
import FileOpsPanel from "./FileOpsPanel";
import FolderTree from "./FolderTree";
import { findPreview } from "./previews/registry";

interface FileBrowserProps {
  /** Initial subpath from URL, e.g. "provider-id/folder/subfolder/" */
  initialPath?: string;
  /** Callback to notify parent of navigation — parent owns the URL. */
  onPathChange?: (subpath: string) => void;
}

export default function FileBrowser({ initialPath, onPathChange }: FileBrowserProps) {
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

  // Notify parent of navigation so it can update the URL.
  const notifyPath = useCallback((providerId: string, pfx: string, trash?: boolean, fileKey?: string) => {
    const trashSegment = trash ? ".trash/" : "";
    const base = pfx ? `${providerId}/${trashSegment}${pfx}` : `${providerId}/${trashSegment}`;
    onPathChange?.(fileKey ? `${base}${filenameFromKey(fileKey)}` : base);
  }, [onPathChange]);

  // Wrap browse to also notify parent.
  const handleBrowse = useCallback((pfx: string) => {
    if (trashMode) {
      browseTrash(pfx);
    } else {
      browse(pfx);
    }
    if (selectedProvider) notifyPath(selectedProvider.id, pfx, trashMode);
  }, [browse, browseTrash, trashMode, selectedProvider, notifyPath]);

  // Wrap selectProvider to also notify parent.
  const handleSelectProvider = useCallback((p: Parameters<typeof selectProvider>[0]) => {
    selectProvider(p);
    notifyPath(p.id, "");
  }, [selectProvider, notifyPath]);

  useEffect(() => {
    loadProviders();
  }, [loadProviders]);

  // Restore location from URL on initial mount, after providers are loaded.
  useEffect(() => {
    if (restoredFromUrl.current || providers.length === 0 || !initialPath) return;
    restoredFromUrl.current = true;
    const slashIdx = initialPath.indexOf("/");
    const providerId = slashIdx === -1 ? initialPath : initialPath.slice(0, slashIdx);
    let urlPrefix = slashIdx === -1 ? "" : initialPath.slice(slashIdx + 1);
    const isTrash = urlPrefix.startsWith(".trash/");
    if (isTrash) urlPrefix = urlPrefix.slice(".trash/".length);

    // Check if URL points to a file (no trailing slash and has a dot in the last segment).
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
        // After browsing, find and open the file from the loaded file list.
        if (pendingFile) {
          const state = useFileStore.getState();
          const match = state.files.find(
            (f) => filenameFromKey(f.key) === pendingFile
          );
          if (match) viewFile(match);
        }
      };
      doBrowse();
    }
  }, [providers, initialPath, selectProvider, browse, browseTrash, setTrashMode, viewFile]);

  const handleUpload = () => fileInputRef.current?.click();

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0 && selectedProvider) {
      enqueue(selectedProvider.id, prefix, e.target.files);
      e.target.value = "";
    }
  };

  const handleDelete = (key: string) => {
    setDeleteModal({ key, permanent: false });
  };

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
    if (trimmed && trimmed !== filenameFromKey(key)) {
      renameFile(key, trimmed);
    }
    setRenamingKey(null);
  }, [renameFile]);

  const handleCreateSubmit = useCallback((name: string) => {
    const trimmed = name.trim();
    if (trimmed && creating) {
      if (creating === "folder") createFolder(trimmed);
      else createFile(trimmed);
    }
    setCreating(null);
  }, [creating, createFolder, createFile]);

  // Auto-focus and select filename (without extension) when entering rename mode.
  useEffect(() => {
    if (!renamingKey || !renameInputRef.current) return;
    const input = renameInputRef.current;
    input.focus();
    const dotIdx = input.value.lastIndexOf(".");
    input.setSelectionRange(0, dotIdx > 0 ? dotIdx : input.value.length);
  }, [renamingKey]);

  // Auto-focus create input.
  useEffect(() => {
    if (!creating || !createInputRef.current) return;
    createInputRef.current.focus();
    if (creating === "file") {
      // Select just "untitled" part, not ".txt"
      createInputRef.current.setSelectionRange(0, 8);
    }
  }, [creating]);

  // Close context menu on any click outside.
  useEffect(() => {
    if (!contextMenu) return;
    const handler = () => setContextMenu(null);
    window.addEventListener("click", handler);
    return () => window.removeEventListener("click", handler);
  }, [contextMenu]);

  const breadcrumbs = prefix
    ? prefix.replace(/\/$/, "").split("/")
    : [];

  return (
    <div className="file-layout">
      {/* Sidebar */}
      <div className="file-sidebar">
        <div className="plugin-sidebar-header">STORAGE</div>
        {providers.length === 0 ? (
          <div className="plugin-sidebar-empty">No storage providers found</div>
        ) : (
          <div className="folder-tree-list">
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
      </div>

      {/* Main panel */}
      <div className="file-detail">
        {!selectedProvider ? (
          <div className="plugin-detail-empty">
            <div className="plugin-detail-empty-icon">{">"}_</div>
            <p>Select a storage provider</p>
          </div>
        ) : (
          <>
            {/* Toolbar */}
            <div className="file-toolbar">
              <div className="file-breadcrumbs">
                <button
                  className="file-breadcrumb-btn"
                  onClick={() => {
                    if (trashMode) {
                      setTrashMode(false);
                      if (selectedProvider) notifyPath(selectedProvider.id, "");
                    } else {
                      handleBrowse("");
                    }
                  }}
                >
                  {selectedProvider?.name || selectedProvider?.id || "Disk"}
                </button>
                {trashMode && (
                  <>
                    <span className="file-breadcrumb-sep">/</span>
                    <button
                      className="file-breadcrumb-btn file-trash-crumb"
                      onClick={() => handleBrowse("")}
                    >
                      Trash
                    </button>
                  </>
                )}
                {breadcrumbs.map((part, i) => {
                  const crumbPath = breadcrumbs.slice(0, i + 1).join("/") + "/";
                  return (
                    <span key={crumbPath}>
                      <span className="file-breadcrumb-sep">/</span>
                      <button
                        className="file-breadcrumb-btn"
                        onClick={() => handleBrowse(crumbPath)}
                      >
                        {part}
                      </button>
                    </span>
                  );
                })}
              </div>
              <div className="file-toolbar-actions">
                <button className="file-action-btn" onClick={refresh} title="Refresh">
                  &#x21bb;
                </button>
                {!trashMode && (
                  <>
                    <button className="file-new-btn file-new-folder" onClick={() => setCreating("folder")} title="New Folder">
                      + FOLDER
                    </button>
                    <button className="file-new-btn file-new-file" onClick={() => setCreating("file")} title="New File">
                      + FILE
                    </button>
                    <button className="file-upload-btn" onClick={handleUpload}>
                      UPLOAD
                    </button>
                    <input
                      ref={fileInputRef}
                      type="file"
                      multiple
                      style={{ display: "none" }}
                      onChange={handleFileChange}
                    />
                  </>
                )}
                {trashMode && (
                  <button
                    className="file-action-btn file-delete-btn"
                    onClick={() => setEmptyTrashModal(true)}
                    title="Empty Trash"
                  >
                    EMPTY TRASH
                  </button>
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
              <>
                {error && <div className="file-error">{error}</div>}

                {loading ? (
                  <div className="file-loading">Loading...</div>
                ) : folders.length === 0 && files.length === 0 ? (
                  <div className="file-empty">Empty directory</div>
                ) : (
                  <div className="file-list">
                    {/* Header row */}
                    <div className="file-row file-header">
                      <span className="file-col-name">NAME</span>
                      <span className="file-col-size">SIZE</span>
                      <span className="file-col-type">TYPE</span>
                      <span className="file-col-modified">MODIFIED</span>
                      <span className="file-col-actions"></span>
                    </div>

                    {/* Inline create row */}
                    {creating && (
                      <div className="file-row file-create-row">
                        <span className="file-col-name">
                          <span className="file-icon">{creating === "folder" ? "📁" : "📄"}</span>
                          <input
                            ref={createInputRef}
                            className="file-rename-input"
                            defaultValue={creating === "file" ? "untitled.txt" : ""}
                            placeholder={creating === "folder" ? "Folder name" : "File name"}
                            onKeyDown={(e) => {
                              if (e.key === "Enter") handleCreateSubmit(e.currentTarget.value);
                              if (e.key === "Escape") setCreating(null);
                            }}
                            onBlur={(e) => handleCreateSubmit(e.currentTarget.value)}
                          />
                        </span>
                        <span className="file-col-size">-</span>
                        <span className="file-col-type">{creating}</span>
                        <span className="file-col-modified">-</span>
                        <span className="file-col-actions">
                          <button className="file-action-btn" onClick={() => setCreating(null)} title="Cancel">
                            &#x2716;
                          </button>
                        </span>
                      </div>
                    )}

                    {/* Folders */}
                    {folders.map((f) => (
                      <div
                        key={f}
                        className="file-row file-folder"
                        onClick={() => { if (renamingKey !== f) handleBrowse(f); }}
                        onContextMenu={(e) => handleContextMenu(e, f)}
                      >
                        <span className="file-col-name">
                          {renamingKey === f ? (
                            <input
                              ref={renameInputRef}
                              className="file-rename-input"
                              defaultValue={folderName(f)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") handleRenameSubmit(f, e.currentTarget.value);
                                if (e.key === "Escape") setRenamingKey(null);
                              }}
                              onBlur={(e) => handleRenameSubmit(f, e.currentTarget.value)}
                              onClick={(e) => e.stopPropagation()}
                            />
                          ) : (
                            <><span className="file-icon">📁</span>{folderName(f)}/</>
                          )}
                        </span>
                        <span className="file-col-size">-</span>
                        <span className="file-col-type">folder</span>
                        <span className="file-col-modified">-</span>
                        <span className="file-col-actions"></span>
                      </div>
                    ))}

                    {/* Files */}
                    {files.map((f) => (
                      <div
                        key={f.key}
                        className={`file-row file-selectable ${selectedFile?.key === f.key ? "selected" : ""}`}
                        onClick={() => { if (!hasOps) selectFile(selectedFile?.key === f.key ? null : f); }}
                        onContextMenu={(e) => handleContextMenu(e, f.key)}
                      >
                        <span className="file-col-name">
                          {renamingKey === f.key ? (
                            <input
                              ref={renameInputRef}
                              className="file-rename-input"
                              defaultValue={filenameFromKey(f.key)}
                              onKeyDown={(e) => {
                                if (e.key === "Enter") handleRenameSubmit(f.key, e.currentTarget.value);
                                if (e.key === "Escape") setRenamingKey(null);
                              }}
                              onBlur={(e) => handleRenameSubmit(f.key, e.currentTarget.value)}
                              onClick={(e) => e.stopPropagation()}
                            />
                          ) : (
                            <><span className="file-icon">{fileIcon(f.content_type || "", filenameFromKey(f.key))}</span>{filenameFromKey(f.key)}</>
                          )}
                        </span>
                        <span className="file-col-size">{formatBytes(f.size)}</span>
                        <span className="file-col-type">{f.content_type || "-"}</span>
                        <span className="file-col-modified">
                          {f.last_modified
                            ? new Date(f.last_modified).toLocaleString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' })
                            : "-"}
                        </span>
                        <span className="file-col-actions">
                          {trashMode ? (
                            <>
                              <button
                                className="file-action-btn file-restore-btn"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setRestoreModal(f.key);
                                }}
                                title="Restore"
                              >
                                &#x21A9;
                              </button>
                              <button
                                className="file-action-btn file-delete-btn"
                                onClick={(e) => {
                                  e.stopPropagation();
                                  setDeleteModal({ key: f.key, permanent: true });
                                }}
                                title="Delete permanently"
                              >
                                &#x2716;
                              </button>
                            </>
                          ) : (
                            <>
                              {canPreview(f) && (
                                <button
                                  className="file-action-btn"
                                  onClick={(e) => {
                                    e.stopPropagation();
                                    viewFile(f);
                                    if (selectedProvider) notifyPath(selectedProvider.id, prefix, false, f.key);
                                  }}
                                  title="View"
                                >
                                  &#x1F441;&#xFE0E;
                                </button>
                              )}
                              <button
                                className="file-action-btn"
                                onClick={(e) => { e.stopPropagation(); handleDownload(f.key); }}
                                title="Download"
                              >
                                &#x2B07;
                              </button>
                              <button
                                className="file-action-btn file-delete-btn"
                                onClick={(e) => { e.stopPropagation(); handleDelete(f.key); }}
                                title="Delete"
                              >
                                &#x2716;
                              </button>
                            </>
                          )}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </>
        )}
      </div>
      {contextMenu && (
        <div
          className="file-context-menu"
          style={{ top: contextMenu.y, left: contextMenu.x }}
          onClick={(e) => e.stopPropagation()}
        >
          <button
            className="file-context-menu-item"
            onClick={() => { setRenamingKey(contextMenu.key); closeContextMenu(); }}
          >
            Rename
          </button>
          <button
            className="file-context-menu-item"
            onClick={() => { duplicateFile(contextMenu.key); closeContextMenu(); }}
          >
            Duplicate
          </button>
          <button
            className="file-context-menu-item"
            onClick={() => { addCopyItem(contextMenu.key); closeContextMenu(); }}
          >
            Copy
          </button>
          <button
            className="file-context-menu-item"
            onClick={() => { addMoveItem(contextMenu.key); closeContextMenu(); }}
          >
            Move
          </button>
          <button
            className="file-context-menu-item"
            onClick={() => { handleDownload(contextMenu.key); closeContextMenu(); }}
          >
            Download
          </button>
          <button
            className="file-context-menu-item file-context-menu-danger"
            onClick={() => { handleDelete(contextMenu.key); closeContextMenu(); }}
          >
            Delete
          </button>
        </div>
      )}
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
      <UploadQueue />

      {deleteModal && (
        <ConfirmDialog
          title={deleteModal.permanent ? "Permanently delete" : "Delete"}
          onConfirm={() => {
            if (deleteModal.permanent) { emptyTrash(deleteModal.key); } else { deleteFile(deleteModal.key); }
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
