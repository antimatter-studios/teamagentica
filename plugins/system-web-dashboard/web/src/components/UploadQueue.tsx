import { useShallow } from "zustand/react/shallow";
import { ChevronDown, ChevronUp, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Progress } from "@/components/ui/progress";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";
import { useUploadStore, formatBytes } from "../stores/uploadStore";

export default function UploadQueue() {
  const { items, panelOpen, cancel, dismiss, clearCompleted, togglePanel } =
    useUploadStore(
      useShallow((s) => ({
        items: s.items,
        panelOpen: s.panelOpen,
        cancel: s.cancel,
        dismiss: s.dismiss,
        clearCompleted: s.clearCompleted,
        togglePanel: s.togglePanel,
      }))
    );

  if (items.length === 0) return null;

  const active = items.filter((i) => i.status === "uploading" || i.status === "queued").length;
  const done = items.filter((i) => i.status === "done").length;
  const hasClearable = items.some((i) => i.status === "done" || i.status === "error" || i.status === "cancelled");

  return (
    <Card className="fixed bottom-4 right-4 z-50 w-96 overflow-hidden p-0 shadow-lg">
      <button
        type="button"
        onClick={togglePanel}
        className="flex w-full items-center justify-between border-b px-3 py-2 text-left hover:bg-accent"
      >
        <span className="text-xs font-semibold uppercase tracking-wide">
          Uploads{active > 0 && ` (${active} active)`}{done > 0 && ` · ${done} done`}
        </span>
        <span className="flex items-center gap-2">
          {hasClearable && (
            <Button
              size="sm"
              variant="ghost"
              className="h-6 px-2 text-xs"
              onClick={(e) => { e.stopPropagation(); clearCompleted(); }}
            >
              Clear
            </Button>
          )}
          {panelOpen ? <ChevronDown className="h-4 w-4" /> : <ChevronUp className="h-4 w-4" />}
        </span>
      </button>
      {panelOpen && (
        <ScrollArea className="max-h-80">
          <div className="flex flex-col divide-y">
            {items.map((item) => (
              <UploadRow key={item.id} item={item} onCancel={cancel} onDismiss={dismiss} />
            ))}
          </div>
        </ScrollArea>
      )}
    </Card>
  );
}

function UploadRow({
  item,
  onCancel,
  onDismiss,
}: {
  item: ReturnType<typeof useUploadStore.getState>["items"][0];
  onCancel: (id: string) => void;
  onDismiss: (id: string) => void;
}) {
  const pct = item.total > 0 ? Math.round((item.loaded / item.total) * 100) : 0;
  const eta =
    item.status === "uploading" && item.speed > 0
      ? Math.ceil((item.total - item.loaded) / item.speed)
      : null;

  return (
    <div className="flex flex-col gap-1.5 px-3 py-2">
      <div className="flex items-center gap-2">
        <span className="flex-1 truncate text-sm" title={item.fileName}>
          {item.fileName.length > 28 ? item.fileName.slice(0, 25) + "..." : item.fileName}
        </span>
        <span className="text-xs text-muted-foreground">{formatBytes(item.fileSize)}</span>
        {(item.status === "uploading" || item.status === "queued") && (
          <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => onCancel(item.id)} title="Cancel">
            <X className="h-3.5 w-3.5" />
          </Button>
        )}
        {(item.status === "done" || item.status === "error" || item.status === "cancelled") && (
          <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => onDismiss(item.id)} title="Dismiss">
            <X className="h-3.5 w-3.5" />
          </Button>
        )}
      </div>

      {(item.status === "uploading" || item.status === "done") && (
        <Progress value={pct} className={cn("h-1.5", item.status === "done" && "[&>div]:bg-emerald-500")} />
      )}

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>
          {item.status === "uploading" && (
            <>
              {formatBytes(item.speed)}/s
              {eta !== null && ` · ${eta}s`}
            </>
          )}
        </span>
        {item.status === "queued" && <Badge variant="secondary">Queued</Badge>}
        {item.status === "done" && <Badge className="bg-emerald-500 text-white">Done</Badge>}
        {item.status === "error" && <Badge variant="destructive">{item.error || "Error"}</Badge>}
        {item.status === "cancelled" && <Badge variant="outline">Cancelled</Badge>}
      </div>
    </div>
  );
}
