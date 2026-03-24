import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { Attachment } from "@teamagentica/api-client";
import { apiClient } from "../api/client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

const TEXT_TYPES = /^text\/|application\/json|application\/xml|application\/x-yaml/;
const TEXT_EXTENSIONS = /\.(txt|json|csv|xml|yaml|yml|toml|ini|cfg|conf|log|env|sh|bash|md|mdx|py|js|ts|go|rs|html|css)$/i;

function isTextContent(mimeType: string, filename: string): boolean {
  return TEXT_TYPES.test(mimeType) || TEXT_EXTENSIONS.test(filename);
}

interface Props {
  attachment: Attachment;
  /** When true, render a small left-aligned thumbnail (inline in chat). When false/omitted, render full-size. */
  compact?: boolean;
}

export default function ChatAttachmentPreview({ attachment, compact }: Props) {
  const filename = attachment.filename || "file";
  const mimeType = attachment.mime_type || "";
  const fileKey = attachment.storage_key || attachment.file_id || "";

  const [src, setSrc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(mimeType, filename);
    return loader ? lazy(loader) : null;
  }, [mimeType, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  useEffect(() => {
    setSrc(null);
    setError(null);
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    // URL-type attachments already have a direct URL
    if (attachment.type === "url" && attachment.url) {
      setSrc(attachment.url);
      return;
    }

    if (!fileKey) {
      setError("No file reference");
      return;
    }

    let cancelled = false;

    if (isTextContent(mimeType, filename)) {
      apiClient.chat.fetchFileBlob(fileKey)
        .then(async (blob) => {
          if (cancelled) return;
          const text = await blob.text();
          setSrc(text);
        })
        .catch((err) => {
          if (!cancelled) setError(err.message || "Fetch failed");
        });
    } else {
      apiClient.chat.fetchFileBlob(fileKey)
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
  }, [attachment.type, attachment.url, fileKey, mimeType, filename]);

  // Cleanup blob URL on unmount
  useEffect(() => {
    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, []);

  if (error) {
    return <div className="file-preview-placeholder">{error}</div>;
  }

  if (src === null) {
    return <div className="file-preview-placeholder">Loading...</div>;
  }

  return (
    <Suspense fallback={<div className="file-preview-placeholder">Loading...</div>}>
      {compact ? (
        <div className="chat-attachment-compact">
          <Preview src={src} filename={filename} contentType={mimeType} className="chat-attachment-compact-inner" />
        </div>
      ) : (
        <Preview src={src} filename={filename} contentType={mimeType} />
      )}
    </Suspense>
  );
}
