import { useEffect, useState, useCallback, useRef, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import { useMarketplaceStore } from "../stores/marketplaceStore";
import { usePluginStore } from "../stores/pluginStore";

export default function Marketplace() {
  const { catalog, loading, error, query } = useMarketplaceStore(
    useShallow((s) => ({ catalog: s.catalog, loading: s.loading, error: s.error, query: s.query }))
  );
  const setQuery = useMarketplaceStore((s) => s.setQuery);
  const fetch = useMarketplaceStore((s) => s.fetch);
  const install = useMarketplaceStore((s) => s.install);
  const plugins = usePluginStore((s) => s.plugins);
  const fetchPlugins = usePluginStore((s) => s.fetch);

  const installedIds = useMemo(() => new Set(plugins.map((p) => p.id)), [plugins]);

  const [searchInput, setSearchInput] = useState("");
  const [installing, setInstalling] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const toastTimer = useRef<ReturnType<typeof setTimeout>>(undefined);

  const showToast = useCallback((msg: string) => {
    setToast(msg);
    if (toastTimer.current) clearTimeout(toastTimer.current);
    toastTimer.current = setTimeout(() => setToast(null), 3000);
  }, []);

  useEffect(() => {
    fetch();
  }, [fetch, query]);

  useEffect(() => {
    fetchPlugins();
  }, [fetchPlugins]);

  async function handleInstall(pluginId: string) {
    setInstalling(pluginId);
    try {
      await install(pluginId);
      await fetchPlugins();
      showToast("Plugin installed successfully");
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Install failed");
    } finally {
      setInstalling(null);
    }
  }

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    setQuery(searchInput);
  }

  if (loading) {
    return (
      <div className="plugin-loading">
        <div className="spinner large" />
        <p>SCANNING MARKETPLACE...</p>
      </div>
    );
  }

  return (
    <div className="plugin-page">
      <div className="plugin-page-header">
        <h2 className="section-title">
          <span className="section-icon">[M]</span>
          MARKETPLACE
          {catalog.length > 0 && (
            <span className="section-count">{catalog.length}</span>
          )}
        </h2>
      </div>

      <form className="marketplace-search" onSubmit={handleSearch}>
        <div className="search-bar">
          <input
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search plugins..."
            className="search-input"
          />
          <button type="submit" className="plugin-action-btn search-btn">
            SEARCH
          </button>
          {query && (
            <button
              type="button"
              className="plugin-action-btn"
              onClick={() => {
                setSearchInput("");
                setQuery("");
              }}
            >
              CLEAR
            </button>
          )}
        </div>
      </form>

      {error && <div className="form-error">{error}</div>}

      {catalog.length === 0 && !error && (
        <div className="plugin-empty">
          <div className="plugin-empty-icon">[~]</div>
          <p>{query ? "No plugins match your search." : "No plugins available."}</p>
          <p className="plugin-empty-hint">
            {query
              ? "Try a different search term."
              : "Add a marketplace provider to browse plugins."}
          </p>
        </div>
      )}

      <div className="plugin-grid">
        {catalog.map((p) => {
          const isInstalled = installedIds.has(p.plugin_id);
          const isInstalling = installing === p.plugin_id;

          return (
            <div className="plugin-card" key={`${p.provider}-${p.plugin_id}`}>
              <div className="plugin-card-header">
                <div className="plugin-name-row">
                  <span className="plugin-name">{p.name}</span>
                  <span className="plugin-version">v{p.version}</span>
                </div>
                {p.provider && (
                  <span className="marketplace-provider-badge">{p.provider}</span>
                )}
              </div>

              {p.description && (
                <p className="marketplace-description">{p.description}</p>
              )}

              {p.author && (
                <div className="plugin-meta">
                  <span className="plugin-meta-item">by {p.author}</span>
                </div>
              )}

              {p.tags && p.tags.length > 0 && (
                <div className="plugin-capabilities">
                  {p.tags.map((tag) => (
                    <span className="capability-tag" key={tag}>
                      {tag}
                    </span>
                  ))}
                </div>
              )}

              <div className="plugin-actions">
                {isInstalled ? (
                  <span className="marketplace-installed-badge">INSTALLED</span>
                ) : (
                  <button
                    className="login-submit marketplace-install-btn"
                    onClick={() => handleInstall(p.plugin_id)}
                    disabled={isInstalling}
                  >
                    {isInstalling ? (
                      <span className="loading-text">
                        <span className="spinner" />
                        INSTALLING...
                      </span>
                    ) : (
                      "INSTALL"
                    )}
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {toast && (
        <div className="toast-notification">
          <span className="toast-text">{toast}</span>
        </div>
      )}
    </div>
  );
}
