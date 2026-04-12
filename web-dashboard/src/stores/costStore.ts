import { create } from "zustand";
import { apiClient } from "../api/client";
import type { ModelPrice, UsageRecord, CostExplorerRecord, CostExplorerUser } from "@teamagentica/api-client";
import { isTokenRecord } from "@teamagentica/api-client";

export type Granularity = "hourly" | "daily" | "weekly" | "monthly";

interface CostRecord {
  date: string;
  provider: string;
  model: string;
  cost: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  requests: number;
}

interface CostStore {
  prices: ModelPrice[];
  allPrices: ModelPrice[];
  records: UsageRecord[];
  providerMap: Record<string, string>;
  granularity: Granularity;
  selectedUserID: string;
  users: CostExplorerUser[];
  loading: boolean;
  error: string | null;
  loadPricing: () => Promise<void>;
  loadUsage: () => Promise<void>;
  loadUsers: () => Promise<void>;
  setGranularity: (g: Granularity) => void;
  setUserFilter: (userID: string) => void;
  savePriceEntry: (price: Parameters<typeof apiClient.costs.savePricing>[0]) => Promise<void>;
  deletePriceEntry: (id: number) => Promise<void>;
}

// Find the price that was effective at a given timestamp.
function findEffectivePrice(
  allPrices: ModelPrice[],
  provider: string,
  model: string,
  timestamp: string
): ModelPrice | undefined {
  const ts = new Date(timestamp).getTime();
  return allPrices.find((p) => {
    if (p.provider !== provider || p.model !== model) return false;
    const from = new Date(p.effective_from).getTime();
    const to = p.effective_to ? new Date(p.effective_to).getTime() : Infinity;
    return ts >= from && ts < to;
  });
}

function bucketKey(ts: string, granularity: Granularity): string {
  switch (granularity) {
    case "hourly":
      return ts.substring(0, 13); // YYYY-MM-DDTHH
    case "daily":
      return ts.substring(0, 10); // YYYY-MM-DD
    case "weekly": {
      const d = new Date(ts);
      const day = d.getUTCDay();
      const monday = new Date(d.getTime() - ((day + 6) % 7) * 86400000);
      return monday.toISOString().substring(0, 10);
    }
    case "monthly":
      return ts.substring(0, 7); // YYYY-MM
  }
}

function calculateCosts(
  allPrices: ModelPrice[],
  records: UsageRecord[],
  providerMap: Record<string, string>,
  granularity: Granularity = "hourly"
): CostRecord[] {
  const dayMap = new Map<string, CostRecord>();

  for (const rec of records) {
    const provider = providerMap[rec.model] || "";
    if (!provider || !rec.model) continue; // skip records with missing provider/model
    const date = bucketKey(rec.ts, granularity);
    const key = `${date}|${provider}|${rec.model}`;

    let entry = dayMap.get(key);
    if (!entry) {
      entry = {
        date,
        provider,
        model: rec.model,
        cost: 0,
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
        requests: 0,
      };
      dayMap.set(key, entry);
    }

    entry.requests++;

    // Always accumulate token counts regardless of pricing availability.
    if (isTokenRecord(rec)) {
      entry.inputTokens += rec.input_tokens;
      entry.outputTokens += rec.output_tokens;
      entry.cachedTokens += rec.cached_tokens || 0;
    }

    // Only calculate per-usage costs when pricing is available and not subscription-based.
    const price = findEffectivePrice(allPrices, provider, rec.model, rec.ts);
    if (price && !price.subscription) {
      if (isTokenRecord(rec)) {
        const inputCost = (rec.input_tokens / 1_000_000) * price.input_per_1m;
        const outputCost = (rec.output_tokens / 1_000_000) * price.output_per_1m;
        const cachedCost = ((rec.cached_tokens || 0) / 1_000_000) * price.cached_per_1m;
        entry.cost += inputCost + outputCost + cachedCost;
      } else {
        // Video/image tools: per-request pricing.
        if (rec.status === "completed" || rec.status === "submitted") {
          entry.cost += price.per_request;
        }
      }
    }
  }

  // Add subscription costs: flat monthly fee per provider+model price window.
  // Each month that falls within a subscription price window gets the flat cost once.
  const subSeen = new Set<string>();
  for (const price of allPrices) {
    if (!price.subscription) continue;
    const from = new Date(price.effective_from);
    const to = price.effective_to ? new Date(price.effective_to) : new Date();
    // Walk each month in the window.
    const cursor = new Date(Date.UTC(from.getUTCFullYear(), from.getUTCMonth(), 1));
    const end = new Date(Date.UTC(to.getUTCFullYear(), to.getUTCMonth(), 1));
    while (cursor <= end) {
      const monthKey = cursor.toISOString().substring(0, 7); // YYYY-MM
      const date = granularity === "monthly" ? monthKey : bucketKey(cursor.toISOString(), granularity);
      const dedupKey = `${monthKey}|${price.provider}|${price.model}`;
      if (!subSeen.has(dedupKey)) {
        subSeen.add(dedupKey);
        const key = `${date}|${price.provider}|${price.model}`;
        let entry = dayMap.get(key);
        if (!entry) {
          entry = {
            date,
            provider: price.provider,
            model: price.model,
            cost: 0,
            inputTokens: 0,
            outputTokens: 0,
            cachedTokens: 0,
            requests: 0,
          };
          dayMap.set(key, entry);
        }
        entry.cost += price.subscription;
      }
      cursor.setUTCMonth(cursor.getUTCMonth() + 1);
    }
  }

  return Array.from(dayMap.values()).sort(
    (a, b) => a.date.localeCompare(b.date) || a.provider.localeCompare(b.provider)
  );
}

// Convert cost-explorer records to UsageRecord format and build provider map.
function convertCostExplorerRecords(
  ceRecords: CostExplorerRecord[]
): { records: UsageRecord[]; providerMap: Record<string, string> } {
  const records: UsageRecord[] = [];
  const providerMap: Record<string, string> = {};

  for (const r of ceRecords) {
    providerMap[r.model] = r.provider;

    if (r.record_type === "request") {
      records.push({
        ts: r.ts,
        model: r.model,
        prompt: r.prompt,
        task_id: r.task_id,
        status: r.status,
        duration_ms: r.duration_ms,
      });
    } else {
      records.push({
        ts: r.ts,
        model: r.model,
        input_tokens: r.input_tokens,
        output_tokens: r.output_tokens,
        total_tokens: r.total_tokens,
        cached_tokens: r.cached_tokens,
        reasoning_tokens: r.reasoning_tokens,
        duration_ms: r.duration_ms,
        backend: r.backend,
      });
    }
  }

  return { records, providerMap };
}

// Memoized selector for derived costRecords.
let _cachedCostRecords: CostRecord[] = [];
let _prevAllPrices: ModelPrice[] = [];
let _prevRecords: UsageRecord[] = [];
let _prevProviderMap: Record<string, string> = {};
let _prevGranularity: Granularity = "hourly";

export function selectCostRecords(state: CostStore): CostRecord[] {
  if (
    state.allPrices === _prevAllPrices &&
    state.records === _prevRecords &&
    state.providerMap === _prevProviderMap &&
    state.granularity === _prevGranularity
  ) {
    return _cachedCostRecords;
  }
  _prevAllPrices = state.allPrices;
  _prevRecords = state.records;
  _prevProviderMap = state.providerMap;
  _prevGranularity = state.granularity;
  _cachedCostRecords = calculateCosts(state.allPrices, state.records, state.providerMap, state.granularity);
  return _cachedCostRecords;
}

export const useCostStore = create<CostStore>((set, get) => ({
  prices: [],
  allPrices: [],
  records: [],
  providerMap: {},
  granularity: "hourly" as Granularity,
  selectedUserID: "",
  users: [],
  loading: false,
  error: null,

  loadPricing: async () => {
    try {
      const [current, all] = await Promise.all([
        apiClient.costs.fetchCurrentPricing(),
        apiClient.costs.fetchPricing(),
      ]);
      set({ prices: current, allPrices: all });
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to load pricing" });
    }
  },

  loadUsage: async () => {
    set({ loading: true });
    try {
      const userID = get().selectedUserID || undefined;
      const resp = await apiClient.costs.fetchCostExplorerRecords(undefined, userID);
      const converted = convertCostExplorerRecords(resp.records);

      const allPrices = get().allPrices.length > 0 ? get().allPrices : await apiClient.costs.fetchPricing();

      set({
        records: converted.records,
        providerMap: converted.providerMap,
        allPrices: allPrices,
        loading: false,
        error: null,
      });
    } catch (err) {
      set({
        loading: false,
        error: err instanceof Error ? err.message : "Failed to load usage — cost-explorer may not be running",
      });
    }
  },

  loadUsers: async () => {
    try {
      const resp = await apiClient.costs.fetchCostExplorerUsers();
      set({ users: resp.users || [] });
    } catch {
      // Non-critical — silently ignore.
    }
  },

  setGranularity: (g: Granularity) => {
    set({ granularity: g });
  },

  setUserFilter: (userID: string) => {
    set({ selectedUserID: userID });
    // Reload usage with new filter.
    get().loadUsage();
  },

  savePriceEntry: async (price) => {
    try {
      await apiClient.costs.savePricing(price);
      await get().loadPricing();
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to save price" });
    }
  },

  deletePriceEntry: async (id) => {
    try {
      await apiClient.costs.deletePricing(id);
      await get().loadPricing();
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to delete price" });
    }
  },
}));
