import { useMemo } from "react";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";

interface ProgressBarProps {
  done: number;
  total: number;
  /** Show "X%" label centered over the bar (default true) */
  showPct?: boolean;
  /** Show "done/total" label centered instead of percentage */
  showRatio?: boolean;
  /** CSS class for the outer container */
  className?: string;
}

export function ProgressBar({ done, total, showPct = true, showRatio, className }: ProgressBarProps) {
  const pct = useMemo(() => (total > 0 ? Math.round((done / total) * 100) : 0), [done, total]);

  return (
    <div
      className={cn("relative w-full", className)}
      title={`${done}/${total} done (${pct}%)`}
    >
      <Progress value={pct} className="h-4" />
      {(showPct || showRatio) && (
        <span className="absolute inset-0 flex items-center justify-center text-xs font-medium text-foreground">
          {showRatio ? `${done}/${total}` : `${pct}%`}
        </span>
      )}
    </div>
  );
}
