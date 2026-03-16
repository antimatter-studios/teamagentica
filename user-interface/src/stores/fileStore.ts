import { create } from "zustand";
import { apiClient } from "../api/client";
import type { Plugin, StorageFile } from "@teamagentica/api-client";

interface FileStore {
  providers: Plugin[];
  selectedProvider: Plugin | null;
  prefix: string;
  folders: string[];
  files: StorageFile[];
  loading: boolean;
  error: string | null;
  selectedFile: StorageFile | null;
  loadProviders: () => Promise<void>;
  selectProvider: (p: Plugin) => void;
  browse: (prefix: string) => Promise<void>;
  navigateUp: () => void;
  upload: (files: FileList) => Promise<void>;
  deleteFile: (key: string) => Promise<void>;
  refresh: () => Promise<void>;
  selectFile: (file: StorageFile | null) => void;
}

export const useFileStore = create<FileStore>((set, get) => ({
  providers: [],
  selectedProvider: null,
  prefix: "",
  folders: [],
  files: [],
  loading: false,
  error: null,
  selectedFile: null,

  selectFile: (file) => set({ selectedFile: file }),

  loadProviders: async () => {
    try {
      const providers = await apiClient.files.fetchStorageProviders();
      set({ providers });
      if (providers.length > 0 && !get().selectedProvider) {
        get().selectProvider(providers[0]);
      }
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to load storage providers" });
    }
  },

  selectProvider: (p: Plugin) => {
    set({ selectedProvider: p, prefix: "", folders: [], files: [], selectedFile: null });
    get().browse("");
  },

  browse: async (prefix: string) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    set({ loading: true, error: null, prefix, selectedFile: null });
    try {
      const result = await apiClient.files.browse(provider.id, prefix);
      set({ folders: result.folders || [], files: result.files || [], loading: false });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Browse failed" });
    }
  },

  navigateUp: () => {
    const { prefix } = get();
    if (!prefix) return;
    const parts = prefix.replace(/\/$/, "").split("/");
    parts.pop();
    const parent = parts.length > 0 ? parts.join("/") + "/" : "";
    get().browse(parent);
  },

  upload: async (fileList: FileList) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    const { prefix } = get();
    set({ error: null });
    try {
      for (let i = 0; i < fileList.length; i++) {
        const file = fileList[i];
        const key = prefix + file.name;
        await apiClient.files.upload(provider.id, key, file, file.type || "application/octet-stream");
      }
      await get().browse(prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Upload failed" });
    }
  },

  deleteFile: async (key: string) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    set({ error: null });
    try {
      await apiClient.files.delete(provider.id, key);
      await get().browse(get().prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Delete failed" });
    }
  },

  refresh: async () => {
    await get().browse(get().prefix);
  },
}));
