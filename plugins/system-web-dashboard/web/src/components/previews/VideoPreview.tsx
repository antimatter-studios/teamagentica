import { cn } from "@/lib/utils";
import type { PreviewProps } from "./registry";

export default function VideoPreview({ src, className }: PreviewProps) {
  return (
    <div className={cn("flex h-full w-full items-center justify-center", className)}>
      <video
        src={src}
        controls
        className="h-full w-full rounded-md object-contain"
      >
        Your browser does not support video playback.
      </video>
    </div>
  );
}
