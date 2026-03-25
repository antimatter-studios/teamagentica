import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { filenameFromKey, formatBytes } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

/** Text-based content types that should be fetched as text, not blob */
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

export default function FileViewer({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);
  const contentType = file.content_type || "";

  const [src, setSrc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(contentType, filename);
    return loader ? lazy(loader) : null;
  }, [contentType, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  // Fetch content
  useEffect(() => {
    setSrc(null);
    setError(null);
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

    if (isTextContent(contentType, filename)) {
      apiClient.files.fetchText(pluginId, file.key)
        .then((text) => {
          if (!cancelled) setSrc(text);
        })
        .catch((err) => {
          if (!cancelled) setError(err.message || "Fetch failed");
        });
    } else {
      apiClient.files.fetchBlob(pluginId, file.key)
        .then((blob) => {
          if (cancelled) return;
          if (blob.size === 0) {
            setError("Empty response");
            return;
          }
          const url = URL.createObjectURL(blob);
          blobUrlRef.current = url;
          setSrc(url);
        })
        .catch((err) => {
          if (!cancelled) setError(err.message || "Fetch failed");
        });
    }

    return () => { cancelled = true; };
  }, [pluginId, file.key, contentType, filename]);

  // Cleanup blob URL on unmount
  useEffect(() => {
    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, []);

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
        {error ? (
          <div className="file-preview-placeholder">{error}</div>
        ) : src === null ? (
          <div className="file-preview-placeholder">Loading...</div>
        ) : (
          <Suspense fallback={<div className="file-preview-placeholder">Loading...</div>}>
            <Preview src={src} filename={filename} contentType={contentType} />
          </Suspense>
        )}
      </div>
    </div>
  );
}
