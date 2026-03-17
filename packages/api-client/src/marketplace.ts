import type { HttpTransport } from "./client.js";

export interface MarketplacePlugin {
  plugin_id: string;
  name: string;
  description: string;
  group: string;
  version: string;
  image: string;
  author: string;
  tags: string[];
  config_schema: Record<string, unknown>;
  provider: string;
}

export interface MarketplaceGroup {
  id: string;
  name: string;
  description: string;
  order: number;
}

export interface MarketplaceProvider {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  system: boolean;
  created_at: string;
}

interface BrowseResponse {
  plugins: MarketplacePlugin[];
  groups: MarketplaceGroup[];
}

interface ProvidersResponse {
  providers: MarketplaceProvider[];
}

interface InstallResponse {
  message: string;
  plugin: unknown;
}

export class MarketplaceAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async browse(query?: string): Promise<BrowseResponse> {
    const q = query ? `?q=${encodeURIComponent(query)}` : "";
    const res = await this.http.get<BrowseResponse>(
      `/api/marketplace/plugins${q}`
    );
    return { plugins: res.plugins || [], groups: res.groups || [] };
  }

  async listProviders(): Promise<MarketplaceProvider[]> {
    const res = await this.http.get<ProvidersResponse>(
      "/api/marketplace/providers"
    );
    return res.providers || [];
  }

  async install(pluginId: string): Promise<InstallResponse> {
    return this.http.post<InstallResponse>("/api/marketplace/install", {
      plugin_id: pluginId,
    });
  }
}
