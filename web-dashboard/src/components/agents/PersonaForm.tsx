import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { AgentEntry, CreateAgentEntryRequest } from "@teamagentica/api-client";
import ConfirmDialog from "../ConfirmDialog";
import SaveButton from "../SaveButton";
import ToggleButton from "../ToggleButton";

interface Props {
  agent?: AgentEntry;
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function AgentEntryForm({ agent, onSave, onCancel }: Props) {
  const { chatAliases, createAgent, updateAgent, deleteAgent } = useAgentStore();
  const isEdit = !!agent;
  const [alias, setAlias] = useState(agent?.alias ?? "");
  const [plugin, setPlugin] = useState(agent?.plugin ?? "");
  const [model, setModel] = useState(agent?.model ?? "");
  const [systemPrompt, setSystemPrompt] = useState(agent?.system_prompt ?? "");
  const [isDefault, setIsDefault] = useState(!!agent?.is_default);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!alias.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateAgent(agent!.alias, {
          alias: alias.trim() !== agent!.alias ? alias.trim() : undefined,
          system_prompt: systemPrompt || undefined,
          plugin: plugin || undefined,
          is_default: isDefault || undefined,
        });
      } else {
        const req: CreateAgentEntryRequest = {
          alias: alias.trim(),
          system_prompt: systemPrompt,
          plugin: plugin || undefined,
          is_default: isDefault || undefined,
        };
        await createAgent(req);
      }
      onSave(isEdit ? undefined : alias.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [alias, plugin, systemPrompt, isDefault, isEdit, agent, onSave, createAgent, updateAgent]);

  const remove = useCallback(async () => {
    if (!agent) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteAgent(agent.alias);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [agent, onSave, deleteAgent]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  const agents = chatAliases();
  const pluginOptions = Array.from(new Set(agents.map((a) => a.plugin))).sort();

  return (
    <div className="agents-form" onKeyDown={handleKeyDown}>
      <h3 className="agents-form-title">
        {isEdit ? <>Edit Agent: <span className="agents-name-val">@{alias}</span></> : "Create Agent"}
      </h3>

      {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}

      <div className="agents-form-field">
        <label className="agents-form-label">Name</label>
        <input
          className="agents-input"
          value={alias}
          onChange={(e) => setAlias(e.target.value.toLowerCase())}
          placeholder="agent name"
          autoFocus={!isEdit}
        />
        {alias.trim() && <span className="agents-form-hint">use <strong>@{alias.trim()}</strong> in your prompts</span>}
      </div>

      <div className="agents-form-field">
        <ToggleButton
          checked={isDefault}
          onChange={setIsDefault}
          label="Default Agent"
          hint="handles unaddressed messages"
        />
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Plugin</label>
        <select
          className="agents-select"
          value={plugin}
          onChange={(e) => setPlugin(e.target.value)}
        >
          <option value="">Select plugin...</option>
          {pluginOptions.map((p) => (
            <option key={p} value={p}>{p}</option>
          ))}
        </select>
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Model</label>
        <input
          className="agents-input"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          placeholder="model override (optional)"
        />
      </div>

      <div className="agents-form-field agents-form-field--grow">
        <label className="agents-form-label">System Prompt</label>
        <textarea
          className="agents-input"
          value={systemPrompt}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="System prompt (leave empty for default)..."
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
          title="Delete Agent"
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
