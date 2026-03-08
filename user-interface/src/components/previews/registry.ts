import type { ComponentType } from "react";
import type { StorageFile } from "../../api/files";

export interface PreviewProps {
  file: StorageFile;
  pluginId: string;
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
