import { useMemo } from "react";

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
      className={`progress-bar ${className ?? ""}`}
      title={`${done}/${total} done (${pct}%)`}
    >
      <div className="progress-bar-fill" style={{ width: `${pct}%` }} />
      {(showPct || showRatio) && (
        <span className="progress-bar-label">
          {showRatio ? `${done}/${total}` : `${pct}%`}
        </span>
      )}
    </div>
  );
}
