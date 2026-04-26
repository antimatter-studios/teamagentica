import { Check } from "lucide-react";
import TimeoutButton from "./TimeoutButton";

interface Props {
  label?: string;
  onClick: () => void;
  disabled?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

export default function SaveButton({
  label = "Save",
  onClick,
  disabled,
  className,
  style,
}: Props) {
  return (
    <TimeoutButton
      label={label}
      icon={<Check className="h-4 w-4" />}
      seconds={5}
      onClick={onClick}
      disabled={disabled}
      className={className}
      style={style}
    />
  );
}
