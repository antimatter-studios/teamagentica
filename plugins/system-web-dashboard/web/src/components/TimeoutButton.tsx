import { useState, useEffect, useCallback, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface Props {
  label: string;
  icon: ReactNode;
  seconds: number;
  onClick: () => void;
  disabled?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

export default function TimeoutButton({
  label,
  icon,
  seconds,
  onClick,
  disabled = false,
  className,
  style,
}: Props) {
  const [remaining, setRemaining] = useState(0);
  const active = remaining > 0;

  const handleClick = useCallback(() => {
    if (active || disabled) return;
    onClick();
    setRemaining(seconds);
  }, [active, disabled, onClick, seconds]);

  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => {
      setRemaining((r) => (r <= 1 ? 0 : r - 1));
    }, 1000);
    return () => clearInterval(id);
  }, [active]);

  return (
    <Button
      type="button"
      variant="default"
      className={cn(active && "opacity-60 cursor-not-allowed", className)}
      style={style}
      onClick={handleClick}
      disabled={active || disabled}
    >
      {active && <span className="mr-1.5">{icon}</span>}
      {label}
      {active && <span className="ml-1.5">({remaining}s)</span>}
    </Button>
  );
}
