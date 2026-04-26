import { cn } from "@/lib/utils";
import type { PreviewProps } from "./registry";

export default function ImagePreview({ src, filename, className, onClick }: PreviewProps) {
  return (
    <div
      className={cn("flex h-full w-full items-center justify-center overflow-hidden", className)}
      onClick={onClick}
    >
      <img
        className={cn("max-h-full max-w-full object-contain", onClick && "cursor-pointer")}
        src={src}
        alt={filename}
        onError={(e) => {
          (e.target as HTMLImageElement).style.display = "none";
        }}
      />
    </div>
  );
}
