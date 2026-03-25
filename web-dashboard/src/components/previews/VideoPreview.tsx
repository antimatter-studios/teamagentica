import type { PreviewProps } from "./registry";

export default function VideoPreview({ src, className }: PreviewProps) {
  return (
    <div className={className || "file-preview-video-wrap"} style={{ width: "100%", height: "100%", display: "flex", alignItems: "center", justifyContent: "center" }}>
      <video
        className="file-preview-video"
        src={src}
        controls
        style={{ width: "100%", height: "100%", objectFit: "contain", borderRadius: 8 }}
      >
        Your browser does not support video playback.
      </video>
    </div>
  );
}
