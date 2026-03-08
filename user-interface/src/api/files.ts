import { apiGet, API_BASE } from "./client";
import { searchPlugins, type Plugin } from "./plugins";

export interface StorageFile {
  key: string;
  size: number;
  content_type: string;
  last_modified: string;
  etag: string;
}

export interface BrowseResult {
  prefix: string;
  folders: string[];
  files: StorageFile[];
}

export async function fetchStorageProviders(): Promise<Plugin[]> {
  return searchPlugins("storage:");
}

export async function browseStorage(pluginId: string, prefix: string): Promise<BrowseResult> {
  return apiGet<BrowseResult>(`/api/route/${pluginId}/browse?prefix=${encodeURIComponent(prefix)}`);
}

export async function uploadFile(pluginId: string, key: string, file: File): Promise<void> {
  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {
    "Content-Type": file.type || "application/octet-stream",
  };
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/route/${pluginId}/objects/${encodeURIComponent(key)}`, {
    method: "PUT",
    headers,
    body: file,
  });
  if (!res.ok) throw new Error(`Upload failed: ${res.status}`);
}

export interface XHRRef {
  xhr: XMLHttpRequest | null;
}

export function uploadFileXHR(
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

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress({ loaded: e.loaded, total: e.total });
    };
    xhr.onload = () => {
      ref.xhr = null;
      if (xhr.status >= 200 && xhr.status < 300) resolve();
      else reject(new Error(`Upload failed: ${xhr.status}`));
    };
    xhr.onerror = () => {
      ref.xhr = null;
      reject(new Error("Upload network error"));
    };
    xhr.onabort = () => {
      ref.xhr = null;
      reject(new Error("Upload cancelled"));
    };

    xhr.send(file);
  });
}

export async function deleteFile(pluginId: string, key: string): Promise<void> {
  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/route/${pluginId}/objects/${encodeURIComponent(key)}`, {
    method: "DELETE",
    headers,
  });
  if (!res.ok) throw new Error(`Delete failed: ${res.status}`);
}

export async function downloadFile(pluginId: string, key: string): Promise<void> {
  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/route/${pluginId}/objects/${encodeURIComponent(key)}`, {
    method: "GET",
    headers,
  });
  if (!res.ok) throw new Error(`Download failed: ${res.status}`);

  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filenameFromKey(key);
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

export async function fetchObjectBlob(pluginId: string, key: string): Promise<Blob> {
  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/route/${pluginId}/objects/${encodeURIComponent(key)}`, {
    method: "GET",
    headers,
  });
  if (!res.ok) throw new Error(`Fetch failed: ${res.status}`);
  return res.blob();
}

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 1)} ${sizes[i]}`;
}

export function filenameFromKey(key: string): string {
  const parts = key.split("/");
  return parts[parts.length - 1] || key;
}

export function folderName(folder: string): string {
  const trimmed = folder.endsWith("/") ? folder.slice(0, -1) : folder;
  const parts = trimmed.split("/");
  return parts[parts.length - 1] || folder;
}
