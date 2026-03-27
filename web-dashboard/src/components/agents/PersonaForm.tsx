import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { Persona, CreatePersonaRequest } from "@teamagentica/api-client";
import { apiClient } from "../../api/client";
import ConfirmDialog from "../ConfirmDialog";
import SaveButton from "../SaveButton";
import ToggleButton from "../ToggleButton";

interface Props {
  persona?: Persona;
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function PersonaForm({ persona, onSave, onCancel }: Props) {
  const { roles, chatAliases, createPersona, updatePersona, deletePersona } = useAgentStore();
  const isEdit = !!persona;
  const [alias, setAlias] = useState(persona?.alias ?? "");
  const [backendAlias, setBackendAlias] = useState(persona?.backend_alias ?? "");
  const [role, setRole] = useState(persona?.role ?? "");
  const [systemPrompt, setSystemPrompt] = useState(persona?.system_prompt ?? "");
  const [isDefault, setIsDefault] = useState(!!persona?.is_default);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [resettingPrompt, setResettingPrompt] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!alias.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updatePersona(persona!.alias, {
          alias: alias.trim() !== persona!.alias ? alias.trim() : undefined,
          system_prompt: systemPrompt || undefined,
          backend_alias: backendAlias || undefined,
          role: role || undefined,
          is_default: isDefault || undefined,
        });
      } else {
        const req: CreatePersonaRequest = {
          alias: alias.trim(),
          system_prompt: systemPrompt,
          backend_alias: backendAlias || undefined,
          role: role || undefined,
          is_default: isDefault || undefined,
        };
        await createPersona(req);
      }
      onSave(isEdit ? undefined : alias.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [alias, backendAlias, role, systemPrompt, isDefault, isEdit, persona, onSave, createPersona, updatePersona]);

  const remove = useCallback(async () => {
    if (!persona) return;
    setDeleting(true);
    setError(null);
    try {
      await deletePersona(persona.alias);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [persona, onSave, deletePersona]);

  const resetToRoleDefault = useCallback(async () => {
    const matched = roles.find((r) => r.id === role);
    if (!matched) return;
    setResettingPrompt(true);
    setError(null);
    try {
      const freshRole = await apiClient.personas.getRole(role);
      setSystemPrompt(freshRole.system_prompt);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Reset failed");
    } finally {
      setResettingPrompt(false);
    }
  }, [role, roles]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  const agents = chatAliases();

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Persona: <span className="agents-name-val">@{alias}</span></> : "Create Persona"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">Name</label>
        <input
          className="agents-input"
          value={alias}
          onChange={(e) => setAlias(e.target.value.toLowerCase())}
          placeholder="persona name"
          autoFocus={!isEdit}
        />
        {alias.trim() && <span className="agents-form-hint">use <strong>@{alias.trim()}</strong> in your prompts</span>}
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Role</label>
        <select
          className="agents-select"
          value={role}
          onChange={(e) => {
            const newRole = e.target.value;
            setRole(newRole);
            if (!isEdit) {
              const matched = roles.find((r) => r.id === newRole);
              if (matched) setSystemPrompt(matched.system_prompt);
            }
          }}
        >
          <option value="">No role</option>
          {roles.map((r) => (
            <option key={r.id} value={r.id}>{r.label}</option>
          ))}
        </select>
      </div>

      <div className="agents-form-field">
        <ToggleButton
          checked={isDefault}
          onChange={setIsDefault}
          label="Default Persona"
          hint="handles unaddressed messages"
        />
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Backend Alias</label>
        <select
          className="agents-select"
          value={backendAlias}
          onChange={(e) => setBackendAlias(e.target.value)}
        >
          <option value="">Select alias...</option>
          {agents.map((a) => (
            <option key={a.name} value={a.name}>@{a.name}</option>
          ))}
        </select>
      </div>

      <div className="agents-form-field agents-form-field--grow">
        <label className="agents-form-label">
          System Prompt
          {role && (
            <button
              className="agents-reset-btn"
              onClick={resetToRoleDefault}
              disabled={resettingPrompt}
              title="Reset system prompt to the current role default"
            >
              {resettingPrompt ? "..." : "Reset to Role Default"}
            </button>
          )}
        </label>
        <textarea
          className="agents-input"
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="System prompt..."
          style={{ resize: "none", fontFamily: "inherit" }}
        />
      </div>

      <div className="agents-form-actions">
        <SaveButton onClick={save} disabled={saving || !alias.trim()} className="agents-save-btn" />
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
          title="Delete Persona"
          onConfirm={() => { setConfirmDelete(false); remove(); }}
          onCancel={() => setConfirmDelete(false)}
          disabled={deleting}
          confirmLabel={deleting ? "..." : "Yes"}
        >
          Are you sure you want to delete <strong>@{alias}</strong>?
        </ConfirmDialog>
      )}
    </div>
  );
}
