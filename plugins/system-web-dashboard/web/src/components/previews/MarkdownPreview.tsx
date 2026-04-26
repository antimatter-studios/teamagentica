import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { ScrollArea } from "@/components/ui/scroll-area";
import type { PreviewProps } from "./registry";

export default function MarkdownPreview({ src }: PreviewProps) {
  return (
    <ScrollArea className="h-full w-full">
      <div className="prose prose-sm dark:prose-invert max-w-none p-4">
        <Markdown remarkPlugins={[remarkGfm]}>{src}</Markdown>
      </div>
    </ScrollArea>
  );
}
