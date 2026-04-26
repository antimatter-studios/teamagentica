import { useEffect, useState, useCallback, useRef, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import { Loader2, Search, Inbox } from "lucide-react";
import { useMarketplaceStore } from "../stores/marketplaceStore";
import { usePluginStore } from "../stores/pluginStore";
import { apiClient } from "../api/client";
import type { MarketplacePlugin, MarketplaceGroup } from "@teamagentica/api-client";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";

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

  const installedVersions = useMemo(
    () => new Map(plugins.map((p) => [p.id, p.version])),
    [plugins]
  );

  const [searchInput, setSearchInput] = useState("");
  const [installing, setInstalling] = useState<string | null>(null);
  const [upgrading, setUpgrading] = useState<string | null>(null);
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
        entries.sort((a, b) => a.name.localeCompare(b.name));
        sections.push({ group: gm, plugins: entries });
        byGroup.delete(gm.id);
      }
    }
    // Any remaining groups not in the metadata
    for (const [id, entries] of byGroup) {
      entries.sort((a, b) => a.name.localeCompare(b.name));
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

  async function handleUpgrade(pluginId: string) {
    setUpgrading(pluginId);
    try {
      await apiClient.marketplace.upgrade(pluginId);
      await Promise.all([fetch(), fetchPlugins()]);
      showToast("Plugin upgraded successfully");
    } catch (err) {
      showToast(err instanceof Error ? err.message : "Upgrade failed");
    } finally {
      setUpgrading(null);
    }
  }

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    setQuery(searchInput);
  }

  const totalFiltered = providerFiltered.length;

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center gap-3 py-16">
        <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
        <p className="text-sm text-muted-foreground">SCANNING MARKETPLACE...</p>
      </div>
    );
  }

  return (
    <div className="grid grid-cols-[260px_1fr] gap-4 h-full">
      {/* Left sidebar: groups */}
      <aside className="flex flex-col gap-1 border-r pr-3">
        <div className="flex items-center gap-2 px-2 py-2 text-xs font-semibold tracking-wide text-muted-foreground">
          <span>CATEGORIES</span>
          {sortedGroups.length > 0 && (
            <Badge variant="secondary" className="ml-auto">{sortedGroups.length}</Badge>
          )}
        </div>

        <nav className="flex flex-col gap-0.5">
          <button
            type="button"
            className={cn(
              "flex items-center justify-between rounded-md px-2 py-1.5 text-sm hover:bg-accent",
              selectedGroup === null && "bg-accent text-accent-foreground"
            )}
            onClick={() => setSelectedGroup(null)}
          >
            <span>All Plugins</span>
            <Badge variant="outline">{totalFiltered}</Badge>
          </button>

          {sortedGroups.map((g) => {
            const count = groupCounts.get(g.id) || 0;
            if (count === 0) return null;
            return (
              <button
                type="button"
                key={g.id}
                className={cn(
                  "flex items-center justify-between rounded-md px-2 py-1.5 text-sm hover:bg-accent",
                  selectedGroup === g.id && "bg-accent text-accent-foreground"
                )}
                onClick={() => setSelectedGroup(g.id)}
                title={g.description}
              >
                <span>{g.name}</span>
                <Badge variant="outline">{count}</Badge>
              </button>
            );
          })}
        </nav>

        {/* Provider filter (collapsed under categories) */}
        {providers.length > 1 && (
          <>
            <Separator className="my-3" />
            <div className="flex items-center gap-2 px-2 py-2 text-xs font-semibold tracking-wide text-muted-foreground">
              <span>PROVIDERS</span>
            </div>
            <nav className="flex flex-col gap-0.5">
              <button
                type="button"
                className={cn(
                  "flex items-center justify-between rounded-md px-2 py-1.5 text-sm hover:bg-accent",
                  selectedProvider === null && "bg-accent text-accent-foreground"
                )}
                onClick={() => setSelectedProvider(null)}
              >
                <span>All</span>
              </button>
              {providers.map((pr) => (
                <button
                  type="button"
                  key={pr.id}
                  className={cn(
                    "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent",
                    selectedProvider === pr.name && "bg-accent text-accent-foreground"
                  )}
                  onClick={() => setSelectedProvider(pr.name)}
                >
                  <span
                    className={cn(
                      "inline-block h-2 w-2 rounded-full",
                      pr.enabled ? "bg-green-500" : "bg-muted-foreground"
                    )}
                  />
                  <span>{pr.name}</span>
                </button>
              ))}
            </nav>
          </>
        )}
      </aside>

      {/* Right content: grouped plugin list */}
      <main className="flex flex-col gap-4 min-w-0">
        <div className="flex items-center justify-between gap-3 flex-wrap">
          <h2 className="flex items-center gap-2 text-lg font-semibold tracking-wide">
            {selectedGroup
              ? sortedGroups.find((g) => g.id === selectedGroup)?.name || selectedGroup
              : "MARKETPLACE"}
            {totalFiltered > 0 && (
              <Badge variant="secondary">
                {selectedGroup ? (groupCounts.get(selectedGroup) || 0) : totalFiltered}
              </Badge>
            )}
          </h2>

          <form className="flex items-center gap-2" onSubmit={handleSearch}>
            <Input
              type="text"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              placeholder="Search plugins..."
              className="w-64"
            />
            <Button type="submit" variant="outline" size="sm">
              <Search className="h-4 w-4" />
              SEARCH
            </Button>
            {query && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setSearchInput("");
                  setQuery("");
                }}
              >
                CLEAR
              </Button>
            )}
          </form>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {groupedPlugins.length === 0 && !error && (
          <div className="flex flex-col items-center justify-center gap-2 py-16 text-center">
            <Inbox className="h-10 w-10 text-muted-foreground opacity-60" />
            <p className="text-sm">
              {query
                ? "No plugins match your search."
                : "No plugins available."}
            </p>
            <p className="text-xs text-muted-foreground">
              {query
                ? "Try a different search term."
                : "Add a marketplace provider to browse plugins."}
            </p>
          </div>
        )}

        <div className="flex flex-col gap-6">
          {groupedPlugins.map(({ group, plugins: groupPlugins }) => (
            <div key={group.id} className="flex flex-col gap-2">
              {selectedGroup === null && (
                <div className="flex items-baseline gap-2">
                  <span className="text-sm font-semibold tracking-wide">{group.name}</span>
                  {group.description && (
                    <span className="text-xs text-muted-foreground">{group.description}</span>
                  )}
                  <Badge variant="outline" className="ml-auto">{groupPlugins.length}</Badge>
                </div>
              )}

              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
                {groupPlugins.map((p) => {
                  const isInstalled = installedIds.has(p.plugin_id);
                  const isInstalling = installing === p.plugin_id;
                  const isUpgrading = upgrading === p.plugin_id;
                  const installedVersion = installedVersions.get(p.plugin_id);
                  const normalizeVersion = (v: string | undefined) => v?.replace(/^v/i, "").trim() ?? "";
                  const parseSemver = (v: string) => {
                    const parts = v.split(".").map(Number);
                    return [parts[0] || 0, parts[1] || 0, parts[2] || 0] as const;
                  };
                  const nInstalled = normalizeVersion(installedVersion);
                  const nCatalog = normalizeVersion(p.version);
                  const hasUpdate = isInstalled && nCatalog !== "" && nInstalled !== "" && (() => {
                    const [a, b, c] = parseSemver(nInstalled);
                    const [x, y, z] = parseSemver(nCatalog);
                    return x > a || (x === a && y > b) || (x === a && y === b && z > c);
                  })();

                  return (
                    <Card
                      key={`${p.provider}-${p.plugin_id}`}
                      className={cn(hasUpdate && "border-amber-500/50")}
                    >
                      <CardContent className="flex flex-col gap-2 p-4">
                        <div className="flex items-start justify-between gap-2">
                          <div className="flex flex-col min-w-0">
                            <span className="font-semibold truncate">{p.name}</span>
                            {p.author && (
                              <span className="text-xs text-muted-foreground">
                                by {p.author}
                              </span>
                            )}
                          </div>
                          <Badge variant="outline">v{p.version}</Badge>
                        </div>

                        {p.description && (
                          <p className="text-sm text-muted-foreground line-clamp-2">{p.description}</p>
                        )}

                        {p.tags && p.tags.length > 0 && (
                          <div className="flex flex-wrap gap-1">
                            {p.tags.map((tag) => (
                              <Badge variant="secondary" key={tag}>
                                {tag}
                              </Badge>
                            ))}
                          </div>
                        )}

                        <div className="mt-2">
                          {hasUpdate ? (
                            <Button
                              variant="default"
                              size="sm"
                              className="w-full"
                              onClick={() => !isUpgrading && handleUpgrade(p.plugin_id)}
                              disabled={isUpgrading}
                            >
                              {isUpgrading ? "UPGRADING..." : `UPGRADE TO v${p.version}?`}
                            </Button>
                          ) : isInstalled ? (
                            <Badge variant="secondary" className="w-full justify-center py-1.5">
                              INSTALLED
                            </Badge>
                          ) : (
                            <Button
                              variant="default"
                              size="sm"
                              className="w-full"
                              onClick={() => handleInstall(p.plugin_id)}
                              disabled={isInstalling}
                            >
                              {isInstalling ? (
                                <>
                                  <Loader2 className="h-4 w-4 animate-spin" />
                                  INSTALLING...
                                </>
                              ) : (
                                "INSTALL"
                              )}
                            </Button>
                          )}
                        </div>
                      </CardContent>
                    </Card>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </main>

      {toast && (
        <div className="fixed bottom-6 right-6 z-50 rounded-md border bg-background px-4 py-2 shadow-lg">
          <span className="text-sm">{toast}</span>
        </div>
      )}
    </div>
  );
}
