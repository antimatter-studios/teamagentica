import { useEffect, useState } from "react";
import {
  getPluginConfig,
  updatePluginConfig,
  type Plugin,
  type PluginConfigEntry,
} from "../api/plugins";

interface Props {
  plugin: Plugin;
  onClose: () => void;
  onSaved: () => void;
}

interface ConfigField {
  key: string;
  value: string;
  is_secret: boolean;
  showSecret: boolean;
}

export default function PluginConfig({ plugin, onClose, onSaved }: Props) {
  const [fields, setFields] = useState<ConfigField[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    async function load() {
      try {
        const entries: PluginConfigEntry[] = await getPluginConfig(plugin.id);
        const existingKeys = new Set(entries.map((e) => e.key));

        const configFields: ConfigField[] = entries.map((e) => ({
          key: e.key,
          value: e.is_secret ? "" : e.value,
          is_secret: e.is_secret,
          showSecret: false,
        }));

        // Add fields from schema that don't exist yet
        for (const [key, schema] of Object.entries(plugin.config_schema)) {
          if (!existingKeys.has(key)) {
            configFields.push({
              key,
              value: "",
              is_secret: schema.secret,
              showSecret: false,
            });
          }
        }

        setFields(configFields);
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load config");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [plugin.id, plugin.config_schema]);

  function updateField(index: number, value: string) {
    setFields((prev) =>
      prev.map((f, i) => (i === index ? { ...f, value } : f))
    );
  }

  function toggleShow(index: number) {
    setFields((prev) =>
      prev.map((f, i) =>
        i === index ? { ...f, showSecret: !f.showSecret } : f
      )
    );
  }

  async function handleSave() {
    setSaving(true);
    setError("");
    try {
      const config: Record<string, { value: string; is_secret: boolean }> = {};
      for (const field of fields) {
        // Skip secret fields that haven't been changed (empty value)
        if (field.is_secret && field.value === "") continue;
        config[field.key] = {
          value: field.value,
          is_secret: field.is_secret,
        };
      }
      await updatePluginConfig(plugin.id, config);
      onSaved();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save config");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title">
            <span className="section-icon">[*]</span>
            CONFIGURE: {plugin.name}
          </h2>
        </div>

        {loading ? (
          <div className="modal-loading">
            <span className="spinner" />
            LOADING CONFIGURATION...
          </div>
        ) : (
          <div className="config-form">
            {fields.length === 0 && (
              <div className="config-empty">
                No configuration options available for this plugin.
              </div>
            )}

            {fields.map((field, index) => {
              const schema = plugin.config_schema[field.key];
              return (
                <div className="form-field" key={field.key}>
                  <label>
                    {field.key.toUpperCase()}
                    {schema?.required && (
                      <span className="config-required"> *</span>
                    )}
                    {field.is_secret && (
                      <span className="config-secret-badge">SECRET</span>
                    )}
                  </label>
                  <div className="config-input-wrapper">
                    <input
                      type={
                        field.is_secret && !field.showSecret
                          ? "password"
                          : "text"
                      }
                      value={field.value}
                      onChange={(e) => updateField(index, e.target.value)}
                      placeholder={
                        field.is_secret
                          ? "Leave empty to keep current value"
                          : `Enter ${field.key}`
                      }
                    />
                    {field.is_secret && (
                      <button
                        type="button"
                        className="config-toggle-btn"
                        onClick={() => toggleShow(index)}
                      >
                        {field.showSecret ? "HIDE" : "SHOW"}
                      </button>
                    )}
                  </div>
                </div>
              );
            })}

            {plugin.status === "running" && fields.length > 0 && (
              <div className="config-notice">
                Plugin will be restarted after saving.
              </div>
            )}

            {error && <div className="form-error">{error}</div>}

            <div className="modal-actions">
              <button className="plugin-action-btn" onClick={onClose}>
                CANCEL
              </button>
              <button
                className="login-submit"
                onClick={handleSave}
                disabled={saving || fields.length === 0}
              >
                {saving ? (
                  <span className="loading-text">
                    <span className="spinner" />
                    SAVING...
                  </span>
                ) : (
                  "SAVE CONFIGURATION"
                )}
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
