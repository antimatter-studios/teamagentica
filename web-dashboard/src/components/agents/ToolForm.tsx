import { useState, useCallback } from "react";
import { apiClient } from "../../api/client";
import type { RegistryAlias, CreateAliasRequest, Plugin } from "@teamagentica/api-client";

interface Props {
  alias?: RegistryAlias;
  plugins: Plugin[];
  onSave: () => void;
  onCancel: () => void;
}

export default function ToolForm({ alias, plugins, onSave, onCancel }: Props) {
  const isEdit = !!alias;
  const [name, setName] = useState(alias?.name ?? "");
  const [plugin, setPlugin] = useState(alias?.plugin ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!name.trim() || !plugin.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await apiClient.agents.update(alias!.name, {
          type: "tool",
          plugin: plugin.trim(),
        });
      } else {
        const req: CreateAliasRequest = {
          name: name.trim(), type: "tool", plugin: plugin.trim(),
        };
        await apiClient.agents.create(req);
      }
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [name, plugin, isEdit, alias, onSave]);

  const remove = useCallback(async () => {
    if (!alias) return;
    setDeleting(true);
    setError(null);
    try {
      await apiClient.agents.delete(alias.name);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [alias, onSave]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Tool: <span className="agents-name-val">@{alias!.name}</span></> : "Create Tool"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">Name</label>
        {isEdit ? (
          <span className="agents-name-val">@{alias!.name}</span>
        ) : (
          <input
            className="agents-input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="alias name"
            autoFocus
          />
        )}
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
        <button className="agents-save-btn" onClick={save} disabled={saving || !name.trim() || !plugin.trim()}>
          {saving ? "..." : "Save"}
        </button>
        <button className="agents-cancel-btn" onClick={onCancel}>Cancel</button>
        {isEdit && (
          <button
            className="agents-delete-btn"
            onClick={remove}
            disabled={deleting}
            style={{ marginLeft: "auto" }}
          >
            {deleting ? "..." : "Delete"}
          </button>
        )}
      </div>
    </div>
  );
}
