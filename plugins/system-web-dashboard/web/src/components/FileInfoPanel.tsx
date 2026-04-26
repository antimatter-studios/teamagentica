import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { formatBytes, filenameFromKey } from "@teamagentica/api-client";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { ScrollArea } from "@/components/ui/scroll-area";
import { apiClient } from "../api/client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

const TEXT_TYPES = /^text\/|application\/json|application\/xml|application\/x-yaml/;
const TEXT_EXTENSIONS = /\.(txt|json|csv|xml|yaml|yml|toml|ini|cfg|conf|log|env|sh|bash|zsh|fish|bat|cmd|ps1|py|rb|js|jsx|ts|tsx|go|rs|c|cpp|h|hpp|java|kt|scala|swift|cs|php|lua|r|sql|graphql|gql|html|htm|css|scss|sass|less|vue|svelte|astro|makefile|dockerfile|gitignore|gitattributes|editorconfig|eslintrc|prettierrc|babelrc|md|mdx|markdown)$/i;

function isTextContent(contentType: string, filename: string): boolean {
  return TEXT_TYPES.test(contentType) || TEXT_EXTENSIONS.test(filename);
}

interface Props {
  file: StorageFile;
  pluginId: string;
  onClose: () => void;
}

export default function FileInfoPanel({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);
  const contentType = file.content_type || "";
  const ext = filename.includes(".") ? filename.split(".").pop()!.toUpperCase() : "-";

  const [src, setSrc] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(contentType, filename);
    return loader ? lazy(loader) : null;
  }, [contentType, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  useEffect(() => {
    setSrc(null);
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

    if (isTextContent(contentType, filename)) {
      apiClient.files.fetchText(pluginId, file.key)
        .then((text) => { if (!cancelled) setSrc(text); })
        .catch(() => {});
    } else {
      apiClient.files.fetchBlob(pluginId, file.key)
        .then((blob) => {
          if (cancelled) return;
          const url = URL.createObjectURL(blob);
          blobUrlRef.current = url;
          setSrc(url);
        })
        .catch(() => {});
    }

    return () => {
      cancelled = true;
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, [pluginId, file.key, contentType, filename]);

  return (
    <Card className="flex h-full w-full flex-col overflow-hidden p-0">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Info</span>
        <Button variant="ghost" size="icon" onClick={onClose} title="Close" className="h-7 w-7">
          <X className="h-4 w-4" />
        </Button>
      </div>

      <ScrollArea className="flex-1">
        <div className="flex flex-col gap-3 p-3">
          <div className="flex h-64 items-center justify-center overflow-hidden rounded-md border bg-muted/30">
            <Suspense fallback={<Placeholder>Loading...</Placeholder>}>
              {src !== null ? (
                <Preview src={src} filename={filename} contentType={contentType} />
              ) : (
                <Placeholder>Loading...</Placeholder>
              )}
            </Suspense>
          </div>

          <Separator />

          <div className="flex flex-col gap-2 text-sm">
            <InfoRow label="Name" value={filename} />
            <InfoRow label="Path" value={file.key} />
            <InfoRow label="Size" value={formatBytes(file.size)} />
            <InfoRow label="Type" value={file.content_type || "-"} />
            <InfoRow label="Extension" value={ext} />
            <InfoRow
              label="Modified"
              value={
                file.last_modified
                  ? new Date(file.last_modified).toLocaleString()
                  : "-"
              }
            />
            {file.etag && <InfoRow label="ETag" value={file.etag} />}
          </div>
        </div>
      </ScrollArea>
    </Card>
  );
}

function Placeholder({ children }: { children: React.ReactNode }) {
  return <div className="text-sm text-muted-foreground">{children}</div>;
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="grid grid-cols-[6rem_1fr] gap-2">
      <span className="text-xs uppercase tracking-wide text-muted-foreground">{label}</span>
      <span className="break-all text-sm">{value}</span>
    </div>
  );
}
