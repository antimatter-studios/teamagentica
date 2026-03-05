import { create } from "zustand";
import {
  browseMarketplace,
  installFromMarketplace,
  type MarketplacePlugin,
} from "../api/marketplace";

interface MarketplaceStore {
  catalog: MarketplacePlugin[];
  loading: boolean;
  error: string | null;
  query: string;
  setQuery: (q: string) => void;
  fetch: () => Promise<void>;
  install: (pluginId: string) => Promise<void>;
}

export const useMarketplaceStore = create<MarketplaceStore>((set, get) => ({
  catalog: [],
  loading: false,
  error: null,
  query: "",

  setQuery: (q) => set({ query: q }),

  fetch: async () => {
    set({ loading: true, error: null });
    try {
      const q = get().query;
      const catalog = await browseMarketplace(q || undefined);
      set({ catalog, loading: false });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Failed to browse marketplace",
      });
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
