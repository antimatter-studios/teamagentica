import { useEffect, useState, useCallback, type FormEvent } from "react";
import {
  listPlugins,
  installPlugin,
  uninstallPlugin,
  enablePlugin,
  disablePlugin,
  restartPlugin,
  type Plugin,
} from "../api/plugins";
import PluginConfig from "./PluginConfig";
import PluginLogs from "./PluginLogs";

export default function PluginList() {
  const [plugins, setPlugins] = useState<Plugin[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [showInstall, setShowInstall] = useState(false);
  const [configPlugin, setConfigPlugin] = useState<Plugin | null>(null);
  const [logsPlugin, setLogsPlugin] = useState<Plugin | null>(null);

  const fetchPlugins = useCallback(async () => {
    try {
      const list = await listPlugins();
      setPlugins(list);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load plugins");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchPlugins();
  }, [fetchPlugins]);

  // Auto-refresh every 10 seconds
  useEffect(() => {
    const interval = setInterval(fetchPlugins, 10000);
    return () => clearInterval(interval);
  }, [fetchPlugins]);

  async function handleAction(
    action: () => Promise<void>,
    successMsg?: string
  ) {
    setActionError("");
    try {
      await action();
      if (successMsg) {
        // Could show a toast here, but for now just refresh
      }
      await fetchPlugins();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : "Action failed");
    }
  }

  function statusColor(status: string): string {
    switch (status) {
      case "running":
        return "status-running";
      case "starting":
        return "status-starting";
      case "error":
      case "unhealthy":
        return "status-error";
      default:
        return "status-stopped";
    }
  }

  if (loading) {
    return (
      <div className="plugin-loading">
        <div className="spinner large" />
        <p>LOADING PLUGINS...</p>
      </div>
    );
  }

  return (
    <div className="plugin-page">
      <div className="plugin-page-header">
        <h2 className="section-title">
          <span className="section-icon">[+]</span>
          PLUGINS
          {plugins.length > 0 && (
            <span className="section-count">{plugins.length}</span>
          )}
        </h2>
        <button
          className="login-submit plugin-install-btn"
          onClick={() => setShowInstall(true)}
        >
          INSTALL PLUGIN
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}
      {actionError && <div className="form-error">{actionError}</div>}

      {plugins.length === 0 && !error && (
        <div className="plugin-empty">
          <div className="plugin-empty-icon">[~]</div>
          <p>No plugins installed.</p>
          <p className="plugin-empty-hint">
            Install your first plugin to get started.
          </p>
        </div>
      )}

      <div className="plugin-grid">
        {plugins.map((p) => (
          <div className="plugin-card" key={p.id}>
            <div className="plugin-card-header">
              <div className="plugin-name-row">
                <span className={`plugin-status-dot ${statusColor(p.status)}`} />
                <span className="plugin-name">{p.name}</span>
                <span className="plugin-version">v{p.version}</span>
              </div>
              <span className={`plugin-status-label ${statusColor(p.status)}`}>
                {p.status.toUpperCase()}
              </span>
            </div>

            <div className="plugin-image">{p.image}</div>

            {p.capabilities.length > 0 && (
              <div className="plugin-capabilities">
                {p.capabilities.map((cap) => (
                  <span className="capability-tag" key={cap}>
                    {cap}
                  </span>
                ))}
              </div>
            )}

            <div className="plugin-meta">
              <span className="plugin-meta-item">
                gRPC: {p.grpc_port}
              </span>
              <span className="plugin-meta-item">
                HTTP: {p.http_port}
              </span>
            </div>

            <div className="plugin-actions">
              <button
                className={`plugin-action-btn ${p.enabled ? "btn-warning" : "btn-success"}`}
                onClick={() =>
                  handleAction(() =>
                    p.enabled ? disablePlugin(p.id) : enablePlugin(p.id)
                  )
                }
              >
                {p.enabled ? "DISABLE" : "ENABLE"}
              </button>
              {p.status === "running" && (
                <button
                  className="plugin-action-btn"
                  onClick={() => handleAction(() => restartPlugin(p.id))}
                >
                  RESTART
                </button>
              )}
              <button
                className="plugin-action-btn"
                onClick={() => setConfigPlugin(p)}
              >
                CONFIGURE
              </button>
              <button
                className="plugin-action-btn"
                onClick={() => setLogsPlugin(p)}
              >
                LOGS
              </button>
              <button
                className="plugin-action-btn btn-danger"
                onClick={() => {
                  if (confirm(`Uninstall plugin "${p.name}"?`)) {
                    handleAction(() => uninstallPlugin(p.id));
                  }
                }}
              >
                UNINSTALL
              </button>
            </div>
          </div>
        ))}
      </div>

      {showInstall && (
        <InstallForm
          onClose={() => setShowInstall(false)}
          onInstalled={() => {
            setShowInstall(false);
            fetchPlugins();
          }}
        />
      )}

      {configPlugin && (
        <PluginConfig
          plugin={configPlugin}
          onClose={() => setConfigPlugin(null)}
          onSaved={() => {
            setConfigPlugin(null);
            fetchPlugins();
          }}
        />
      )}

      {logsPlugin && (
        <PluginLogs
          pluginId={logsPlugin.id}
          pluginName={logsPlugin.name}
          onClose={() => setLogsPlugin(null)}
        />
      )}
    </div>
  );
}

/* ── Inline Install Form ── */

interface InstallFormProps {
  onClose: () => void;
  onInstalled: () => void;
}

function InstallForm({ onClose, onInstalled }: InstallFormProps) {
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [version, setVersion] = useState("latest");
  const [marketplace, setMarketplace] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      await installPlugin({ name, image, version, marketplace });
      onInstalled();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Install failed");
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title">
            <span className="section-icon">[+]</span>
            INSTALL PLUGIN
          </h2>
        </div>

        <form onSubmit={handleSubmit} className="config-form">
          <div className="form-field">
            <label htmlFor="install-name">PLUGIN NAME</label>
            <input
              id="install-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-plugin"
              required
            />
          </div>

          <div className="form-field">
            <label htmlFor="install-image">CONTAINER IMAGE</label>
            <input
              id="install-image"
              type="text"
              value={image}
              onChange={(e) => setImage(e.target.value)}
              placeholder="registry.example.com/plugin:latest"
              required
            />
          </div>

          <div className="form-field">
            <label htmlFor="install-version">VERSION</label>
            <input
              id="install-version"
              type="text"
              value={version}
              onChange={(e) => setVersion(e.target.value)}
              placeholder="latest"
            />
          </div>

          <div className="form-field">
            <label htmlFor="install-marketplace">MARKETPLACE</label>
            <input
              id="install-marketplace"
              type="text"
              value={marketplace}
              onChange={(e) => setMarketplace(e.target.value)}
              placeholder="Optional marketplace URL"
            />
          </div>

          {error && <div className="form-error">{error}</div>}

          <div className="modal-actions">
            <button
              type="button"
              className="plugin-action-btn"
              onClick={onClose}
            >
              CANCEL
            </button>
            <button type="submit" className="login-submit" disabled={saving}>
              {saving ? (
                <span className="loading-text">
                  <span className="spinner" />
                  INSTALLING...
                </span>
              ) : (
                "INSTALL"
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
