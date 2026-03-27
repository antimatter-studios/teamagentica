import React, { useState } from "react";
import type { Plugin } from "@teamagentica/api-client";
import {
  usePluginConfig,
  type ConfigField,
  type SelectOption,
} from "../hooks/usePluginConfig";

// Extract value and label from a select option (string or {label, value}).
function optValue(opt: SelectOption): string {
  return typeof opt === "string" ? opt : opt.value;
}
function optLabel(opt: SelectOption): string {
  return typeof opt === "string" ? opt : opt.label;
}

/** Truncate text to maxLen characters with ellipsis. */
function truncate(text: string, maxLen: number): string {
  return text.length > maxLen ? text.slice(0, maxLen) + "…" : text;
}

/** Format milliseconds as a human-readable duration. */
function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

interface DAGNode {
  id: string;
  alias: string;
  tool?: string;
  prompt?: string;
  state: string;
  duration_ms?: number;
  error?: string;
}

/** Collapsible raw JSON debug view. */
function DAGRawJson({ item }: { item: Record<string, unknown> }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="dag-raw-json-section">
      <div className="dag-raw-json-toggle" onClick={() => setOpen(!open)}>
        <span style={{ marginRight: 6, display: "inline-block", width: 10, fontSize: 9 }}>
          {open ? "▼" : "▶"}
        </span>
        Raw JSON
      </div>
      {open && (
        <pre className="schema-readonly-json" style={{ margin: 0, padding: "8px 0" }}>
          {JSON.stringify(item, null, 2)}
        </pre>
      )}
    </div>
  );
}

/** Expandable item row — click to reveal DAG node detail. */
function ExpandableItem({ item, idx }: { item: Record<string, unknown>; idx: number }) {
  const [open, setOpen] = useState(false);
  const state = String(item.state || "");
  const stateColor = state === "running" ? "#f59e0b"
    : state === "completed" ? "#22c55e"
    : state === "failed" ? "#ef4444"
    : state ? "#888"
    : "#e5e5e5";
  const message = String(item.message || "");
  const nodes = (item.nodes || []) as DAGNode[];

  return (
    <div key={String(item.id || idx)}>
      <div
        className="schema-readonly-row schema-readonly-row-clickable"
        onClick={() => setOpen(!open)}
        style={{ cursor: "pointer", display: "flex", alignItems: "center", gap: 8 }}
      >
        <span style={{ color: stateColor, fontWeight: state === "running" ? 600 : 400, flex: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          <span style={{ marginRight: 6, display: "inline-block", width: 12, fontSize: 10 }}>
            {open ? "▼" : "▶"}
          </span>
          {String(item.time || "")} {message}
        </span>
        <span className="schema-readonly-value" style={{ color: "#f59e0b", flexShrink: 0, textAlign: "right", fontSize: 12 }}>
          {String(item.summary || "")}
        </span>
      </div>
      {open && (
        <div className="dag-detail">
          <div className="dag-detail-message">{message}</div>
          {nodes.length > 0 && (
            <div className="dag-nodes-grid">
              <div className="dag-nodes-header">
                <span>STATUS</span>
                <span>ALIAS</span>
                <span>TOOL</span>
                <span>DURATION</span>
              </div>
              {nodes.map((node, ni) => {
                const nc = node.state === "running" ? "#f59e0b"
                  : node.state === "completed" ? "#22c55e"
                  : node.state === "failed" ? "#ef4444"
                  : "#888";
                const icon = node.state === "running" ? "▶"
                  : node.state === "completed" ? "✓"
                  : node.state === "failed" ? "✗"
                  : "○";
                return (
                  <React.Fragment key={node.id || ni}>
                    <div className="dag-node-row">
                      <span style={{ color: nc }}>{icon} {node.state}</span>
                      <span>@{node.alias}</span>
                      <span style={{ color: "var(--text-muted)" }}>{node.tool || "—"}</span>
                      <span>{node.duration_ms ? formatDuration(node.duration_ms) : "—"}</span>
                    </div>
                    {node.prompt && (
                      <div className="dag-node-prompt">
                        <span style={{ color: "var(--text-muted)", fontSize: 11 }}>prompt:</span>
                        <span style={{ fontSize: 11, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
                          {node.prompt}
                        </span>
                      </div>
                    )}
                  </React.Fragment>
                );
              })}
            </div>
          )}
          {nodes.length === 0 && (
            <div className="dag-nodes-empty">No steps recorded</div>
          )}
          <DAGRawJson item={item} />
        </div>
      )}
    </div>
  );
}

import type { SchemaSection } from "../hooks/usePluginConfig";

function ReadonlyTable({ items, columns }: { items: Record<string, unknown>[]; columns: string[] }) {
  return (
    <div className="schema-table-wrap">
      <div className="schema-table-header" style={{ gridTemplateColumns: `repeat(${columns.length}, 1fr)` }}>
        {columns.map((col) => (
          <span key={col}>{col.replace(/_/g, " ").toUpperCase()}</span>
        ))}
      </div>
      {items.map((item, idx) => (
        <div className="schema-table-row" key={String(item.id || idx)} style={{ gridTemplateColumns: `repeat(${columns.length}, 1fr)` }}>
          {columns.map((col) => (
            <span key={col}>{item[col] == null ? "—" : String(item[col])}</span>
          ))}
        </div>
      ))}
      {items.length === 0 && (
        <div className="schema-readonly-empty">No entries</div>
      )}
    </div>
  );
}

function ReadonlySection({ section }: { section: SchemaSection }) {
  return (
    <div className="schema-readonly-section">
      <div className="schema-readonly-header">
        {section.name.replace(/_/g, " ").toUpperCase()}
      </div>
      <div className="schema-readonly-fields">
        {section.items && section.columns ? (
          <ReadonlyTable items={section.items} columns={section.columns} />
        ) : section.items ? (
          <>
            {section.items.map((item, idx) => (
              <ExpandableItem key={String(item.id || idx)} item={item} idx={idx} />
            ))}
            {section.items.length === 0 && (
              <div className="schema-readonly-empty">No entries</div>
            )}
          </>
        ) : (
          <>
            {section.fields.map((f) => (
              <div className="schema-readonly-row" key={f.key}>
                <span className="schema-readonly-key">{f.key}</span>
                <span className="schema-readonly-value">{f.value}</span>
              </div>
            ))}
            {section.fields.length === 0 && (
              <div className="schema-readonly-empty">No entries</div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

interface Props {
  plugin: Plugin;
  onSaved: () => void;
}

export default function PluginConfigForm({ plugin, onSaved }: Props) {
  const {
    fields,
    dirty,
    loading,
    saving,
    error,
    saveSuccess,
    extraSections,
    dynamicOptions,
    oauthStates,
    updateField,
    handleSave,
    handleOAuthLogin,
    handleOAuthLogout,
  } = usePluginConfig(plugin, onSaved);

  function renderField(field: ConfigField, index: number) {
    const schema = field.schema;
    const fieldType = schema?.type || "string";
    const label = schema?.label || field.key.toUpperCase();
    const helpText = schema?.help_text;

    if (fieldType === "oauth") {
      const state = oauthStates[field.key];

      return (
        <div className="auth-section" key={field.key}>
          <div className="auth-section-header">
            <span className="section-icon">[&gt;</span> {label.toUpperCase()}
          </div>
          {helpText && <span className="config-help-text">{helpText}</span>}

          {plugin.status !== "running" ? (
            <div className="auth-hint">
              Plugin must be running to authenticate.
            </div>
          ) : !state || state.loading ? (
            <div className="auth-hint">
              <span className="spinner" /> Checking authentication...
            </div>
          ) : state.status?.authenticated ? (
            <div className="auth-authenticated">
              <span className="auth-status-ok">AUTHENTICATED</span>
              {state.status.detail && (
                <span className="auth-account">{state.status.detail}</span>
              )}
              <button
                className="plugin-action-btn btn-warning auth-logout-btn"
                onClick={() => handleOAuthLogout(field.key)}
              >
                LOGOUT
              </button>
            </div>
          ) : state.deviceCode ? (
            <div className="auth-device-code">
              <p>Open the link below and enter the code to sign in:</p>
              <a
                className="auth-verify-link"
                href={state.deviceCode.url}
                target="_blank"
                rel="noopener noreferrer"
              >
                {state.deviceCode.url}
              </a>
              <div className="auth-code-display">{state.deviceCode.code}</div>
              <p className="auth-code-hint">
                {state.polling ? (
                  <>
                    <span className="spinner" /> Waiting for login to complete...
                  </>
                ) : (
                  "Login flow expired. Click the button to try again."
                )}
              </p>
            </div>
          ) : (
            <div className="auth-login-prompt">
              <button
                className="login-submit auth-login-btn"
                onClick={() => handleOAuthLogin(field.key)}
              >
                {label.toUpperCase()}
              </button>
            </div>
          )}
          {state?.error && <div className="form-error">{state.error}</div>}
        </div>
      );
    }

    // Aliases have their own dedicated tab — skip rendering here.
    if (fieldType === "aliases") {
      return null;
    }

    if (fieldType === "model_list") {
      let entries: string[] = [];
      try {
        entries = JSON.parse(field.value || "[]");
      } catch {
        entries = [];
      }

      const updateEntries = (updated: string[]) => {
        updateField(index, JSON.stringify(updated));
      };

      return (
        <div className="form-field" key={field.key}>
          <label>
            {label}
            {schema?.required && <span className="config-required"> *</span>}
          </label>
          {helpText && <span className="config-help-text">{helpText}</span>}

          <div className="model-list-table">
            {entries.map((entry, i) => (
              <div className="model-list-row" key={i}>
                <input
                  className="model-list-input"
                  type="text"
                  value={entry}
                  placeholder="model name (e.g. llama3.2:3b)"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = e.target.value;
                    updateEntries(updated);
                  }}
                />
                <button
                  className="model-list-remove"
                  onClick={() => {
                    updateEntries(entries.filter((_, j) => j !== i));
                  }}
                  title="Remove model"
                >
                  ✕
                </button>
              </div>
            ))}
            <button
              className="model-list-add"
              onClick={() => {
                updateEntries([...entries, ""]);
              }}
            >
              + Add model
            </button>
          </div>
        </div>
      );
    }

    if (fieldType === "bot_token") {
      let entries: { alias: string; token: string }[] = [];
      try {
        entries = JSON.parse(field.value || "[]");
      } catch {
        entries = [];
      }

      const updateEntries = (updated: typeof entries) => {
        updateField(index, JSON.stringify(updated));
      };

      return (
        <div className="form-field" key={field.key}>
          <label>
            {label}
            {schema?.required && <span className="config-required"> *</span>}
          </label>
          {helpText && <span className="config-help-text">{helpText}</span>}

          <div className="bot-token-table">
            {entries.length > 0 && (
              <div className="bot-token-header">
                <span className="bot-token-col-alias">ALIAS</span>
                <span className="bot-token-col-token">TOKEN</span>
                <span className="bot-token-col-action" />
              </div>
            )}
            {entries.map((entry, i) => (
              <div className="bot-token-row" key={i}>
                <input
                  className="bot-token-alias"
                  type="text"
                  value={entry.alias}
                  placeholder="alias"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = { ...updated[i], alias: e.target.value };
                    updateEntries(updated);
                  }}
                />
                <input
                  className="bot-token-token"
                  type="password"
                  value={entry.token}
                  placeholder="bot token"
                  onChange={(e) => {
                    const updated = [...entries];
                    updated[i] = { ...updated[i], token: e.target.value };
                    updateEntries(updated);
                  }}
                />
                <button
                  className="bot-token-remove"
                  onClick={() => {
                    updateEntries(entries.filter((_, j) => j !== i));
                  }}
                  title="Remove bot"
                >
                  ✕
                </button>
              </div>
            ))}
            <button
              className="bot-token-add"
              onClick={() => {
                updateEntries([...entries, { alias: "", token: "" }]);
              }}
            >
              + Add bot
            </button>
          </div>
        </div>
      );
    }

    const isReadOnly = schema?.readonly ?? false;

    return (
      <div className="form-field" key={field.key}>
        <label>
          {label}
          {schema?.required && <span className="config-required"> *</span>}
          {isReadOnly && (
            <span className="config-secret-badge" style={{ background: "#555" }}>READ-ONLY</span>
          )}
          {field.is_secret && (
            <span className="config-secret-badge">SECRET</span>
          )}
          {fieldType === "select" && schema?.dynamic && dynamicOptions[field.key]?.fallback && (
            <span className="config-secret-badge" style={{ background: "#a16207" }}> STATIC FALLBACK</span>
          )}
        </label>
        {helpText && <span className="config-help-text">{helpText}</span>}

        {fieldType === "select" && schema?.dynamic ? (() => {
          const dyn = dynamicOptions[field.key];
          const isLoading = dyn?.loading ?? false;
          const dynError = dyn?.error;
          const opts = dyn?.options ?? [];
          const optValues = opts.map(optValue);
          const allOpts = field.value && !optValues.includes(field.value)
            ? [field.value, ...opts]
            : opts;
          const isDisabled = isReadOnly || isLoading || !!dynError;
          return (
            <>
              <select
                className={`config-select${isDisabled ? " config-select-disabled" : ""}`}
                value={field.value}
                onChange={(e) => updateField(index, e.target.value)}
                disabled={isDisabled}
              >
                {isLoading ? (
                  <option value={field.value}>{field.value || "Loading..."}</option>
                ) : dynError ? (
                  field.value
                    ? <option value={field.value}>{field.value}</option>
                    : <option value="">-- Unavailable --</option>
                ) : (
                  <>
                    <option value="">-- Select --</option>
                    {allOpts.map((opt) => (
                      <option key={optValue(opt)} value={optValue(opt)}>{optLabel(opt)}</option>
                    ))}
                  </>
                )}
              </select>
              {isLoading && (
                <span className="config-dynamic-loading">
                  <span className="spinner" /> Fetching available options...
                </span>
              )}
              {dynError && (
                <span className="config-dynamic-error">{dynError}</span>
              )}
            </>
          );
        })() : fieldType === "select" && schema?.options ? (
          <select
            className="config-select"
            value={field.value}
            onChange={(e) => updateField(index, e.target.value)}
            disabled={isReadOnly}
          >
            <option value="">-- Select --</option>
            {schema.options.map((opt) => (
              <option key={optValue(opt)} value={optValue(opt)}>{optLabel(opt)}</option>
            ))}
          </select>
        ) : fieldType === "boolean" ? (
          <label className="config-toggle-label">
            <input
              type="checkbox"
              className="config-checkbox"
              checked={field.value === "true" || field.value === "1"}
              onChange={(e) =>
                updateField(index, e.target.checked ? "true" : "false")
              }
              disabled={isReadOnly}
            />
            <span className="config-toggle-switch" />
            <span className="config-toggle-text">
              {field.value === "true" || field.value === "1"
                ? "Enabled"
                : "Disabled"}
            </span>
          </label>
        ) : fieldType === "number" ? (
          <input
            type="number"
            value={field.value}
            onChange={(e) => updateField(index, e.target.value)}
            placeholder={`Enter ${field.key}`}
            disabled={isReadOnly}
          />
        ) : (
          <input
            type="text"
            value={
              field.is_secret && field.hasStoredValue && !field.value
                ? "••••••••"
                : field.value
            }
            onChange={(e) => {
              if (isReadOnly) return;
              const val = e.target.value;
              if (field.is_secret && field.hasStoredValue && !field.value) {
                updateField(index, val.replace("••••••••", ""));
              } else {
                updateField(index, val);
              }
            }}
            placeholder={
              field.is_secret
                ? "Enter secret value"
                : `Enter ${field.key}`
            }
            readOnly={isReadOnly}
          />
        )}
      </div>
    );
  }

  if (loading) {
    return (
      <div className="config-inline-loading">
        <span className="spinner" />
        LOADING CONFIGURATION...
      </div>
    );
  }

  return (
    <div className="config-form">
      {fields.filter((f) => f.key !== "PLUGIN_PORT" && f.key !== "PLUGIN_DATA_PATH").length === 0 && (
        <div className="config-empty">
          No configuration options available for this plugin.
        </div>
      )}

      {fields.map((field, index) => {
        // Hide system-injected env vars that are not user-configurable.
        if (field.key === "PLUGIN_PORT" || field.key === "PLUGIN_DATA_PATH") return null;
        if (field.schema?.visible_when) {
          const dep = fields.find(
            (f) => f.key === field.schema!.visible_when!.field
          );
          if (dep && dep.value !== field.schema.visible_when.value) {
            return null;
          }
        }
        return renderField(field, index);
      })}

      {extraSections.length > 0 && extraSections.map((section) => (
        <ReadonlySection key={section.name} section={section} />
      ))}

      {error && <div className="form-error">{error}</div>}
      {saveSuccess && <div className="form-success">Configuration saved. Changes are now active.</div>}

      {fields.length > 0 && (
        <div className="config-inline-actions">
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
              "SAVE CONFIGURATION"
            )}
          </button>
        </div>
      )}
    </div>
  );
}
