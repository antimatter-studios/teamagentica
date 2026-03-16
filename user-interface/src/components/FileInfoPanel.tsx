import { lazy, Suspense, useMemo } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { formatBytes, filenameFromKey } from "@teamagentica/api-client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

interface Props {
  file: StorageFile;
  pluginId: string;
  onClose: () => void;
}

export default function FileInfoPanel({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);
  const ext = filename.includes(".") ? filename.split(".").pop()!.toUpperCase() : "-";

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(file.content_type || "", filename);
    return loader ? lazy(loader) : null;
  }, [file.content_type, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  return (
    <div className="file-info-panel">
      <div className="file-info-header">
        <span className="file-info-title">Info</span>
        <button className="file-info-close" onClick={onClose} title="Close">
          &#x2715;
        </button>
      </div>

      <div className="file-info-body">
        {/* Preview area */}
        <Suspense fallback={<div className="file-preview-placeholder">Loading...</div>}>
          <Preview file={file} pluginId={pluginId} />
        </Suspense>

        {/* Properties */}
        <div className="file-info-props">
          <InfoRow label="Name" value={filename} />
          <InfoRow label="Path" value={file.key} />
          <InfoRow label="Size" value={formatBytes(file.size)} />
          <InfoRow label="Type" value={file.content_type || "-"} />
          <InfoRow label="Extension" value={ext} />
          <InfoRow
            label="Modified"
            value={
              file.last_modified
                ? new Date(file.last_modified).toLocaleString()
                : "-"
            }
          />
          {file.etag && <InfoRow label="ETag" value={file.etag} />}
        </div>
      </div>
    </div>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="file-info-row">
      <span className="file-info-label">{label}</span>
      <span className="file-info-value">{value}</span>
    </div>
  );
}
