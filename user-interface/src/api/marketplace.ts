import { apiGet, apiPost } from "./client";

export interface MarketplacePlugin {
  plugin_id: string;
  name: string;
  description: string;
  version: string;
  image: string;
  author: string;
  tags: string[];
  config_schema: Record<string, unknown>;
  provider: string;
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
}

interface ProvidersResponse {
  providers: MarketplaceProvider[];
}

interface InstallResponse {
  message: string;
  plugin: unknown;
}

export async function browseMarketplace(query?: string): Promise<MarketplacePlugin[]> {
  const q = query ? `?q=${encodeURIComponent(query)}` : "";
  const res = await apiGet<BrowseResponse>(`/api/marketplace/plugins${q}`);
  return res.plugins || [];
}

export async function listProviders(): Promise<MarketplaceProvider[]> {
  const res = await apiGet<ProvidersResponse>("/api/marketplace/providers");
  return res.providers || [];
}

export async function installFromMarketplace(pluginId: string): Promise<InstallResponse> {
  return apiPost<InstallResponse>("/api/marketplace/install", { plugin_id: pluginId });
}
