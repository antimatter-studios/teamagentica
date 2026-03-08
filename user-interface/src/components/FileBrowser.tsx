import { useEffect, useRef, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { useFileStore } from "../stores/fileStore";
import { useUploadStore } from "../stores/uploadStore";
import { downloadFile, formatBytes, filenameFromKey, folderName } from "../api/files";
import UploadQueue from "./UploadQueue";
import FileInfoPanel from "./FileInfoPanel";
import FolderTree from "./FolderTree";

export default function FileBrowser() {
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
    }))
  );

  const enqueue = useUploadStore((s) => s.enqueue);

  const fileInputRef = useRef<HTMLInputElement>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  useEffect(() => {
    loadProviders();
  }, [loadProviders]);

  const handleUpload = () => fileInputRef.current?.click();

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e.target.files && e.target.files.length > 0 && selectedProvider) {
      enqueue(selectedProvider.id, prefix, e.target.files);
      e.target.value = "";
    }
  };

  const handleDelete = (key: string) => {
    if (confirmDelete === key) {
      deleteFile(key);
      setConfirmDelete(null);
    } else {
      setConfirmDelete(key);
    }
  };

  const handleDownload = (key: string) => {
    if (!selectedProvider) return;
    downloadFile(selectedProvider.id, key);
  };

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
                activePath={selectedProvider?.id === p.id ? prefix : ""}
                onSelectProvider={selectProvider}
                onNavigate={browse}
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
                  onClick={() => browse("")}
                >
                  {selectedProvider?.name || selectedProvider?.id || "Disk"}
                </button>
                {breadcrumbs.map((part, i) => {
                  const crumbPath = breadcrumbs.slice(0, i + 1).join("/") + "/";
                  return (
                    <span key={crumbPath}>
                      <span className="file-breadcrumb-sep">/</span>
                      <button
                        className="file-breadcrumb-btn"
                        onClick={() => browse(crumbPath)}
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
              </div>
            </div>

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

                {/* Folders */}
                {folders.map((f) => (
                  <button
                    key={f}
                    className="file-row file-folder"
                    onClick={() => browse(f)}
                  >
                    <span className="file-col-name">{folderName(f)}/</span>
                    <span className="file-col-size">-</span>
                    <span className="file-col-type">folder</span>
                    <span className="file-col-modified">-</span>
                    <span className="file-col-actions"></span>
                  </button>
                ))}

                {/* Files */}
                {files.map((f) => (
                  <div
                    key={f.key}
                    className={`file-row file-selectable ${selectedFile?.key === f.key ? "selected" : ""}`}
                    onClick={() => selectFile(selectedFile?.key === f.key ? null : f)}
                  >
                    <span className="file-col-name">{filenameFromKey(f.key)}</span>
                    <span className="file-col-size">{formatBytes(f.size)}</span>
                    <span className="file-col-type">{f.content_type || "-"}</span>
                    <span className="file-col-modified">
                      {f.last_modified
                        ? new Date(f.last_modified).toLocaleDateString()
                        : "-"}
                    </span>
                    <span className="file-col-actions">
                      <button
                        className="file-action-btn"
                        onClick={(e) => { e.stopPropagation(); handleDownload(f.key); }}
                        title="Download"
                      >
                        &#x2B07;
                      </button>
                      <button
                        className={`file-action-btn file-delete-btn ${confirmDelete === f.key ? "confirm" : ""}`}
                        onClick={(e) => { e.stopPropagation(); handleDelete(f.key); }}
                        title={confirmDelete === f.key ? "Click again to confirm" : "Delete"}
                      >
                        {confirmDelete === f.key ? "?" : "\u2716"}
                      </button>
                    </span>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
      {selectedFile && selectedProvider && (
        <FileInfoPanel
          file={selectedFile}
          pluginId={selectedProvider.id}
          onClose={() => selectFile(null)}
        />
      )}
      <UploadQueue />
    </div>
  );
}
