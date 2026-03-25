import { useShallow } from "zustand/react/shallow";
import { filenameFromKey } from "@teamagentica/api-client";
import { useFileStore } from "../stores/fileStore";

export default function FileOpsPanel() {
  const {
    copyItems,
    moveItems,
    removeCopyItem,
    removeMoveItem,
    pasteCopyItem,
    pasteMoveItem,
    clearOps,
    prefix,
  } = useFileStore(
    useShallow((s) => ({
      copyItems: s.copyItems,
      moveItems: s.moveItems,
      removeCopyItem: s.removeCopyItem,
      removeMoveItem: s.removeMoveItem,
      pasteCopyItem: s.pasteCopyItem,
      pasteMoveItem: s.pasteMoveItem,
      clearOps: s.clearOps,
      prefix: s.prefix,
    }))
  );

  const handleClose = () => {
    if (window.confirm("Clear all pending operations?")) {
      clearOps();
    }
  };

  const destLabel = prefix || "/";

  return (
    <div className="file-info-panel">
      <div className="file-info-header">
        <span className="file-info-title">FILE OPERATIONS</span>
        <button className="file-info-close" onClick={handleClose} title="Clear all">
          &#x2715;
        </button>
      </div>

      <div className="file-ops-body">
        <div className="file-ops-dest">
          Destination: <span className="file-ops-dest-path">{destLabel}</span>
        </div>

        {copyItems.length > 0 && (
          <div className="file-ops-section">
            <div className="file-ops-section-title">COPY ({copyItems.length})</div>
            {copyItems.map((key) => (
              <div key={key} className="file-ops-item">
                <span className="file-ops-item-name" title={key}>
                  {filenameFromKey(key)}
                </span>
                <div className="file-ops-item-actions">
                  <button
                    className="file-ops-paste"
                    onClick={() => pasteCopyItem(key)}
                    title={`Copy to ${destLabel}`}
                  >
                    Paste
                  </button>
                  <button
                    className="file-ops-remove"
                    onClick={() => removeCopyItem(key)}
                    title="Remove"
                  >
                    &#x2715;
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {moveItems.length > 0 && (
          <div className="file-ops-section">
            <div className="file-ops-section-title">MOVE ({moveItems.length})</div>
            {moveItems.map((key) => (
              <div key={key} className="file-ops-item">
                <span className="file-ops-item-name" title={key}>
                  {filenameFromKey(key)}
                </span>
                <div className="file-ops-item-actions">
                  <button
                    className="file-ops-paste"
                    onClick={() => pasteMoveItem(key)}
                    title={`Move to ${destLabel}`}
                  >
                    Paste
                  </button>
                  <button
                    className="file-ops-remove"
                    onClick={() => removeMoveItem(key)}
                    title="Remove"
                  >
                    &#x2715;
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
