import { useEffect, useRef, useState } from "react";
import { fetchObjectBlob } from "../../api/files";
import type { PreviewProps } from "./registry";

export default function ImagePreview({ file, pluginId }: PreviewProps) {
  const [src, setSrc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  useEffect(() => {
    // Reset state for new file
    setSrc(null);
    setError(null);

    // Revoke previous blob URL
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

    fetchObjectBlob(pluginId, file.key)
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

    return () => {
      cancelled = true;
    };
  }, [pluginId, file.key]);

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

  if (!src) {
    return <div className="file-preview-placeholder">Loading preview...</div>;
  }

  return (
    <div className="file-preview-image-wrap">
      <img
        className="file-preview-image"
        src={src}
        alt={file.key}
        onError={() => setError("Image decode failed")}
      />
    </div>
  );
}
