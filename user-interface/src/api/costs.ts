import { apiGet, apiPost, apiPut, apiDelete } from "./client";

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

// Token-based usage record (from agent plugins: openai, gemini).
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

// Request-based usage record (from video tools: veo, seedance).
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

export async function fetchPricing(): Promise<ModelPrice[]> {
  return apiGet<ModelPrice[]>("/api/pricing");
}

export async function fetchCurrentPricing(): Promise<ModelPrice[]> {
  return apiGet<ModelPrice[]>("/api/pricing/current");
}

export async function savePricing(
  price: Omit<ModelPrice, "id" | "effective_from" | "effective_to" | "created_at">
): Promise<ModelPrice> {
  return apiPost<ModelPrice>("/api/pricing", price);
}

export async function deletePricing(id: number): Promise<void> {
  return apiDelete(`/api/pricing/${id}`);
}

export async function fetchPluginUsageRecords(
  pluginId: string,
  since?: string
): Promise<{ records: UsageRecord[] }> {
  const qs = since ? `?since=${encodeURIComponent(since)}` : "";
  return apiGet(`/api/route/${pluginId}/usage/records${qs}`);
}

// Cost-explorer centralized record (single source of truth).
export interface CostExplorerRecord {
  id: number;
  plugin_id: string;
  provider: string;
  model: string;
  record_type: string; // "token" or "request"
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

export async function fetchCostExplorerRecords(
  since?: string,
  userID?: string
): Promise<{ records: CostExplorerRecord[] }> {
  const params = new URLSearchParams();
  if (since) params.set("since", since);
  if (userID) params.set("user_id", userID);
  const qs = params.toString() ? `?${params}` : "";
  return apiGet(`/api/route/infra-cost-explorer/usage/records${qs}`);
}

export interface CostExplorerUser {
  user_id: string;
  count: number;
}

export async function fetchCostExplorerUsers(): Promise<{ users: CostExplorerUser[] }> {
  return apiGet("/api/route/infra-cost-explorer/usage/users");
}

// External user mapping (kernel API).
export interface ExternalUserMapping {
  id: number;
  teamagentica_user_id: number;
  external_id: string;
  source: string;
  label: string;
  created_at: string;
  updated_at: string;
}

export async function fetchExternalUsers(source?: string): Promise<{ mappings: ExternalUserMapping[] }> {
  const qs = source ? `?source=${encodeURIComponent(source)}` : "";
  return apiGet(`/api/external-users${qs}`);
}

export async function createExternalUser(data: {
  external_id: string;
  source: string;
  teamagentica_user_id: number;
  label?: string;
}): Promise<ExternalUserMapping> {
  return apiPost("/api/external-users", data);
}

export async function updateExternalUser(
  id: number,
  data: { teamagentica_user_id?: number; label?: string }
): Promise<ExternalUserMapping> {
  return apiPut(`/api/external-users/${id}`, data);
}

export async function deleteExternalUser(id: number): Promise<void> {
  return apiDelete(`/api/external-users/${id}`);
}
