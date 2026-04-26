import { FileIcon } from "lucide-react";
import type { PreviewProps } from "./registry";

export default function DefaultPreview({ filename }: PreviewProps) {
  const ext = filename.split(".").pop()?.toUpperCase() || "FILE";
  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-muted-foreground">
      <FileIcon className="h-10 w-10" />
      <span className="text-2xl font-semibold tracking-wide">{ext}</span>
      <span className="text-sm">No preview available</span>
    </div>
  );
}
