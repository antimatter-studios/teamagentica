interface Props {
  checked: boolean;
  onChange: (checked: boolean) => void;
  label: string;
  hint?: string;
  disabled?: boolean;
}

export default function ToggleButton({ checked, onChange, label, hint, disabled }: Props) {
  return (
    <label className="config-toggle-label">
      <input
        type="checkbox"
        className="config-checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
      />
      <span className="config-toggle-switch" />
      <span className="config-toggle-text">
        {label}
        {hint && <span className="agents-form-hint"> ({hint})</span>}
      </span>
    </label>
  );
}
