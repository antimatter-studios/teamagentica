import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { PersonaRole } from "@teamagentica/api-client";
import ConfirmDialog from "../ConfirmDialog";
import SaveButton from "../SaveButton";

interface Props {
  role?: PersonaRole;
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function RoleForm({ role, onSave, onCancel }: Props) {
  const { createRole, updateRole, deleteRole } = useAgentStore();
  const isEdit = !!role;
  const [id, setId] = useState(role?.id ?? "");
  const [label, setLabel] = useState(role?.label ?? "");
  const [systemPrompt, setSystemPrompt] = useState(role?.system_prompt ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!id.trim() || !label.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateRole(role!.id, {
          label: label || undefined,
          system_prompt: systemPrompt || undefined,
        });
      } else {
        await createRole({
          id: id.trim(),
          label: label.trim(),
          system_prompt: systemPrompt || undefined,
        });
      }
      onSave(isEdit ? undefined : id.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [id, label, systemPrompt, isEdit, role, onSave, createRole, updateRole]);

  const remove = useCallback(async () => {
    if (!role) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteRole(role.id);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [role, onSave, deleteRole]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Role: <span className="agents-name-val">{id}</span></> : "Create Role"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">ID</label>
        <input
          className="agents-input"
          value={id}
          onChange={(e) => setId(e.target.value)}
          placeholder="role-id"
          autoFocus={!isEdit}
        />
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Label</label>
        <input
          className="agents-input"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="Display name"
        />
      </div>

      <div className="agents-form-field agents-form-field--grow">
        <label className="agents-form-label">System Prompt</label>
        <textarea
          className="agents-input"
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="Default system prompt for personas with this role..."
          style={{ resize: "none", fontFamily: "inherit" }}
        />
      </div>

      <div className="agents-form-actions">
        <SaveButton onClick={save} disabled={saving || !id.trim() || !label.trim()} className="agents-save-btn" />
        <button className="agents-cancel-btn" onClick={onCancel}>Cancel</button>
        {isEdit && (
          <button
            className="agents-delete-btn"
            onClick={() => setConfirmDelete(true)}
            disabled={deleting}
            style={{ marginLeft: "auto" }}
          >
            {deleting ? "..." : "Delete"}
          </button>
        )}
      </div>

      {confirmDelete && (
        <ConfirmDialog
          title="Delete Role"
          onConfirm={() => { setConfirmDelete(false); remove(); }}
          onCancel={() => setConfirmDelete(false)}
          disabled={deleting}
          confirmLabel={deleting ? "..." : "Yes"}
        >
          Are you sure you want to delete role <strong>{id}</strong>?
        </ConfirmDialog>
      )}
    </div>
  );
}
