import { useState, useEffect, useCallback } from "react";
import { apiClient } from "../api/client";
import type { RegistryAlias, AgentType, CreateAliasRequest, Plugin } from "@teamagentica/api-client";

type Section = {
  type: AgentType;
  label: string;
  description: string;
  capabilities: string[]; // capability prefixes to search for plugin dropdown
  hasModel: boolean;
};

const SECTIONS: Section[] = [
  { type: "agent", label: "Agents", description: "AI personas with provider, model, and system prompt", capabilities: ["agent:chat"], hasModel: true },
  { type: "tool_agent", label: "Tool Agents", description: "AI-powered tool plugins (image gen, video gen, etc.)", capabilities: ["agent:tool"], hasModel: true },
  { type: "tool", label: "Tools", description: "Service plugins exposed as addressable tools", capabilities: ["tool:", "storage:", "infra:"], hasModel: false },
];

interface EditingState {
  name: string;
  type: AgentType;
  plugin: string;
  provider: string;
  model: string;
  system_prompt: string;
}

const emptyForm = (type: AgentType): EditingState => ({
  name: "", type, plugin: "", provider: "", model: "", system_prompt: "",
});

export default function Agents() {
  const [aliases, setAliases] = useState<RegistryAlias[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<EditingState | null>(null);
  const [editingOriginalName, setEditingOriginalName] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<string | null>(null);

  // Plugin lists per section type, fetched once.
  const [pluginsByType, setPluginsByType] = useState<Record<AgentType, Plugin[]>>({
    agent: [], tool_agent: [], tool: [],
  });

  const load = useCallback(async () => {
    try {
      const data = await apiClient.agents.list();
      setAliases(data);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load agents");
    } finally {
      setLoading(false);
    }
  }, []);

  // Fetch available plugins for each section's capabilities.
  const loadPlugins = useCallback(async () => {
    const result: Record<AgentType, Plugin[]> = { agent: [], tool_agent: [], tool: [] };
    for (const sec of SECTIONS) {
      const seen = new Set<string>();
      for (const cap of sec.capabilities) {
        try {
          const plugins = await apiClient.plugins.search(cap);
          for (const p of plugins) {
            if (!seen.has(p.id)) {
              seen.add(p.id);
              result[sec.type].push(p);
            }
          }
        } catch { /* ignore */ }
      }
    }
    setPluginsByType(result);
  }, []);

  useEffect(() => { load(); loadPlugins(); }, [load, loadPlugins]);

  const byType = (type: AgentType) => aliases.filter((a) => a.type === type);

  const startCreate = (type: AgentType) => {
    setEditing(emptyForm(type));
    setEditingOriginalName(null);
  };

  const startEdit = (a: RegistryAlias) => {
    setEditing({
      name: a.name, type: a.type, plugin: a.plugin,
      provider: a.provider || "", model: a.model || "",
      system_prompt: a.system_prompt || "",
    });
    setEditingOriginalName(a.name);
  };

  const cancelEdit = () => { setEditing(null); setEditingOriginalName(null); };

  const save = async () => {
    if (!editing || !editing.name.trim() || !editing.plugin.trim()) return;
    setSaving(true);
    try {
      if (editingOriginalName) {
        await apiClient.agents.update(editingOriginalName, {
          type: editing.type,
          plugin: editing.plugin,
          provider: editing.provider || undefined,
          model: editing.model || undefined,
          system_prompt: editing.system_prompt || undefined,
        });
      } else {
        const req: CreateAliasRequest = {
          name: editing.name.trim(), type: editing.type, plugin: editing.plugin.trim(),
          provider: editing.provider || undefined,
          model: editing.model || undefined,
          system_prompt: editing.system_prompt || undefined,
        };
        await apiClient.agents.create(req);
      }
      setEditing(null);
      setEditingOriginalName(null);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  };

  const remove = async (name: string) => {
    setDeleting(name);
    try {
      await apiClient.agents.delete(name);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(null);
    }
  };

  if (loading) {
    return <div className="agents-page"><div className="agents-loading">Loading agents...</div></div>;
  }

  return (
    <div className="agents-page">
      <div className="agents-header">
        <h2>Agents & Tools</h2>
        {error && <div className="agents-error">{error}</div>}
      </div>

      {SECTIONS.map((sec) => (
        <AgentSection
          key={sec.type}
          section={sec}
          aliases={byType(sec.type)}
          plugins={pluginsByType[sec.type]}
          editing={editing}
          editingOriginalName={editingOriginalName}
          saving={saving}
          deleting={deleting}
          onStartCreate={() => startCreate(sec.type)}
          onStartEdit={startEdit}
          onCancel={cancelEdit}
          onSave={save}
          onDelete={remove}
          onEditChange={setEditing}
        />
      ))}
    </div>
  );
}

interface AgentSectionProps {
  section: Section;
  aliases: RegistryAlias[];
  plugins: Plugin[];
  editing: EditingState | null;
  editingOriginalName: string | null;
  saving: boolean;
  deleting: string | null;
  onStartCreate: () => void;
  onStartEdit: (a: RegistryAlias) => void;
  onCancel: () => void;
  onSave: () => void;
  onDelete: (name: string) => void;
  onEditChange: (state: EditingState) => void;
}

function AgentSection({
  section, aliases, plugins, editing, editingOriginalName, saving, deleting,
  onStartCreate, onStartEdit, onCancel, onSave, onDelete, onEditChange,
}: AgentSectionProps) {
  const isEditingThisType = editing?.type === section.type;
  const isCreating = isEditingThisType && editingOriginalName === null;

  return (
    <div className="agents-section">
      <div className="agents-section-header">
        <div>
          <h3>{section.label}</h3>
          <span className="agents-section-desc">{section.description}</span>
        </div>
        <button className="agents-add-btn" onClick={onStartCreate} disabled={isCreating}>
          + Add
        </button>
      </div>

      <div className="agents-table">
        <div className="agents-table-header">
          <span className="agents-col-name">Name</span>
          <span className="agents-col-plugin">Plugin</span>
          {section.hasModel && <span className="agents-col-model">Model</span>}
          {section.type === "agent" && <span className="agents-col-provider">Provider</span>}
          <span className="agents-col-actions">Actions</span>
        </div>

        {aliases.map((a) => {
          const isEditingThis = isEditingThisType && editingOriginalName === a.name;
          if (isEditingThis && editing) {
            return (
              <AgentFormRow
                key={a.name}
                form={editing}
                section={section}
                plugins={plugins}
                saving={saving}
                onChange={onEditChange}
                onSave={onSave}
                onCancel={onCancel}
              />
            );
          }
          return (
            <div className="agents-row" key={a.name}>
              <span className="agents-col-name agents-name-val">@{a.name}</span>
              <span className="agents-col-plugin">{a.plugin}</span>
              {section.hasModel && <span className="agents-col-model">{a.model || "—"}</span>}
              {section.type === "agent" && <span className="agents-col-provider">{a.provider || "—"}</span>}
              <span className="agents-col-actions">
                <button className="agents-edit-btn" onClick={() => onStartEdit(a)}>Edit</button>
                <button
                  className="agents-delete-btn"
                  onClick={() => onDelete(a.name)}
                  disabled={deleting === a.name}
                >
                  {deleting === a.name ? "..." : "Delete"}
                </button>
              </span>
            </div>
          );
        })}

        {isCreating && editing && (
          <AgentFormRow
            form={editing}
            section={section}
            plugins={plugins}
            saving={saving}
            onChange={onEditChange}
            onSave={onSave}
            onCancel={onCancel}
            isNew
          />
        )}

        {aliases.length === 0 && !isCreating && (
          <div className="agents-empty">No {section.label.toLowerCase()} configured</div>
        )}
      </div>
    </div>
  );
}

interface AgentFormRowProps {
  form: EditingState;
  section: Section;
  plugins: Plugin[];
  saving: boolean;
  isNew?: boolean;
  onChange: (state: EditingState) => void;
  onSave: () => void;
  onCancel: () => void;
}

function AgentFormRow({ form, section, plugins, saving, isNew, onChange, onSave, onCancel }: AgentFormRowProps) {
  const [models, setModels] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);

  // Fetch models when plugin changes (only for sections with models).
  useEffect(() => {
    if (!section.hasModel || !form.plugin) {
      setModels([]);
      return;
    }
    let cancelled = false;
    setModelsLoading(true);
    apiClient.agents.pluginModels(form.plugin)
      .then((res) => { if (!cancelled) setModels(res.models || []); })
      .catch(() => { if (!cancelled) setModels([]); })
      .finally(() => { if (!cancelled) setModelsLoading(false); });
    return () => { cancelled = true; };
  }, [form.plugin, section.hasModel]);

  const upd = (field: keyof EditingState, value: string) =>
    onChange({ ...form, [field]: value });

  const handlePluginChange = (pluginId: string) => {
    // Reset model when plugin changes.
    onChange({ ...form, plugin: pluginId, model: "" });
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") onSave();
    if (e.key === "Escape") onCancel();
  };

  return (
    <div className="agents-row agents-row-editing" onKeyDown={handleKeyDown}>
      <span className="agents-col-name">
        {isNew ? (
          <input
            className="agents-input"
            value={form.name}
            onChange={(e) => upd("name", e.target.value)}
            placeholder="alias name"
            autoFocus
          />
        ) : (
          <span className="agents-name-val">@{form.name}</span>
        )}
      </span>
      <span className="agents-col-plugin">
        <select
          className="agents-select"
          value={form.plugin}
          onChange={(e) => handlePluginChange(e.target.value)}
        >
          <option value="">Select plugin...</option>
          {plugins.map((p) => (
            <option key={p.id} value={p.id}>{p.name} ({p.id})</option>
          ))}
        </select>
      </span>
      {section.hasModel && (
        <span className="agents-col-model">
          {modelsLoading ? (
            <span className="agents-models-loading">loading...</span>
          ) : models.length > 0 ? (
            <select
              className="agents-select"
              value={form.model}
              onChange={(e) => upd("model", e.target.value)}
            >
              <option value="">Default</option>
              {models.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
          ) : (
            <input
              className="agents-input"
              value={form.model}
              onChange={(e) => upd("model", e.target.value)}
              placeholder={form.plugin ? "no models found" : "select plugin first"}
              disabled={!form.plugin}
            />
          )}
        </span>
      )}
      {section.type === "agent" && (
        <span className="agents-col-provider">
          <input
            className="agents-input"
            value={form.provider}
            onChange={(e) => upd("provider", e.target.value)}
            placeholder="provider"
          />
        </span>
      )}
      <span className="agents-col-actions">
        <button className="agents-save-btn" onClick={onSave} disabled={saving}>
          {saving ? "..." : "Save"}
        </button>
        <button className="agents-cancel-btn" onClick={onCancel}>Cancel</button>
      </span>
    </div>
  );
}
