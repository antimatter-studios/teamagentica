import Markdown from "react-markdown";
import type { PreviewProps } from "./registry";

export default function MarkdownPreview({ src }: PreviewProps) {
  return (
    <div className="file-preview-markdown-wrap">
      <div className="file-preview-markdown">
        <Markdown>{src}</Markdown>
      </div>
    </div>
  );
}
