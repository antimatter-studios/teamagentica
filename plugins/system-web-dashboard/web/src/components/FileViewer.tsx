import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { StorageFile } from "@teamagentica/api-client";
import { filenameFromKey, formatBytes } from "@teamagentica/api-client";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { apiClient } from "../api/client";
import { findPreview } from "./previews/registry";

const DefaultPreview = lazy(() => import("./previews/DefaultPreview"));

/** Text-based content types that should be fetched as text, not blob */
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

export default function FileViewer({ file, pluginId, onClose }: Props) {
  const filename = filenameFromKey(file.key);
  const contentType = file.content_type || "";

  const [src, setSrc] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const blobUrlRef = useRef<string | null>(null);

  const PreviewComponent = useMemo(() => {
    const loader = findPreview(contentType, filename);
    return loader ? lazy(loader) : null;
  }, [contentType, filename]);

  const Preview = PreviewComponent || DefaultPreview;

  // Fetch content
  useEffect(() => {
    setSrc(null);
    setError(null);
    if (blobUrlRef.current) {
      URL.revokeObjectURL(blobUrlRef.current);
      blobUrlRef.current = null;
    }

    let cancelled = false;

    if (isTextContent(contentType, filename)) {
      apiClient.files.fetchText(pluginId, file.key)
        .then((text) => {
          if (!cancelled) setSrc(text);
        })
        .catch((err) => {
          if (!cancelled) setError(err.message || "Fetch failed");
        });
    } else {
      apiClient.files.fetchBlob(pluginId, file.key)
        .then((blob) => {
          if (cancelled) return;
          if (blob.size === 0) {
            setError("Empty response");
            return;
          }
          const url = URL.createObjectURL(blob);
          blobUrlRef.current = url;
          setSrc(url);
        })
        .catch((err) => {
          if (!cancelled) setError(err.message || "Fetch failed");
        });
    }

    return () => { cancelled = true; };
  }, [pluginId, file.key, contentType, filename]);

  // Cleanup blob URL on unmount
  useEffect(() => {
    return () => {
      if (blobUrlRef.current) {
        URL.revokeObjectURL(blobUrlRef.current);
        blobUrlRef.current = null;
      }
    };
  }, []);

  return (
    <Card className="flex h-full w-full flex-col overflow-hidden p-0">
      <div className="flex items-center gap-3 border-b px-3 py-2">
        <Button variant="ghost" size="sm" onClick={onClose} title="Back to file list" className="h-8">
          <ArrowLeft className="mr-1 h-4 w-4" />
          Back
        </Button>
        <span className="flex-1 truncate text-sm font-medium">{filename}</span>
        <Badge variant="secondary">{formatBytes(file.size)}</Badge>
      </div>
      <div className="flex flex-1 items-center justify-center overflow-hidden bg-muted/20">
        {error ? (
          <div className="text-sm text-destructive">{error}</div>
        ) : src === null ? (
          <div className="text-sm text-muted-foreground">Loading...</div>
        ) : (
          <Suspense fallback={<div className="text-sm text-muted-foreground">Loading...</div>}>
            <Preview src={src} filename={filename} contentType={contentType} />
          </Suspense>
        )}
      </div>
    </Card>
  );
}
