import { create } from "zustand";
import { apiClient } from "../api/client";
import { filenameFromKey } from "@teamagentica/api-client";
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
  viewingFile: StorageFile | null;
  loadProviders: () => Promise<void>;
  selectProvider: (p: Plugin) => void;
  browse: (prefix: string) => Promise<void>;
  navigateUp: () => void;
  upload: (files: FileList) => Promise<void>;
  deleteFile: (key: string) => Promise<void>;
  refresh: () => Promise<void>;
  selectFile: (file: StorageFile | null) => void;
  viewFile: (file: StorageFile | null) => void;
  duplicateFile: (key: string) => Promise<void>;
  renameFile: (key: string, newName: string) => Promise<void>;
  // File operations staging
  copyItems: string[];
  moveItems: string[];
  addCopyItem: (key: string) => void;
  addMoveItem: (key: string) => void;
  removeCopyItem: (key: string) => void;
  removeMoveItem: (key: string) => void;
  clearOps: () => void;
  pasteCopyItem: (key: string) => Promise<void>;
  pasteMoveItem: (key: string) => Promise<void>;
  createFolder: (name: string) => Promise<void>;
  createFile: (name: string) => Promise<void>;
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
  viewingFile: null,

  selectFile: (file) => set({ selectedFile: file }),
  viewFile: (file) => set({ viewingFile: file }),

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
    set({ selectedProvider: p, prefix: "", folders: [], files: [], selectedFile: null, viewingFile: null });
    get().browse("");
  },

  browse: async (prefix: string) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    set({ loading: true, error: null, prefix, selectedFile: null, viewingFile: null });
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

  duplicateFile: async (key: string) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    set({ error: null });
    try {
      const isFolder = key.endsWith("/");
      // For folders: "myfolder/" → name "myfolder", trailing "/"
      // For files: "file.txt" → name "file", ext ".txt"
      const trimmed = isFolder ? key.slice(0, -1) : key;
      const name = trimmed.split("/").pop()!;
      const parentPrefix = trimmed.slice(0, trimmed.length - name.length);

      const dotIdx = isFolder ? -1 : name.lastIndexOf(".");
      const baseName = dotIdx > 0 ? name.slice(0, dotIdx) : name;
      const ext = dotIdx > 0 ? name.slice(dotIdx) : "";
      const suffix = isFolder ? "/" : "";

      // Find next available "Copy N" number.
      const existingNames = new Set([
        ...get().files.map((f) => filenameFromKey(f.key)),
        ...get().folders.map((f) => f.replace(/\/$/, "").split("/").pop()!),
      ]);
      let n = 1;
      let destName: string;
      do {
        destName = `${baseName} Copy ${n}${ext}`;
        n++;
      } while (existingNames.has(destName));

      await apiClient.files.copy(provider.id, key, parentPrefix + destName + suffix);
      await get().browse(get().prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Duplicate failed" });
    }
  },

  renameFile: async (key: string, newName: string) => {
    const provider = get().selectedProvider;
    if (!provider) return;
    set({ error: null });
    try {
      const isFolder = key.endsWith("/");
      const trimmed = isFolder ? key.slice(0, -1) : key;
      const oldName = trimmed.split("/").pop()!;
      if (newName === oldName) return; // no change
      const parentPrefix = trimmed.slice(0, trimmed.length - oldName.length);
      const suffix = isFolder ? "/" : "";
      await apiClient.files.move(provider.id, key, parentPrefix + newName + suffix);
      await get().browse(get().prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Rename failed" });
    }
  },

  // File operations staging
  copyItems: [],
  moveItems: [],

  addCopyItem: (key) => set((s) => ({
    copyItems: s.copyItems.includes(key) ? s.copyItems : [...s.copyItems, key],
    moveItems: s.moveItems.filter((k) => k !== key),
    selectedFile: null,
  })),

  addMoveItem: (key) => set((s) => ({
    moveItems: s.moveItems.includes(key) ? s.moveItems : [...s.moveItems, key],
    copyItems: s.copyItems.filter((k) => k !== key),
    selectedFile: null,
  })),

  removeCopyItem: (key) => set((s) => ({
    copyItems: s.copyItems.filter((k) => k !== key),
  })),

  removeMoveItem: (key) => set((s) => ({
    moveItems: s.moveItems.filter((k) => k !== key),
  })),

  clearOps: () => set({ copyItems: [], moveItems: [] }),

  pasteCopyItem: async (key: string) => {
    const { selectedProvider, prefix } = get();
    if (!selectedProvider) return;
    set({ error: null });
    try {
      await apiClient.files.copy(selectedProvider.id, key, prefix + filenameFromKey(key));
      set((s) => ({ copyItems: s.copyItems.filter((k) => k !== key) }));
      await get().browse(prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Paste failed" });
    }
  },

  pasteMoveItem: async (key: string) => {
    const { selectedProvider, prefix } = get();
    if (!selectedProvider) return;
    set({ error: null });
    try {
      const dest = prefix + filenameFromKey(key);
      await apiClient.files.move(selectedProvider.id, key, dest);

      // Rewrite stale child paths in both queues.
      // If we moved "folder/" to "newplace/folder/", any queued item like
      // "folder/child.txt" becomes "newplace/folder/child.txt".
      const rewriteChildren = (items: string[]) =>
        items
          .filter((k) => k !== key)
          .map((k) => k.startsWith(key) ? dest + k.slice(key.length) : k);

      set((s) => ({
        moveItems: rewriteChildren(s.moveItems),
        copyItems: rewriteChildren(s.copyItems),
      }));
      await get().browse(prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Move failed" });
    }
  },

  createFolder: async (name: string) => {
    const provider = get().selectedProvider;
    if (!provider || !name.trim()) return;
    set({ error: null });
    try {
      const key = get().prefix + name.trim() + "/";
      await apiClient.files.upload(provider.id, key, new Blob([]), "application/x-directory");
      await get().browse(get().prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Create folder failed" });
    }
  },

  createFile: async (name: string) => {
    const provider = get().selectedProvider;
    if (!provider || !name.trim()) return;
    set({ error: null });
    try {
      const key = get().prefix + name.trim();
      await apiClient.files.upload(provider.id, key, new Blob([]), "text/plain");
      await get().browse(get().prefix);
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Create file failed" });
    }
  },

  refresh: async () => {
    await get().browse(get().prefix);
  },
}));
