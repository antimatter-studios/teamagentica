import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { PreviewProps } from "./registry";

export default function MarkdownPreview({ src }: PreviewProps) {
  return (
    <div className="file-preview-markdown-wrap">
      <div className="file-preview-markdown">
        <Markdown remarkPlugins={[remarkGfm]}>{src}</Markdown>
      </div>
    </div>
  );
}
