import { create } from "zustand";
import { apiClient } from "../api/client";
import type {
  Environment,
  Workspace,
  Disk,
  WorkspaceOptions,
  WorkspaceOptionsUpdate,
} from "@teamagentica/api-client";

interface WorkspaceStore {
  workspaces: Workspace[];
  environments: Environment[];
  disks: Disk[];
  loading: boolean;
  error: string | null;

  // Options for the currently selected workspace.
  selectedId: string | null;
  options: WorkspaceOptions | null;
  optionsLoading: boolean;
  optionsDirty: boolean;

  fetch: () => Promise<void>;
  selectWorkspace: (id: string | null) => void;
  fetchOptions: (id: string) => Promise<void>;
  updateOptions: (id: string, opts: WorkspaceOptionsUpdate) => Promise<void>;
  restartWorkspace: (id: string) => Promise<void>;
}

export const useWorkspaceStore = create<WorkspaceStore>((set, get) => ({
  workspaces: [],
  environments: [],
  disks: [],
  loading: false,
  error: null,
  selectedId: null,
  options: null,
  optionsLoading: false,
  optionsDirty: false,

  fetch: async () => {
    if (get().workspaces.length === 0) set({ loading: true });
    try {
      const [envs, wss, vols] = await Promise.all([
        apiClient.workspaces.listEnvironments(),
        apiClient.workspaces.listWorkspaces(),
        apiClient.workspaces.listDisks(),
      ]);
      set({ environments: envs, workspaces: wss, disks: vols, loading: false, error: null });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Failed to load" });
    }
  },

  selectWorkspace: (id) => {
    set({ selectedId: id, options: null, optionsDirty: false });
    if (id) get().fetchOptions(id);
  },

  fetchOptions: async (id) => {
    set({ optionsLoading: true });
    try {
      const opts = await apiClient.workspaces.getWorkspaceOptions(id);
      set({ options: opts, optionsLoading: false, optionsDirty: false });
    } catch {
      set({ options: null, optionsLoading: false });
    }
  },

  updateOptions: async (id, opts) => {
    try {
      const updated = await apiClient.workspaces.updateWorkspaceOptions(id, opts);
      set({ options: updated, optionsDirty: true });
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to save options" });
    }
  },

  restartWorkspace: async (id) => {
    try {
      await apiClient.workspaces.restartWorkspace(id);
      set({ optionsDirty: false });
      await get().fetch();
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to restart" });
    }
  },
}));
