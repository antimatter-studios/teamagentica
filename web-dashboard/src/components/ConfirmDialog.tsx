import type { ReactNode } from "react";

interface Props {
  title: string;
  children: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: "danger" | "primary";
  disabled?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export default function ConfirmDialog({
  title,
  children,
  confirmLabel = "Yes",
  cancelLabel = "No",
  variant = "danger",
  disabled = false,
  onConfirm,
  onCancel,
}: Props) {
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 400 }}>
        <div className="modal-header">
          <div className="modal-title">{title}</div>
        </div>
        <div style={{ color: "var(--text-secondary)", margin: "12px 0 0" }}>
          {children}
        </div>
        <div className="modal-actions">
          <button className="modal-btn modal-btn--ghost" onClick={onCancel} disabled={disabled}>
            {cancelLabel}
          </button>
          <button
            className={`modal-btn modal-btn--${variant}`}
            onClick={onConfirm}
            disabled={disabled}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
