import { create } from "zustand";
import {
  browseMarketplace,
  installFromMarketplace,
  listProviders,
  type MarketplacePlugin,
  type MarketplaceGroup,
  type MarketplaceProvider,
} from "../api/marketplace";

interface MarketplaceStore {
  catalog: MarketplacePlugin[];
  groups: MarketplaceGroup[];
  providers: MarketplaceProvider[];
  loading: boolean;
  error: string | null;
  query: string;
  selectedProvider: string | null;
  setQuery: (q: string) => void;
  setSelectedProvider: (name: string | null) => void;
  fetch: () => Promise<void>;
  fetchProviders: () => Promise<void>;
  install: (pluginId: string) => Promise<void>;
}

export const useMarketplaceStore = create<MarketplaceStore>((set, get) => ({
  catalog: [],
  groups: [],
  providers: [],
  loading: false,
  error: null,
  query: "",
  selectedProvider: null,

  setQuery: (q) => set({ query: q }),
  setSelectedProvider: (name) => set({ selectedProvider: name }),

  fetch: async () => {
    set({ loading: true, error: null });
    try {
      const q = get().query;
      const { plugins, groups } = await browseMarketplace(q || undefined);
      set({ catalog: plugins, groups, loading: false });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Failed to browse marketplace",
      });
    }
  },

  fetchProviders: async () => {
    try {
      const providers = await listProviders();
      set({ providers });
    } catch {
      // non-critical — sidebar just won't show providers
    }
  },

  install: async (pluginId) => {
    try {
      await installFromMarketplace(pluginId);
      await get().fetch();
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to install plugin" });
    }
  },
}));
