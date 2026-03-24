import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { formatBytes, filenameFromKey } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

const TEXT_TYPES = /^text\/|application\/json|application\/xml|application\/x-yaml/;
const TEXT_EXTENSIONS = /\.(txt|json|csv|xml|yaml|yml|toml|ini|cfg|conf|log|env|sh|bash|zsh|fish|bat|cmd|ps1|py|rb|js|jsx|ts|tsx|go|rs|c|cpp|h|hpp|java|kt|scala|swift|cs|php|lua|r|sql|graphql|gql|html|htm|css|scss|sass|less|vue|svelte|astro|makefile|dockerfile|gitignore|gitattributes|editorconfig|eslintrc|prettierrc|babelrc|md|mdx|markdown)$/i;

function isTextContent(contentType: string, filename: string): boolean {
  return TEXT_TYPES.test(contentType) || TEXT_EXTENSIONS.test(filename);
}

interface Props {
  file: StorageFile;
  pluginId: string;
  onClose: () => void;
}

export default function FileInfoPanel({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);
  const contentType = file.content_type || "";
  const ext = filename.includes(".") ? filename.split(".").pop()!.toUpperCase() : "-";

  const [src, setSrc] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(contentType, filename);
    return loader ? lazy(loader) : null;
  }, [contentType, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  useEffect(() => {
    setSrc(null);
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

    if (isTextContent(contentType, filename)) {
      apiClient.files.fetchText(pluginId, file.key)
        .then((text) => { if (!cancelled) setSrc(text); })
        .catch(() => {});
    } else {
      apiClient.files.fetchBlob(pluginId, file.key)
        .then((blob) => {
          if (cancelled) return;
          const url = URL.createObjectURL(blob);
          blobUrlRef.current = url;
          setSrc(url);
        })
        .catch(() => {});
    }

    return () => {
      cancelled = true;
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, [pluginId, file.key, contentType, filename]);

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
          {src !== null ? (
            <Preview src={src} filename={filename} contentType={contentType} />
          ) : (
            <div className="file-preview-placeholder">Loading...</div>
          )}
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
