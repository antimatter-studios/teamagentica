import { useState, useCallback } from "react";
import { apiClient } from "../../api/client";
import type { Persona, CreatePersonaRequest, RegistryAlias } from "@teamagentica/api-client";

interface Props {
  persona?: Persona;
  chatAliases: RegistryAlias[];
  onSave: () => void;
  onCancel: () => void;
}

export default function PersonaForm({ persona, chatAliases, onSave, onCancel }: Props) {
  const isEdit = !!persona;
  const [alias, setAlias] = useState(persona?.alias ?? "");
  const [backendAlias, setBackendAlias] = useState(persona?.backend_alias ?? "");
  const [systemPrompt, setSystemPrompt] = useState(persona?.system_prompt ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!alias.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await apiClient.personas.update(persona!.alias, {
          system_prompt: systemPrompt || undefined,
          backend_alias: backendAlias || undefined,
        });
      } else {
        const req: CreatePersonaRequest = {
          alias: alias.trim(),
          system_prompt: systemPrompt,
          backend_alias: backendAlias || undefined,
        };
        await apiClient.personas.create(req);
      }
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [alias, backendAlias, systemPrompt, isEdit, persona, onSave]);

  const remove = useCallback(async () => {
    if (!persona) return;
    setDeleting(true);
    setError(null);
    try {
      await apiClient.personas.delete(persona.alias);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [persona, onSave]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Persona: <span className="agents-name-val">@{persona!.alias}</span></> : "Create Persona"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">Name</label>
        {isEdit ? (
          <span className="agents-name-val">@{persona!.alias}</span>
        ) : (
          <input
            className="agents-input"
            value={alias}
            onChange={(e) => setAlias(e.target.value)}
            placeholder="persona name"
            autoFocus
          />
        )}
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Backend Alias</label>
        <select
          className="agents-select"
          value={backendAlias}
          onChange={(e) => setBackendAlias(e.target.value)}
        >
          <option value="">Select alias...</option>
          {chatAliases.map((a) => (
            <option key={a.name} value={a.name}>@{a.name}</option>
          ))}
        </select>
      </div>

      <div className="agents-form-field agents-form-field--grow">
        <label className="agents-form-label">System Prompt</label>
        <textarea
          className="agents-input"
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="System prompt..."
          style={{ resize: "none", fontFamily: "inherit" }}
        />
      </div>

      <div className="agents-form-actions">
        <button className="agents-save-btn" onClick={save} disabled={saving || !alias.trim()}>
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
