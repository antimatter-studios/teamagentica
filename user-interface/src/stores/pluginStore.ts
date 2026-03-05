import { create } from "zustand";
import {
  listPlugins,
  enablePlugin as apiEnable,
  disablePlugin as apiDisable,
  restartPlugin as apiRestart,
  uninstallPlugin as apiUninstall,
  type Plugin,
} from "../api/plugins";

interface PluginStore {
  plugins: Plugin[];
  loading: boolean;
  error: string | null;
  fetch: () => Promise<void>;
  enable: (id: string) => Promise<void>;
  disable: (id: string) => Promise<void>;
  restart: (id: string) => Promise<void>;
  uninstall: (id: string) => Promise<void>;
}

export const usePluginStore = create<PluginStore>((set, get) => ({
  plugins: [],
  loading: false,
  error: null,

  fetch: async () => {
    // Only show loading spinner on first load
    if (get().plugins.length === 0) {
      set({ loading: true });
    }
    try {
      const plugins = await listPlugins();
      set({ plugins, loading: false, error: null });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Failed to fetch plugins",
      });
    }
  },

  enable: async (id) => {
    set((state) => ({
      plugins: state.plugins.map((p) =>
        p.id === id ? { ...p, enabled: true, status: "starting" } : p
      ),
    }));
    try {
      await apiEnable(id);
      await get().fetch();
    } catch (err) {
      await get().fetch();
      set({ error: err instanceof Error ? err.message : "Failed to enable plugin" });
    }
  },

  disable: async (id) => {
    set((state) => ({
      plugins: state.plugins.map((p) =>
        p.id === id ? { ...p, enabled: false, status: "stopped" } : p
      ),
    }));
    try {
      await apiDisable(id);
      await get().fetch();
    } catch (err) {
      await get().fetch();
      set({ error: err instanceof Error ? err.message : "Failed to disable plugin" });
    }
  },

  restart: async (id) => {
    set((state) => ({
      plugins: state.plugins.map((p) =>
        p.id === id ? { ...p, status: "starting" } : p
      ),
    }));
    try {
      await apiRestart(id);
      await get().fetch();
    } catch (err) {
      await get().fetch();
      set({ error: err instanceof Error ? err.message : "Failed to restart plugin" });
    }
  },

  uninstall: async (id) => {
    set((state) => ({
      plugins: state.plugins.filter((p) => p.id !== id),
    }));
    try {
      await apiUninstall(id);
      await get().fetch();
    } catch (err) {
      await get().fetch();
      set({ error: err instanceof Error ? err.message : "Failed to uninstall plugin" });
    }
  },
}));
