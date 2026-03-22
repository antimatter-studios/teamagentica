import type { HttpTransport } from "./client.js";
import type { Plugin } from "./plugins.js";

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

export class FilesAPI {
  private http: HttpTransport;
  private searchPlugins: (capability: string) => Promise<Plugin[]>;
  constructor(http: HttpTransport, searchPlugins: (capability: string) => Promise<Plugin[]>) {
    this.http = http;
    this.searchPlugins = searchPlugins;
  }

  async fetchStorageProviders(): Promise<Plugin[]> {
    return this.searchPlugins("storage:");
  }

  async browse(pluginId: string, prefix: string): Promise<BrowseResult> {
    return this.http.get<BrowseResult>(
      `/api/route/${pluginId}/browse?prefix=${encodeURIComponent(prefix)}`
    );
  }

  async upload(
    pluginId: string,
    key: string,
    body: BodyInit,
    contentType: string = "application/octet-stream"
  ): Promise<void> {
    return this.http.putRaw(
      `/api/route/${pluginId}/objects/${encodeURIComponent(key)}`,
      body,
      contentType
    );
  }

  async delete(pluginId: string, key: string): Promise<void> {
    return this.http.deleteRaw(
      `/api/route/${pluginId}/objects/${encodeURIComponent(key)}`
    );
  }

  async fetchBlob(pluginId: string, key: string): Promise<Blob> {
    const res = await this.http.getRaw(
      `/api/route/${pluginId}/objects/${encodeURIComponent(key)}`
    );
    return res.blob();
  }

  async copy(pluginId: string, source: string, destination: string): Promise<void> {
    await this.http.post(
      `/api/route/${pluginId}/objects/copy`,
      { source, destination }
    );
  }

  async move(pluginId: string, source: string, destination: string): Promise<void> {
    await this.http.post(
      `/api/route/${pluginId}/objects/move`,
      { source, destination }
    );
  }

  async fetchZip(pluginId: string, prefix: string): Promise<Blob> {
    const res = await this.http.getRaw(
      `/api/route/${pluginId}/download/zip?prefix=${encodeURIComponent(prefix)}`
    );
    return res.blob();
  }

  async fetchText(pluginId: string, key: string): Promise<string> {
    const res = await this.http.getRaw(
      `/api/route/${pluginId}/objects/${encodeURIComponent(key)}`
    );
    return res.text();
  }
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
