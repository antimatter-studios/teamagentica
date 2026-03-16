import type { Plugin } from "@teamagentica/api-client";
import {
  usePluginConfig,
  type ConfigField,
} from "../hooks/usePluginConfig";

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
          const allOpts = field.value && !opts.includes(field.value)
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
                      <option key={opt} value={opt}>{opt}</option>
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
              <option key={opt} value={opt}>{opt}</option>
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
      {fields.length === 0 && (
        <div className="config-empty">
          No configuration options available for this plugin.
        </div>
      )}

      {fields.map((field, index) => {
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

      {plugin.status === "running" && dirty && (
        <div className="config-notice">
          Plugin will be restarted after saving.
        </div>
      )}

      {error && <div className="form-error">{error}</div>}
      {saveSuccess && <div className="form-success">Configuration saved.</div>}

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
