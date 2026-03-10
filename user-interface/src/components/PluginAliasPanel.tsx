import { useEffect, useState } from "react";
import {
  type Plugin,
  getPluginConfigSchema,
  getFieldOptions,
  getPluginConfig,
  updatePluginConfig,
} from "../api/plugins";

interface AliasEntry {
  name: string;
  target: string;
}

function parseAliasEntries(v: string): AliasEntry[] {
  if (!v) return [];
  try {
    const parsed = JSON.parse(v);
    if (Array.isArray(parsed)) return parsed;
  } catch { /* empty */ }
  return [];
}

function targetToModel(target: string, pluginId: string): string {
  if (target.startsWith(pluginId + ":")) return target.slice(pluginId.length + 1);
  if (target === pluginId) return "";
  return target;
}

function modelToTarget(model: string, pluginId: string): string {
  if (!model) return pluginId;
  if (model.includes(":")) return model;
  return `${pluginId}:${model}`;
}

async function findModelFieldKey(plugin: Plugin): Promise<string | null> {
  const schema = await getPluginConfigSchema(plugin.id);
  for (const [key, field] of Object.entries(schema)) {
    if (field.type === "select" && field.dynamic) return key;
  }
  return null;
}

interface Props {
  plugin: Plugin;
  onSaved?: () => void;
}

export default function PluginAliasPanel({ plugin, onSaved }: Props) {
  const [entries, setEntries] = useState<AliasEntry[]>([]);
  const [modelFieldKey, setModelFieldKey] = useState<string | null>(null);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [dirty, setDirty] = useState(false);

  // Load current aliases from plugin config.
  useEffect(() => {
    async function load() {
      try {
        const configs = await getPluginConfig(plugin.id);
        const aliasEntry = configs.find((c) => c.key === "PLUGIN_ALIASES");
        setEntries(parseAliasEntries(aliasEntry?.value || ""));
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load aliases");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [plugin.id]);

  // Resolve model field key from live schema.
  useEffect(() => {
    findModelFieldKey(plugin).then(setModelFieldKey).catch(() => setModelFieldKey(null));
  }, [plugin.id, plugin.status]);

  const fetchModels = () => {
    if (!modelFieldKey || plugin.status !== "running") return;

    setModelsLoading(true);
    setModelsError("");
    getFieldOptions(plugin.id, modelFieldKey)
      .then((res) => setModelOptions(res.options || []))
      .catch((err) => {
        setModelsError(err instanceof Error ? err.message : "Failed to fetch models");
      })
      .finally(() => setModelsLoading(false));
  };

  // Fetch available models from the plugin's dynamic model field.
  useEffect(() => {
    fetchModels();
  }, [plugin.id, plugin.status, modelFieldKey]);

  const updateEntry = (i: number, field: "name" | "model", val: string) => {
    setEntries((prev) =>
      prev.map((e, idx) => {
        if (idx !== i) return e;
        if (field === "name") return { ...e, name: val };
        return { ...e, target: modelToTarget(val, plugin.id) };
      })
    );
    setDirty(true);
  };

  const removeEntry = (i: number) => {
    setEntries((prev) => prev.filter((_, idx) => idx !== i));
    setDirty(true);
  };

  const addEntry = () => {
    setEntries((prev) => [...prev, { name: "", target: plugin.id }]);
    setDirty(true);
  };

  const handleSave = async () => {
    setSaving(true);
    setError("");
    setSaveSuccess(false);
    try {
      const cleaned = entries.filter((e) => e.name.trim() !== "");
      await updatePluginConfig(plugin.id, {
        PLUGIN_ALIASES: {
          value: JSON.stringify(cleaned),
          is_secret: false,
        },
      });
      setEntries(cleaned);
      setDirty(false);
      setSaveSuccess(true);
      onSaved?.();
      setTimeout(() => setSaveSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save aliases");
    } finally {
      setSaving(false);
    }
  };

  const supportsModels = modelFieldKey !== null;
  const hasModels = modelOptions.length > 0;

  if (loading) {
    return (
      <div className="config-inline-loading">
        <span className="spinner" />
        LOADING ALIASES...
      </div>
    );
  }

  return (
    <div className="alias-panel">
      <p className="alias-panel-desc">
        {supportsModels
          ? <>Map short @nicknames to models on this plugin. Use <code>@nickname</code> in chat to route to that model.</>
          : <>Map short @nicknames to this plugin. Use <code>@nickname</code> in chat to reference it.</>
        }
      </p>

      {supportsModels && plugin.status !== "running" && (
        <div className="alias-panel-warn">Plugin must be running to fetch available models.</div>
      )}
      {supportsModels && modelsLoading && (
        <div className="alias-panel-info"><span className="spinner" /> Fetching available models...</div>
      )}
      {supportsModels && modelsError && (
        <div className="alias-panel-warn">
          {modelsError} — <button className="alias-retry" onClick={fetchModels}>retry</button>
        </div>
      )}

      {entries.length === 0 ? (
        <div className="alias-empty">No aliases configured.</div>
      ) : (
        <div className="alias-panel-table">
          <div className="alias-panel-header-row">
            <span className="alias-col-name">NICKNAME</span>
            {supportsModels && <span className="alias-col-model">MODEL</span>}
            <span className="alias-col-del" />
          </div>
          {entries.map((entry, i) => (
            <div className="alias-panel-row" key={i}>
              <input
                className="alias-input alias-name"
                type="text"
                value={entry.name}
                onChange={(e) => updateEntry(i, "name", e.target.value)}
                placeholder="e.g. files"
              />
              {supportsModels && (hasModels ? (
                <select
                  className="alias-model-select"
                  value={targetToModel(entry.target, plugin.id)}
                  onChange={(e) => updateEntry(i, "model", e.target.value)}
                >
                  <option value="">-- default --</option>
                  {modelOptions.map((opt) => (
                    <option key={opt} value={opt}>{opt}</option>
                  ))}
                </select>
              ) : (
                <input
                  className="alias-input"
                  type="text"
                  value={targetToModel(entry.target, plugin.id)}
                  onChange={(e) => updateEntry(i, "model", e.target.value)}
                  placeholder={modelsLoading ? "loading models..." : "model name"}
                />
              ))}
              <button
                className="alias-delete"
                onClick={() => removeEntry(i)}
                title="Remove alias"
              >&times;</button>
            </div>
          ))}
        </div>
      )}

      <div className="alias-panel-footer">
        <button className="alias-add" onClick={addEntry}>+ Add Alias</button>

        {error && <div className="form-error">{error}</div>}
        {saveSuccess && <div className="form-success">Aliases saved.</div>}

        {dirty && (
          <button
            className="login-submit"
            onClick={handleSave}
            disabled={saving}
          >
            {saving ? (
              <span className="loading-text">
                <span className="spinner" />
                SAVING...
              </span>
            ) : (
              "SAVE ALIASES"
            )}
          </button>
        )}
      </div>
    </div>
  );
}
