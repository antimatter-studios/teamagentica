import type { HttpTransport } from "./client.js";

export interface ModelPrice {
  id: number;
  provider: string;
  model: string;
  input_per_1m: number;
  output_per_1m: number;
  cached_per_1m: number;
  per_request: number;
  subscription: number;
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

export class CostsAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async fetchPricing(): Promise<ModelPrice[]> {
    return this.http.get<ModelPrice[]>("/api/route/infra-cost-tracking/pricing");
  }

  async fetchCurrentPricing(): Promise<ModelPrice[]> {
    return this.http.get<ModelPrice[]>("/api/route/infra-cost-tracking/pricing/current");
  }

  async savePricing(
    price: Omit<ModelPrice, "id" | "effective_from" | "effective_to" | "created_at">
  ): Promise<ModelPrice> {
    return this.http.post<ModelPrice>("/api/route/infra-cost-tracking/pricing", price);
  }

  async deletePricing(id: number): Promise<void> {
    return this.http.delete(`/api/route/infra-cost-tracking/pricing/${id}`);
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
}
