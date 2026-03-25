import type { HttpTransport } from "./client.js";

export interface ConfigSchemaField {
  type: string;
  label: string;
  required?: boolean;
  secret?: boolean;
  readonly?: boolean;
  default?: string;
  options?: (string | { label: string; value: string })[];
  dynamic?: boolean;
  help_text?: string;
  visible_when?: { field: string; value: string };
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

export function parseConfigSchema(
  plugin: Plugin
): Record<string, ConfigSchemaField> {
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

export function parseCapabilities(plugin: Plugin): string[] {
  if (!plugin.capabilities) return [];
  if (Array.isArray(plugin.capabilities)) return plugin.capabilities;
  try {
    return JSON.parse(plugin.capabilities);
  } catch {
    return [];
  }
}

export class PluginsAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(): Promise<Plugin[]> {
    const res = await this.http.get<{ plugins: Plugin[] }>("/api/plugins");
    return res.plugins || [];
  }

  async get(id: string): Promise<Plugin> {
    const res = await this.http.get<{ plugin: Plugin }>(`/api/plugins/${id}`);
    return res.plugin;
  }

  async getSchema(id: string): Promise<Record<string, unknown>> {
    return this.http.get<Record<string, unknown>>(`/api/plugins/${id}/schema`);
  }

  async getConfigSchema(
    id: string
  ): Promise<Record<string, ConfigSchemaField>> {
    try {
      const raw = await this.http.get<Record<string, ConfigSchemaField>>(
        `/api/plugins/${id}/schema/config`
      );
      const sorted = Object.entries(raw).sort(
        ([, a], [, b]) => (a.order ?? 50) - (b.order ?? 50)
      );
      return Object.fromEntries(sorted);
    } catch {
      return {};
    }
  }

  async install(plugin: Partial<Plugin>): Promise<Plugin> {
    return this.http.post<Plugin>("/api/plugins", plugin);
  }

  async uninstall(id: string): Promise<void> {
    return this.http.delete(`/api/plugins/${id}`);
  }

  async enable(id: string): Promise<void> {
    return this.http.post(`/api/plugins/${id}/enable`, {});
  }

  async disable(id: string): Promise<void> {
    return this.http.post(`/api/plugins/${id}/disable`, {});
  }

  async restart(id: string): Promise<void> {
    return this.http.post(`/api/plugins/${id}/restart`, {});
  }

  async getConfig(id: string): Promise<PluginConfigEntry[]> {
    const res = await this.http.get<{ config: PluginConfigEntry[] }>(
      `/api/plugins/${id}/config`
    );
    return res.config || [];
  }

  async updateConfig(
    id: string,
    config: Record<string, { value: string; is_secret: boolean }>
  ): Promise<void> {
    return this.http.put(`/api/plugins/${id}/config`, { config });
  }

  async getLogs(id: string, tail?: number): Promise<string> {
    const query = tail ? `?tail=${tail}` : "";
    return this.http.getText(`/api/plugins/${id}/logs${query}`);
  }

  async getFieldOptions(
    pluginId: string,
    field: string
  ): Promise<{ options: (string | { label: string; value: string })[]; error?: string; fallback?: boolean }> {
    return this.http.get(
      `/api/route/${pluginId}/config/options/${field}`
    );
  }

  async search(capability: string): Promise<Plugin[]> {
    const res = await this.http.get<{ plugins: Plugin[] }>(
      `/api/plugins/search?capability=${encodeURIComponent(capability)}`
    );
    return res.plugins || [];
  }

  // --- OAuth ---

  async getOAuthStatus(pluginId: string): Promise<OAuthStatus> {
    return this.http.get(`/api/route/${pluginId}/auth/status`);
  }

  async startOAuthFlow(pluginId: string): Promise<OAuthDeviceCode> {
    return this.http.post(`/api/route/${pluginId}/auth/device-code`, {});
  }

  async pollOAuthFlow(pluginId: string): Promise<OAuthPollResult> {
    return this.http.post(`/api/route/${pluginId}/auth/poll`, {});
  }

  async oauthLogout(pluginId: string): Promise<void> {
    return this.http.delete(`/api/route/${pluginId}/auth`);
  }

  // --- Plugin-routed probes ---

  async getPricing(
    pluginId: string
  ): Promise<{ prices: unknown[] }> {
    return this.http.get(`/api/route/${pluginId}/pricing`);
  }

  async updatePricing(
    pluginId: string,
    prices: unknown[]
  ): Promise<void> {
    return this.http.put(`/api/route/${pluginId}/pricing`, { prices });
  }

  async getTools(
    pluginId: string
  ): Promise<{ tools: unknown[] }> {
    return this.http.get(`/api/route/${pluginId}/mcp`);
  }

  async getSystemPrompt(
    pluginId: string
  ): Promise<Record<string, string | undefined>> {
    return this.http.get(`/api/route/${pluginId}/system-prompt`);
  }

  async getDiscordCommands(
    pluginId: string
  ): Promise<{ commands: unknown[] }> {
    return this.http.get(`/api/route/${pluginId}/discord-commands`);
  }
}
