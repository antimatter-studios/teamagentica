import { useEffect, useState, useCallback, useRef, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import { useMarketplaceStore } from "../stores/marketplaceStore";
import { usePluginStore } from "../stores/pluginStore";
import type { MarketplacePlugin, MarketplaceGroup } from "@teamagentica/api-client";

export default function Marketplace() {
  const { catalog, groups, providers, loading, error, query, selectedProvider } =
    useMarketplaceStore(
      useShallow((s) => ({
        catalog: s.catalog,
        groups: s.groups,
        providers: s.providers,
        loading: s.loading,
        error: s.error,
        query: s.query,
        selectedProvider: s.selectedProvider,
      }))
    );
  const setQuery = useMarketplaceStore((s) => s.setQuery);
  const setSelectedProvider = useMarketplaceStore((s) => s.setSelectedProvider);
  const fetch = useMarketplaceStore((s) => s.fetch);
  const fetchProviders = useMarketplaceStore((s) => s.fetchProviders);
  const install = useMarketplaceStore((s) => s.install);
  const plugins = usePluginStore((s) => s.plugins);
  const fetchPlugins = usePluginStore((s) => s.fetch);

  const installedIds = useMemo(
    () => new Set(plugins.map((p) => p.id)),
    [plugins]
  );

  const [searchInput, setSearchInput] = useState("");
  const [installing, setInstalling] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [selectedGroup, setSelectedGroup] = useState<string | null>(null);
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
    fetchProviders();
  }, [fetchPlugins, fetchProviders]);

  // Filter catalog by selected provider
  const providerFiltered = useMemo(() => {
    if (!selectedProvider) return catalog;
    return catalog.filter((p) => p.provider === selectedProvider);
  }, [catalog, selectedProvider]);

  // Sort groups by order field
  const sortedGroups = useMemo(() => {
    return [...groups].sort((a, b) => a.order - b.order);
  }, [groups]);

  // Group plugins by their group field, in group order
  const groupedPlugins = useMemo(() => {
    const filtered = selectedGroup
      ? providerFiltered.filter((p) => p.group === selectedGroup)
      : providerFiltered;

    const byGroup = new Map<string, MarketplacePlugin[]>();
    for (const p of filtered) {
      const g = p.group || "other";
      if (!byGroup.has(g)) byGroup.set(g, []);
      byGroup.get(g)!.push(p);
    }

    // Build ordered sections
    const sections: { group: MarketplaceGroup; plugins: MarketplacePlugin[] }[] = [];
    for (const gm of sortedGroups) {
      const entries = byGroup.get(gm.id);
      if (entries && entries.length > 0) {
        sections.push({ group: gm, plugins: entries });
        byGroup.delete(gm.id);
      }
    }
    // Any remaining groups not in the metadata
    for (const [id, entries] of byGroup) {
      sections.push({
        group: { id, name: id.charAt(0).toUpperCase() + id.slice(1), description: "", order: 999 },
        plugins: entries,
      });
    }
    return sections;
  }, [providerFiltered, sortedGroups, selectedGroup]);

  // Count plugins per group for the sidebar
  const groupCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const p of providerFiltered) {
      const g = p.group || "other";
      counts.set(g, (counts.get(g) || 0) + 1);
    }
    return counts;
  }, [providerFiltered]);

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

  const totalFiltered = providerFiltered.length;

  if (loading) {
    return (
      <div className="plugin-loading">
        <div className="spinner large" />
        <p>SCANNING MARKETPLACE...</p>
      </div>
    );
  }

  return (
    <div className="plugin-layout">
      {/* ── Left sidebar: groups ── */}
      <aside className="plugin-sidebar">
        <div className="plugin-sidebar-header">
          <span className="section-icon">[M]</span>
          CATEGORIES
          {sortedGroups.length > 0 && (
            <span className="section-count">{sortedGroups.length}</span>
          )}
        </div>

        <nav className="plugin-sidebar-list">
          <div
            className={`plugin-sidebar-item${selectedGroup === null ? " active" : ""}`}
            onClick={() => setSelectedGroup(null)}
          >
            <span className="plugin-sidebar-name">All Plugins</span>
            <span className="marketplace-sidebar-count">{totalFiltered}</span>
          </div>

          {sortedGroups.map((g) => {
            const count = groupCounts.get(g.id) || 0;
            if (count === 0) return null;
            return (
              <div
                key={g.id}
                className={`plugin-sidebar-item${selectedGroup === g.id ? " active" : ""}`}
                onClick={() => setSelectedGroup(g.id)}
                title={g.description}
              >
                <span className="plugin-sidebar-name">{g.name}</span>
                <span className="marketplace-sidebar-count">{count}</span>
              </div>
            );
          })}
        </nav>

        {/* Provider filter (collapsed under categories) */}
        {providers.length > 1 && (
          <>
            <div className="plugin-sidebar-header" style={{ marginTop: 16 }}>
              <span className="section-icon">[P]</span>
              PROVIDERS
            </div>
            <nav className="plugin-sidebar-list">
              <div
                className={`plugin-sidebar-item${selectedProvider === null ? " active" : ""}`}
                onClick={() => setSelectedProvider(null)}
              >
                <span className="plugin-sidebar-name">All</span>
              </div>
              {providers.map((pr) => (
                <div
                  key={pr.id}
                  className={`plugin-sidebar-item${selectedProvider === pr.name ? " active" : ""}`}
                  onClick={() => setSelectedProvider(pr.name)}
                >
                  <span
                    className={`plugin-status-dot ${pr.enabled ? "status-running" : "status-stopped"}`}
                  />
                  <span className="plugin-sidebar-name">{pr.name}</span>
                </div>
              ))}
            </nav>
          </>
        )}
      </aside>

      {/* ── Right content: grouped plugin list ── */}
      <main className="plugin-detail">
        <div className="marketplace-content-header">
          <h2 className="section-title marketplace-title">
            {selectedGroup
              ? sortedGroups.find((g) => g.id === selectedGroup)?.name || selectedGroup
              : "MARKETPLACE"}
            {totalFiltered > 0 && (
              <span className="section-count">{selectedGroup ? (groupCounts.get(selectedGroup) || 0) : totalFiltered}</span>
            )}
          </h2>

          <form className="marketplace-search-inline" onSubmit={handleSearch}>
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
          </form>
        </div>

        {error && <div className="form-error">{error}</div>}

        {groupedPlugins.length === 0 && !error && (
          <div className="plugin-detail-empty">
            <div className="plugin-empty-icon">[~]</div>
            <p>
              {query
                ? "No plugins match your search."
                : "No plugins available."}
            </p>
            <p className="plugin-empty-hint">
              {query
                ? "Try a different search term."
                : "Add a marketplace provider to browse plugins."}
            </p>
          </div>
        )}

        <div className="marketplace-list">
          {groupedPlugins.map(({ group, plugins: groupPlugins }) => (
            <div key={group.id} className="marketplace-group">
              {/* Only show group headers when viewing "All Plugins" */}
              {selectedGroup === null && (
                <div className="marketplace-group-header">
                  <span className="marketplace-group-name">{group.name}</span>
                  {group.description && (
                    <span className="marketplace-group-desc">{group.description}</span>
                  )}
                  <span className="marketplace-sidebar-count">{groupPlugins.length}</span>
                </div>
              )}

              {groupPlugins.map((p) => {
                const isInstalled = installedIds.has(p.plugin_id);
                const isInstalling = installing === p.plugin_id;

                return (
                  <div
                    className="marketplace-row"
                    key={`${p.provider}-${p.plugin_id}`}
                  >
                    <div className="marketplace-row-main">
                      <div className="marketplace-row-title">
                        <span className="plugin-name">{p.name}</span>
                        <span className="plugin-version">v{p.version}</span>
                        {p.author && (
                          <span className="marketplace-row-author">
                            by {p.author}
                          </span>
                        )}
                      </div>
                      {p.description && (
                        <p className="marketplace-row-desc">{p.description}</p>
                      )}
                    </div>

                    <div className="marketplace-row-meta">
                      {p.tags && p.tags.length > 0 && (
                        <div className="plugin-capabilities">
                          {p.tags.map((tag) => (
                            <span className="capability-tag" key={tag}>
                              {tag}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>

                    <div className="marketplace-row-action">
                      {isInstalled ? (
                        <span className="marketplace-installed-badge">
                          INSTALLED
                        </span>
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
          ))}
        </div>
      </main>

      {toast && (
        <div className="toast-notification">
          <span className="toast-text">{toast}</span>
        </div>
      )}
    </div>
  );
}
