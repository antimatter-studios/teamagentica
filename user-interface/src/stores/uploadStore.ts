import { create } from "zustand";
import { API_BASE } from "../api/client";
import { formatBytes } from "@teamagentica/api-client";
import { useFileStore } from "./fileStore";

interface XHRRef { xhr: XMLHttpRequest | null; }

function uploadFileXHR(
  pluginId: string,
  key: string,
  file: File,
  onProgress: (e: { loaded: number; total: number }) => void,
  ref: XHRRef
): Promise<void> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    ref.xhr = xhr;
    const token = localStorage.getItem("teamagentica_token");
    xhr.open("PUT", `${API_BASE}/api/route/${pluginId}/objects/${encodeURIComponent(key)}`);
    xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
    if (token) xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress({ loaded: e.loaded, total: e.total }); };
    xhr.onload = () => { ref.xhr = null; if (xhr.status >= 200 && xhr.status < 300) resolve(); else reject(new Error(`Upload failed: ${xhr.status}`)); };
    xhr.onerror = () => { ref.xhr = null; reject(new Error("Upload network error")); };
    xhr.onabort = () => { ref.xhr = null; reject(new Error("Upload cancelled")); };
    xhr.send(file);
  });
}

type UploadStatus = "queued" | "uploading" | "done" | "error" | "cancelled";

export interface UploadItem {
  id: string;
  fileName: string;
  fileSize: number;
  pluginId: string;
  key: string;
  file: File;
  status: UploadStatus;
  loaded: number;
  total: number;
  speed: number;
  startedAt: number;
  error: string | null;
  _xhr: XHRRef;
}

interface UploadStore {
  items: UploadItem[];
  panelOpen: boolean;
  enqueue: (pluginId: string, prefix: string, files: FileList) => void;
  cancel: (id: string) => void;
  dismiss: (id: string) => void;
  clearCompleted: () => void;
  togglePanel: () => void;
}

const MAX_CONCURRENT = 2;
let idCounter = 0;

function processQueue() {
  const { items } = useUploadStore.getState();
  const active = items.filter((i) => i.status === "uploading").length;
  const toStart = items.filter((i) => i.status === "queued").slice(0, MAX_CONCURRENT - active);

  for (const item of toStart) {
    startUpload(item.id);
  }
}

function startUpload(id: string) {
  const items = useUploadStore.getState().items;
  const item = items.find((i) => i.id === id);
  if (!item || item.status !== "queued") return;

  // Mark uploading
  useUploadStore.setState((s) => ({
    items: s.items.map((i) =>
      i.id === id ? { ...i, status: "uploading" as const, startedAt: Date.now() } : i
    ),
  }));

  let lastLoaded = 0;
  let lastTime = Date.now();

  uploadFileXHR(
    item.pluginId,
    item.key,
    item.file,
    ({ loaded, total }) => {
      const now = Date.now();
      const dt = (now - lastTime) / 1000;
      const speed = dt > 0 ? (loaded - lastLoaded) / dt : 0;
      lastLoaded = loaded;
      lastTime = now;

      useUploadStore.setState((s) => ({
        items: s.items.map((i) =>
          i.id === id ? { ...i, loaded, total, speed } : i
        ),
      }));
    },
    item._xhr
  )
    .then(() => {
      useUploadStore.setState((s) => ({
        items: s.items.map((i) =>
          i.id === id ? { ...i, status: "done" as const, loaded: i.total } : i
        ),
      }));
      useFileStore.getState().refresh();
      processQueue();
    })
    .catch((err) => {
      const msg = err instanceof Error ? err.message : "Upload failed";
      const isCancelled = msg === "Upload cancelled";
      useUploadStore.setState((s) => ({
        items: s.items.map((i) =>
          i.id === id
            ? { ...i, status: isCancelled ? ("cancelled" as const) : ("error" as const), error: isCancelled ? null : msg }
            : i
        ),
      }));
      processQueue();
    });
}

export const useUploadStore = create<UploadStore>((set, get) => ({
  items: [],
  panelOpen: true,

  enqueue: (pluginId, prefix, fileList) => {
    const newItems: UploadItem[] = [];
    for (let i = 0; i < fileList.length; i++) {
      const file = fileList[i];
      newItems.push({
        id: `upload-${++idCounter}`,
        fileName: file.name,
        fileSize: file.size,
        pluginId,
        key: prefix + file.name,
        file,
        status: "queued",
        loaded: 0,
        total: file.size,
        speed: 0,
        startedAt: 0,
        error: null,
        _xhr: { xhr: null },
      });
    }
    set((s) => ({ items: [...s.items, ...newItems], panelOpen: true }));
    processQueue();
  },

  cancel: (id) => {
    const item = get().items.find((i) => i.id === id);
    if (!item) return;
    if (item.status === "uploading" && item._xhr.xhr) {
      item._xhr.xhr.abort();
    } else if (item.status === "queued") {
      set((s) => ({
        items: s.items.map((i) =>
          i.id === id ? { ...i, status: "cancelled" } : i
        ),
      }));
    }
  },

  dismiss: (id) => {
    set((s) => ({ items: s.items.filter((i) => i.id !== id) }));
  },

  clearCompleted: () => {
    set((s) => ({
      items: s.items.filter((i) => i.status === "uploading" || i.status === "queued"),
    }));
  },

  togglePanel: () => set((s) => ({ panelOpen: !s.panelOpen })),
}));

export { formatBytes };
