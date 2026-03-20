import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type { PreviewProps } from "./registry";

const MAX_PREVIEW_BYTES = 512 * 1024; // 512 KB text preview limit

export default function TextPreview({ file, pluginId }: PreviewProps) {
  const [text, setText] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    setText(null);
    setError(null);
    setTruncated(false);

    let cancelled = false;

    apiClient.files.fetchText(pluginId, file.key)
      .then((content) => {
        if (cancelled) return;
        if (content.length > MAX_PREVIEW_BYTES) {
          setText(content.slice(0, MAX_PREVIEW_BYTES));
          setTruncated(true);
        } else {
          setText(content);
        }
      })
      .catch((err) => {
        if (!cancelled) setError(err.message || "Fetch failed");
      });

    return () => { cancelled = true; };
  }, [pluginId, file.key]);

  if (error) return <div className="file-preview-placeholder">{error}</div>;
  if (text === null) return <div className="file-preview-placeholder">Loading...</div>;

  const isJson = file.key.endsWith(".json") || (file.content_type || "").includes("json");
  let display = text;
  if (isJson) {
    try {
      display = JSON.stringify(JSON.parse(text), null, 2);
    } catch {
      // not valid JSON, show raw
    }
  }

  return (
    <div className="file-preview-text-wrap">
      <pre className="file-preview-text">{display}</pre>
      {truncated && (
        <div className="file-preview-text-truncated">
          File truncated — download to see full content
        </div>
      )}
    </div>
  );
}
