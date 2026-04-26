import { ScrollArea } from "@/components/ui/scroll-area";
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
    <ScrollArea className="h-full w-full">
      <pre className="p-4 text-xs font-mono whitespace-pre-wrap break-words">{display}</pre>
    </ScrollArea>
  );
}
