import type { PreviewProps } from "./registry";

export default function TextPreview({ src, filename, contentType }: PreviewProps) {
  const isJson = filename.endsWith(".json") || contentType.includes("json");
  let display = src;
  if (isJson) {
    try {
      display = JSON.stringify(JSON.parse(src), null, 2);
    } catch {
      // not valid JSON, show raw
    }
  }

  return (
    <div className="file-preview-text-wrap">
      <pre className="file-preview-text">{display}</pre>
    </div>
  );
}
