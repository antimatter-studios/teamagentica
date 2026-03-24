import type { PreviewProps } from "./registry";

export default function ImagePreview({ src, filename, className, onClick }: PreviewProps) {
  return (
    <div className={className || "file-preview-image-wrap"} onClick={onClick}>
      <img
        className="file-preview-image"
        src={src}
        alt={filename}
        style={onClick ? { cursor: "pointer" } : undefined}
        onError={(e) => {
          (e.target as HTMLImageElement).style.display = "none";
        }}
      />
    </div>
  );
}
