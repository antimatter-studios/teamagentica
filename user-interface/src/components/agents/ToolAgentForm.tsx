import { useState, useEffect, useCallback } from "react";
import { apiClient } from "../../api/client";
import type { RegistryAlias, CreateAliasRequest, Plugin } from "@teamagentica/api-client";

interface Props {
  alias?: RegistryAlias;
  plugins: Plugin[];
  onSave: () => void;
  onCancel: () => void;
}

export default function ToolAgentForm({ alias, plugins, onSave, onCancel }: Props) {
  const isEdit = !!alias;
  const [name, setName] = useState(alias?.name ?? "");
  const [plugin, setPlugin] = useState(alias?.plugin ?? "");
  const [model, setModel] = useState(alias?.model ?? "");
  const [systemPrompt, setSystemPrompt] = useState(alias?.system_prompt ?? "");
  const [models, setModels] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!plugin) { setModels([]); return; }
    let cancelled = false;
    setModelsLoading(true);
    apiClient.agents.pluginModels(plugin)
      .then((res) => { if (!cancelled) setModels(res.models || []); })
      .catch(() => { if (!cancelled) setModels([]); })
      .finally(() => { if (!cancelled) setModelsLoading(false); });
    return () => { cancelled = true; };
  }, [plugin]);

  const handlePluginChange = (pluginId: string) => {
    setPlugin(pluginId);
    setModel("");
  };

  const save = useCallback(async () => {
    if (!name.trim() || !plugin.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await apiClient.agents.update(alias!.name, {
          type: "tool_agent",
          plugin: plugin.trim(),
          model: model || undefined,
          system_prompt: systemPrompt || undefined,
        });
      } else {
        const req: CreateAliasRequest = {
          name: name.trim(), type: "tool_agent", plugin: plugin.trim(),
          model: model || undefined,
          system_prompt: systemPrompt || undefined,
        };
        await apiClient.agents.create(req);
      }
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [name, plugin, model, systemPrompt, isEdit, alias, onSave]);

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
        {isEdit ? <>Edit Tool Agent: <span className="agents-name-val">@{alias!.name}</span></> : "Create Tool Agent"}
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
          onChange={(e) => handlePluginChange(e.target.value)}
        >
          <option value="">Select plugin...</option>
          {plugins.map((p) => (
            <option key={p.id} value={p.id}>{p.name} ({p.id})</option>
          ))}
        </select>
      </div>

      <div className="agents-form-field">
        <label className="agents-form-label">Model</label>
        {modelsLoading ? (
          <span className="agents-models-loading">loading...</span>
        ) : models.length > 0 ? (
          <select className="agents-select" value={model} onChange={(e) => setModel(e.target.value)}>
            <option value="">Default</option>
            {models.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        ) : (
          <input
            className="agents-input"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder={plugin ? "no models found" : "select plugin first"}
            disabled={!plugin}
          />
        )}
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
