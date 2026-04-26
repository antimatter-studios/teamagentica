import { useShallow } from "zustand/react/shallow";
import { filenameFromKey } from "@teamagentica/api-client";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useFileStore } from "../stores/fileStore";

export default function FileOpsPanel() {
  const {
    copyItems,
    moveItems,
    removeCopyItem,
    removeMoveItem,
    pasteCopyItem,
    pasteMoveItem,
    clearOps,
    prefix,
  } = useFileStore(
    useShallow((s) => ({
      copyItems: s.copyItems,
      moveItems: s.moveItems,
      removeCopyItem: s.removeCopyItem,
      removeMoveItem: s.removeMoveItem,
      pasteCopyItem: s.pasteCopyItem,
      pasteMoveItem: s.pasteMoveItem,
      clearOps: s.clearOps,
      prefix: s.prefix,
    }))
  );

  const handleClose = () => {
    if (window.confirm("Clear all pending operations?")) {
      clearOps();
    }
  };

  const destLabel = prefix || "/";

  return (
    <Card className="flex h-full w-full flex-col overflow-hidden p-0">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          File Operations
        </span>
        <Button variant="ghost" size="icon" onClick={handleClose} title="Clear all" className="h-7 w-7">
          <X className="h-4 w-4" />
        </Button>
      </div>

      <ScrollArea className="flex-1">
        <div className="flex flex-col gap-3 p-3">
          <div className="text-xs text-muted-foreground">
            Destination: <span className="font-mono text-foreground">{destLabel}</span>
          </div>

          {copyItems.length > 0 && (
            <div className="flex flex-col gap-2">
              <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Copy ({copyItems.length})
              </div>
              {copyItems.map((key) => (
                <OpRow
                  key={key}
                  fileKey={key}
                  destLabel={destLabel}
                  action="Copy"
                  onPaste={() => pasteCopyItem(key)}
                  onRemove={() => removeCopyItem(key)}
                />
              ))}
            </div>
          )}

          {copyItems.length > 0 && moveItems.length > 0 && <Separator />}

          {moveItems.length > 0 && (
            <div className="flex flex-col gap-2">
              <div className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                Move ({moveItems.length})
              </div>
              {moveItems.map((key) => (
                <OpRow
                  key={key}
                  fileKey={key}
                  destLabel={destLabel}
                  action="Move"
                  onPaste={() => pasteMoveItem(key)}
                  onRemove={() => removeMoveItem(key)}
                />
              ))}
            </div>
          )}
        </div>
      </ScrollArea>
    </Card>
  );
}

function OpRow({
  fileKey,
  destLabel,
  action,
  onPaste,
  onRemove,
}: {
  fileKey: string;
  destLabel: string;
  action: string;
  onPaste: () => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex items-center gap-2 rounded-md border bg-muted/20 px-2 py-1.5">
      <span className="flex-1 truncate text-sm" title={fileKey}>
        {filenameFromKey(fileKey)}
      </span>
      <Button size="sm" variant="secondary" onClick={onPaste} title={`${action} to ${destLabel}`} className="h-7">
        Paste
      </Button>
      <Button size="icon" variant="ghost" onClick={onRemove} title="Remove" className="h-7 w-7">
        <X className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}
