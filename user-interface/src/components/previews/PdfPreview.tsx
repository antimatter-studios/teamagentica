import { useEffect, useRef, useState } from "react";
import { apiClient } from "../../api/client";
import type { PreviewProps } from "./registry";

export default function PdfPreview({ file, pluginId }: PreviewProps) {
  const [src, setSrc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  useEffect(() => {
    setSrc(null);
    setError(null);

    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

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

    return () => { cancelled = true; };
  }, [pluginId, file.key]);

  useEffect(() => {
    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, []);

  if (error) return <div className="file-preview-placeholder">{error}</div>;
  if (!src) return <div className="file-preview-placeholder">Loading PDF...</div>;

  return (
    <div className="file-preview-pdf-wrap">
      <object
        data={src}
        type="application/pdf"
        className="file-preview-pdf"
      >
        <div className="file-preview-placeholder">
          Browser cannot display PDF — download to view
        </div>
      </object>
    </div>
  );
}
