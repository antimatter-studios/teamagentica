import type { ComponentType } from "react";

export interface PreviewProps {
  /** blob URL for binary content (images, PDFs, video) or text content for text-based previews */
  src: string;
  filename: string;
  contentType: string;
  /** optional CSS class override for the outermost wrapper */
  className?: string;
  /** optional click handler (e.g. open full-size) */
  onClick?: () => void;
}

export type PreviewComponent = ComponentType<PreviewProps>;

type MatchFn = (contentType: string, filename: string) => boolean;

interface PreviewEntry {
  match: MatchFn;
  load: () => Promise<{ default: PreviewComponent }>;
}

const registry: PreviewEntry[] = [];

/**
 * Register a preview handler. Entries are checked in order; first match wins.
 * Use `load` to return a lazy import so previews are code-split automatically.
 */
export function registerPreview(match: MatchFn, load: () => Promise<{ default: PreviewComponent }>) {
  registry.push({ match, load });
}

export function findPreview(contentType: string, filename: string): (() => Promise<{ default: PreviewComponent }>) | null {
  for (const entry of registry) {
    if (entry.match(contentType, filename)) return entry.load;
  }
  return null;
}

// ── Built-in preview registrations ──

const IMAGE_EXTENSIONS = /\.(jpe?g|png|gif|webp|svg|bmp|ico|avif|tiff?)$/i;

registerPreview(
  (ct, filename) => ct.startsWith("image/") || IMAGE_EXTENSIONS.test(filename),
  () => import("./ImagePreview"),
);

// Markdown
const MARKDOWN_EXTENSIONS = /\.(md|mdx|markdown)$/i;

registerPreview(
  (ct, filename) => ct === "text/markdown" || MARKDOWN_EXTENSIONS.test(filename),
  () => import("./MarkdownPreview"),
);

// PDF
registerPreview(
  (ct, filename) => ct === "application/pdf" || /\.pdf$/i.test(filename),
  () => import("./PdfPreview"),
);

// Video
const VIDEO_EXTENSIONS = /\.(mp4|webm|ogv|mov|avi|mkv)$/i;

registerPreview(
  (ct, filename) => ct.startsWith("video/") || VIDEO_EXTENSIONS.test(filename),
  () => import("./VideoPreview"),
);

// Text / code / JSON — broad catch-all for readable files
const TEXT_EXTENSIONS = /\.(txt|json|csv|xml|yaml|yml|toml|ini|cfg|conf|log|env|sh|bash|zsh|fish|bat|cmd|ps1|py|rb|js|jsx|ts|tsx|go|rs|c|cpp|h|hpp|java|kt|scala|swift|cs|php|lua|r|sql|graphql|gql|html|htm|css|scss|sass|less|vue|svelte|astro|makefile|dockerfile|gitignore|gitattributes|editorconfig|eslintrc|prettierrc|babelrc)$/i;

registerPreview(
  (ct, filename) =>
    ct.startsWith("text/") ||
    ct === "application/json" ||
    ct === "application/xml" ||
    ct === "application/x-yaml" ||
    TEXT_EXTENSIONS.test(filename),
  () => import("./TextPreview"),
);
