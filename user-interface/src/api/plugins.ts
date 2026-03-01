import { apiGet, apiPost, apiPut, apiDelete, apiGetText } from "./client";

export interface Plugin {
  id: string;
  name: string;
  version: string;
  image: string;
  status: string;
  capabilities: string[];
  enabled: boolean;
  grpc_port: number;
  http_port: number;
  marketplace: string;
  config_schema: Record<string, { type: string; required: boolean; secret: boolean }>;
  created_at: string;
  updated_at: string;
}

export interface PluginConfigEntry {
  key: string;
  value: string;
  is_secret: boolean;
}

export async function listPlugins(): Promise<Plugin[]> {
  return apiGet<Plugin[]>("/api/plugins");
}

export async function getPlugin(id: string): Promise<Plugin> {
  return apiGet<Plugin>(`/api/plugins/${id}`);
}

export async function installPlugin(plugin: Partial<Plugin>): Promise<Plugin> {
  return apiPost<Plugin>("/api/plugins", plugin);
}

export async function uninstallPlugin(id: string): Promise<void> {
  return apiDelete(`/api/plugins/${id}`);
}

export async function enablePlugin(id: string): Promise<void> {
  return apiPost(`/api/plugins/${id}/enable`, {});
}

export async function disablePlugin(id: string): Promise<void> {
  return apiPost(`/api/plugins/${id}/disable`, {});
}

export async function restartPlugin(id: string): Promise<void> {
  return apiPost(`/api/plugins/${id}/restart`, {});
}

export async function getPluginConfig(id: string): Promise<PluginConfigEntry[]> {
  return apiGet<PluginConfigEntry[]>(`/api/plugins/${id}/config`);
}

export async function updatePluginConfig(
  id: string,
  config: Record<string, { value: string; is_secret: boolean }>
): Promise<void> {
  return apiPut(`/api/plugins/${id}/config`, config);
}

export async function getPluginLogs(id: string, tail?: number): Promise<string> {
  const query = tail ? `?tail=${tail}` : "";
  return apiGetText(`/api/plugins/${id}/logs${query}`);
}

export async function searchPlugins(capability: string): Promise<Plugin[]> {
  return apiGet<Plugin[]>(`/api/plugins/search?capability=${encodeURIComponent(capability)}`);
}
