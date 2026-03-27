import { useState, useEffect, useCallback, type ReactNode } from "react";

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
    <button
      className={className}
      style={{ ...style, opacity: active ? 0.6 : 1, cursor: active ? "not-allowed" : "pointer" }}
      onClick={handleClick}
      disabled={active || disabled}
    >
      {active && <span style={{ marginRight: 6 }}>{icon}</span>}
      {label}
      {active && <span style={{ marginLeft: 6 }}>({remaining}s)</span>}
    </button>
  );
}
