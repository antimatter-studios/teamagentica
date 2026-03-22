import { lazy, Suspense, useMemo } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { filenameFromKey, formatBytes } from "@teamagentica/api-client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

interface Props {
  file: StorageFile;
  pluginId: string;
  onClose: () => void;
}

export default function FileViewer({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(file.content_type || "", filename);
    return loader ? lazy(loader) : null;
  }, [file.content_type, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  return (
    <div className="file-viewer">
      <div className="file-viewer-header">
        <button className="file-viewer-back" onClick={onClose} title="Back to file list">
          &#x2190; Back
        </button>
        <span className="file-viewer-filename">{filename}</span>
        <span className="file-viewer-meta">{formatBytes(file.size)}</span>
      </div>
      <div className="file-viewer-body">
        <Suspense fallback={<div className="file-preview-placeholder">Loading...</div>}>
          <Preview file={file} pluginId={pluginId} />
        </Suspense>
      </div>
    </div>
  );
}
