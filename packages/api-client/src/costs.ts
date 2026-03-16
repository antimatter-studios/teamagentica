import type { HttpTransport } from "./client.js";

export interface ModelPrice {
  id: number;
  provider: string;
  model: string;
  input_per_1m: number;
  output_per_1m: number;
  cached_per_1m: number;
  per_request: number;
  currency: string;
  effective_from: string;
  effective_to: string | null;
  created_at: string;
}

export interface TokenUsageRecord {
  ts: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens?: number;
  reasoning_tokens?: number;
  duration_ms: number;
  backend?: string;
}

export interface RequestUsageRecord {
  ts: string;
  model: string;
  prompt: string;
  task_id: string;
  status: string;
  duration_ms: number;
}

export type UsageRecord = TokenUsageRecord | RequestUsageRecord;

export function isTokenRecord(r: UsageRecord): r is TokenUsageRecord {
  return "input_tokens" in r;
}

export interface CostExplorerRecord {
  id: number;
  plugin_id: string;
  provider: string;
  model: string;
  record_type: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cached_tokens: number;
  reasoning_tokens: number;
  duration_ms: number;
  backend: string;
  status: string;
  prompt: string;
  task_id: string;
  user_id: string;
  ts: string;
  created_at: string;
}

export interface CostExplorerUser {
  user_id: string;
  count: number;
}

export interface ExternalUserMapping {
  id: number;
  teamagentica_user_id: number;
  external_id: string;
  source: string;
  label: string;
  created_at: string;
  updated_at: string;
}

export class CostsAPI {
  constructor(private http: HttpTransport) {}

  async fetchPricing(): Promise<ModelPrice[]> {
    return this.http.get<ModelPrice[]>("/api/pricing");
  }

  async fetchCurrentPricing(): Promise<ModelPrice[]> {
    return this.http.get<ModelPrice[]>("/api/pricing/current");
  }

  async savePricing(
    price: Omit<ModelPrice, "id" | "effective_from" | "effective_to" | "created_at">
  ): Promise<ModelPrice> {
    return this.http.post<ModelPrice>("/api/pricing", price);
  }

  async deletePricing(id: number): Promise<void> {
    return this.http.delete(`/api/pricing/${id}`);
  }

  async fetchPluginUsageRecords(
    pluginId: string,
    since?: string
  ): Promise<{ records: UsageRecord[] }> {
    const qs = since ? `?since=${encodeURIComponent(since)}` : "";
    return this.http.get(`/api/route/${pluginId}/usage/records${qs}`);
  }

  async fetchCostExplorerRecords(
    since?: string,
    userID?: string
  ): Promise<{ records: CostExplorerRecord[] }> {
    const params = new URLSearchParams();
    if (since) params.set("since", since);
    if (userID) params.set("user_id", userID);
    const qs = params.toString() ? `?${params}` : "";
    return this.http.get(`/api/route/infra-cost-tracking/usage/records${qs}`);
  }

  async fetchCostExplorerUsers(): Promise<{ users: CostExplorerUser[] }> {
    return this.http.get("/api/route/infra-cost-tracking/usage/users");
  }

  async fetchExternalUsers(
    source?: string
  ): Promise<{ mappings: ExternalUserMapping[] }> {
    const qs = source ? `?source=${encodeURIComponent(source)}` : "";
    return this.http.get(`/api/external-users${qs}`);
  }

  async createExternalUser(data: {
    external_id: string;
    source: string;
    teamagentica_user_id: number;
    label?: string;
  }): Promise<ExternalUserMapping> {
    return this.http.post("/api/external-users", data);
  }

  async updateExternalUser(
    id: number,
    data: { teamagentica_user_id?: number; label?: string }
  ): Promise<ExternalUserMapping> {
    return this.http.put(`/api/external-users/${id}`, data);
  }

  async deleteExternalUser(id: number): Promise<void> {
    return this.http.delete(`/api/external-users/${id}`);
  }
}
