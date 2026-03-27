import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { RegistryAlias, CreateAliasRequest, Plugin } from "@teamagentica/api-client";
import ConfirmDialog from "../ConfirmDialog";
import SaveButton from "../SaveButton";

interface Props {
  alias?: RegistryAlias;
  plugins: Plugin[];
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function ToolForm({ alias, plugins, onSave, onCancel }: Props) {
  const { createAlias, updateAlias, deleteAlias } = useAgentStore();
  const isEdit = !!alias;
  const [name, setName] = useState(alias?.name ?? "");
  const [plugin, setPlugin] = useState(alias?.plugin ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!name.trim() || !plugin.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateAlias(alias!.name, {
          name: name.trim() !== alias!.name ? name.trim() : undefined,
          type: "tool",
          plugin: plugin.trim(),
        });
      } else {
        const req: CreateAliasRequest = {
          name: name.trim(), type: "tool", plugin: plugin.trim(),
        };
        await createAlias(req);
      }
      onSave(isEdit ? undefined : name.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [name, plugin, isEdit, alias, onSave, createAlias, updateAlias]);

  const remove = useCallback(async () => {
    if (!alias) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteAlias(alias.name);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [alias, onSave, deleteAlias]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Tool: <span className="agents-name-val">@{name}</span></> : "Create Tool"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">Name</label>
        <input
          className="agents-input"
          value={name}
          onChange={(e) => setName(e.target.value.toLowerCase())}
          placeholder="alias name"
          autoFocus={!isEdit}
        />
        {name.trim() && <span className="agents-form-hint">use <strong>@{name.trim()}</strong> in your prompts</span>}
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Plugin</label>
        <select
          className="agents-select"
          value={plugin}
          onChange={(e) => setPlugin(e.target.value)}
        >
          <option value="">Select plugin...</option>
          {plugins.map((p) => (
            <option key={p.id} value={p.id}>{p.name} ({p.id})</option>
          ))}
        </select>
      </div>

      <div className="agents-form-actions">
        <SaveButton onClick={save} disabled={saving || !name.trim() || !plugin.trim()} className="agents-save-btn" />
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
          title="Delete Tool"
          onConfirm={() => { setConfirmDelete(false); remove(); }}
          onCancel={() => setConfirmDelete(false)}
          disabled={deleting}
          confirmLabel={deleting ? "..." : "Yes"}
        >
          Are you sure you want to delete <strong>@{name}</strong>?
        </ConfirmDialog>
      )}
    </div>
  );
}
