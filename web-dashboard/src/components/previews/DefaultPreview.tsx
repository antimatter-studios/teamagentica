import type { PreviewProps } from "./registry";

export default function DefaultPreview({ filename }: PreviewProps) {
  const ext = filename.split(".").pop()?.toUpperCase() || "FILE";
  return (
    <div className="file-preview-placeholder">
      <span className="file-preview-placeholder-ext">{ext}</span>
      <span className="file-preview-placeholder-label">No preview available</span>
    </div>
  );
}
