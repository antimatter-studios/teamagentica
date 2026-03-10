import { apiGet, apiPost, apiPut, apiDelete, apiGetText } from "./client";

export interface ConfigSchemaField {
  type: string;       // "string" | "select" | "number" | "boolean" | "text"
  label: string;
  required?: boolean;
  secret?: boolean;
  readonly?: boolean;
  default?: string;
  options?: string[];
  dynamic?: boolean;
  help_text?: string;
  visible_when?: {
    field: string;
    value: string;
  };
  order?: number;
}

export interface Plugin {
  id: string;
  name: string;
  version: string;
  image: string;
  status: string;
  capabilities: string[] | string;
  enabled: boolean;
  grpc_port: number;
  http_port: number;
  marketplace: string;
  config_schema: Record<string, ConfigSchemaField> | string | null;
  created_at: string;
  updated_at: string;
}

export interface PluginConfigEntry {
  key: string;
  value: string;
  is_secret: boolean;
}

interface PluginsResponse {
  plugins: Plugin[];
}

interface PluginResponse {
  plugin: Plugin;
}

interface ConfigResponse {
  config: PluginConfigEntry[];
}

/** Parse the config_schema into a typed map, sorted by order field.
 *  Handles both object (JSONRawString) and legacy string formats. */
export function parseConfigSchema(plugin: Plugin): Record<string, ConfigSchemaField> {
  if (!plugin.config_schema || plugin.config_schema === "null") return {};
  try {
    const raw: Record<string, ConfigSchemaField> =
      typeof plugin.config_schema === "string"
        ? JSON.parse(plugin.config_schema)
        : plugin.config_schema;
    const sorted = Object.entries(raw).sort(
      ([, a], [, b]) => (a.order ?? 50) - (b.order ?? 50)
    );
    return Object.fromEntries(sorted);
  } catch {
    return {};
  }
}

/** Parse the capabilities into an array. Handles both array and legacy string formats. */
export function parseCapabilities(plugin: Plugin): string[] {
  if (!plugin.capabilities) return [];
  if (Array.isArray(plugin.capabilities)) return plugin.capabilities;
  try {
    return JSON.parse(plugin.capabilities);
  } catch {
    return [];
  }
}

export async function listPlugins(): Promise<Plugin[]> {
  const res = await apiGet<PluginsResponse>("/api/plugins");
  return res.plugins || [];
}

export async function getPlugin(id: string): Promise<Plugin> {
  const res = await apiGet<PluginResponse>(`/api/plugins/${id}`);
  return res.plugin;
}

/** Fetch the live schema from a running plugin (proxied through kernel). */
export async function getPluginSchema(id: string): Promise<Record<string, unknown>> {
  return apiGet<Record<string, unknown>>(`/api/plugins/${id}/schema`);
}

/** Fetch the live config schema section from a running plugin. */
export async function getPluginConfigSchema(id: string): Promise<Record<string, ConfigSchemaField>> {
  try {
    const raw = await apiGet<Record<string, ConfigSchemaField>>(`/api/plugins/${id}/schema/config`);
    const sorted = Object.entries(raw).sort(
      ([, a], [, b]) => (a.order ?? 50) - (b.order ?? 50)
    );
    return Object.fromEntries(sorted);
  } catch {
    return {};
  }
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
  const res = await apiGet<ConfigResponse>(`/api/plugins/${id}/config`);
  return res.config || [];
}

export async function updatePluginConfig(
  id: string,
  config: Record<string, { value: string; is_secret: boolean }>
): Promise<void> {
  return apiPut(`/api/plugins/${id}/config`, { config });
}

export async function getPluginLogs(id: string, tail?: number): Promise<string> {
  const query = tail ? `?tail=${tail}` : "";
  return apiGetText(`/api/plugins/${id}/logs${query}`);
}

export async function getFieldOptions(
  pluginId: string,
  field: string
): Promise<{ options: string[]; error?: string; fallback?: boolean }> {
  return apiGet<{ options: string[]; error?: string; fallback?: boolean }>(
    `/api/route/${pluginId}/config/options/${field}`
  );
}

export async function searchPlugins(capability: string): Promise<Plugin[]> {
  const res = await apiGet<PluginsResponse>(`/api/plugins/search?capability=${encodeURIComponent(capability)}`);
  return res.plugins || [];
}

// --- Generic OAuth (routed to plugin via kernel proxy) ---

export interface OAuthStatus {
  authenticated: boolean;
  detail?: string;
}

export interface OAuthDeviceCode {
  url: string;
  code: string;
}

export interface OAuthPollResult {
  authenticated: boolean;
  error?: string;
}

export async function getOAuthStatus(pluginId: string): Promise<OAuthStatus> {
  return apiGet<OAuthStatus>(`/api/route/${pluginId}/auth/status`);
}

/**
 * Start the device code login flow. The plugin spawns the CLI which talks
 * to the OAuth provider and returns the URL + one-time code directly.
 */
export async function startOAuthFlow(pluginId: string): Promise<OAuthDeviceCode> {
  return apiPost<OAuthDeviceCode>(
    `/api/route/${pluginId}/auth/device-code`,
    {}
  );
}

export async function pollOAuthFlow(pluginId: string): Promise<OAuthPollResult> {
  return apiPost<OAuthPollResult>(`/api/route/${pluginId}/auth/poll`, {});
}

export async function oauthLogout(pluginId: string): Promise<void> {
  return apiDelete(`/api/route/${pluginId}/auth`);
}
