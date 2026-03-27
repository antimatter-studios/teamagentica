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
      icon={<span>✅</span>}
      seconds={5}
      onClick={onClick}
      disabled={disabled}
      className={className}
      style={style}
    />
  );
}
