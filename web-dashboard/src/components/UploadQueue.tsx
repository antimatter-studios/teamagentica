import { useShallow } from "zustand/react/shallow";
import { useUploadStore, formatBytes } from "../stores/uploadStore";

export default function UploadQueue() {
  const { items, panelOpen, cancel, dismiss, clearCompleted, togglePanel } =
    useUploadStore(
      useShallow((s) => ({
        items: s.items,
        panelOpen: s.panelOpen,
        cancel: s.cancel,
        dismiss: s.dismiss,
        clearCompleted: s.clearCompleted,
        togglePanel: s.togglePanel,
      }))
    );

  if (items.length === 0) return null;

  const active = items.filter((i) => i.status === "uploading" || i.status === "queued").length;
  const done = items.filter((i) => i.status === "done").length;
  const hasClearable = items.some((i) => i.status === "done" || i.status === "error" || i.status === "cancelled");

  return (
    <div className="upload-queue">
      <div className="upload-queue-header" onClick={togglePanel}>
        <span className="upload-queue-title">
          UPLOADS{active > 0 && ` (${active} active)`}{done > 0 && ` \u00b7 ${done} done`}
        </span>
        <span className="upload-queue-actions">
          {hasClearable && (
            <button
              className="upload-queue-clear"
              onClick={(e) => { e.stopPropagation(); clearCompleted(); }}
            >
              CLEAR
            </button>
          )}
          <span className="upload-queue-toggle">{panelOpen ? "\u25BC" : "\u25B2"}</span>
        </span>
      </div>
      {panelOpen && (
        <div className="upload-queue-list">
          {items.map((item) => (
            <UploadRow key={item.id} item={item} onCancel={cancel} onDismiss={dismiss} />
          ))}
        </div>
      )}
    </div>
  );
}

function UploadRow({
  item,
  onCancel,
  onDismiss,
}: {
  item: ReturnType<typeof useUploadStore.getState>["items"][0];
  onCancel: (id: string) => void;
  onDismiss: (id: string) => void;
}) {
  const pct = item.total > 0 ? Math.round((item.loaded / item.total) * 100) : 0;
  const eta =
    item.status === "uploading" && item.speed > 0
      ? Math.ceil((item.total - item.loaded) / item.speed)
      : null;

  return (
    <div className={`upload-row upload-status-${item.status}`}>
      <span className="upload-row-name" title={item.fileName}>
        {item.fileName.length > 28 ? item.fileName.slice(0, 25) + "..." : item.fileName}
      </span>
      <span className="upload-row-size">{formatBytes(item.fileSize)}</span>

      <span className="upload-row-progress">
        {(item.status === "uploading" || item.status === "done") && (
          <span className="upload-bar">
            <span
              className="upload-bar-fill"
              style={{ width: `${pct}%` }}
            />
          </span>
        )}
      </span>

      <span className="upload-row-info">
        {item.status === "uploading" && (
          <>
            {formatBytes(item.speed)}/s
            {eta !== null && ` \u00b7 ${eta}s`}
          </>
        )}
        {item.status === "queued" && <span className="upload-label-queued">Queued</span>}
        {item.status === "done" && <span className="upload-label-done">Done</span>}
        {item.status === "error" && <span className="upload-label-error">{item.error || "Error"}</span>}
        {item.status === "cancelled" && <span className="upload-label-cancelled">Cancelled</span>}
      </span>

      <span className="upload-row-action">
        {(item.status === "uploading" || item.status === "queued") && (
          <button className="upload-cancel-btn" onClick={() => onCancel(item.id)} title="Cancel">
            &#x2716;
          </button>
        )}
        {(item.status === "done" || item.status === "error" || item.status === "cancelled") && (
          <button className="upload-dismiss-btn" onClick={() => onDismiss(item.id)} title="Dismiss">
            &#x2716;
          </button>
        )}
      </span>
    </div>
  );
}
