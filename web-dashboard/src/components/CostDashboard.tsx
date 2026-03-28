import { useEffect, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import { useCostStore, selectCostRecords, type Granularity } from "../stores/costStore";
import PricingTable from "./PricingTable";
import ExternalUserMapping from "./ExternalUserMapping";


const GRANULARITY_OPTIONS: { value: Granularity; label: string }[] = [
  { value: "hourly", label: "Hourly" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
  { value: "monthly", label: "Monthly" },
];

// Assign evenly-spaced hues to providers so colors are always distinct.
const _providerColorCache = new Map<string, string>();
let _providerColorIndex = 0;
const GOLDEN_ANGLE = 137.508; // degrees — maximally spreads hues

function providerColor(name: string): string {
  let color = _providerColorCache.get(name);
  if (!color) {
    const hue = (_providerColorIndex * GOLDEN_ANGLE) % 360;
    color = `hsl(${Math.round(hue)}, 70%, 60%)`;
    _providerColorCache.set(name, color);
    _providerColorIndex++;
  }
  return color;
}

export default function CostDashboard() {
  const costRecords = useCostStore(selectCostRecords);
  const { granularity, selectedUserID, users, loading, error } = useCostStore(
    useShallow((s) => ({
      granularity: s.granularity,
      selectedUserID: s.selectedUserID,
      users: s.users,
      loading: s.loading,
      error: s.error,
    }))
  );
  const loadPricing = useCostStore((s) => s.loadPricing);
  const loadUsage = useCostStore((s) => s.loadUsage);
  const loadUsers = useCostStore((s) => s.loadUsers);
  const setGranularity = useCostStore((s) => s.setGranularity);
  const setUserFilter = useCostStore((s) => s.setUserFilter);

  const [showUserMapping, setShowUserMapping] = useState(false);

  useEffect(() => {
    loadPricing();
    loadUsage();
    loadUsers();
  }, [loadPricing, loadUsage, loadUsers]);

  // Summary calculations (always based on date prefix, granularity-independent).
  const now = new Date();
  const todayStr = now.toISOString().substring(0, 10);
  const weekAgo = new Date(now.getTime() - 7 * 86400000)
    .toISOString()
    .substring(0, 10);

  const todayCost = costRecords
    .filter((r) => r.date.startsWith(todayStr))
    .reduce((s, r) => s + r.cost, 0);

  const weekCost = costRecords
    .filter((r) => r.date.substring(0, 10) >= weekAgo)
    .reduce((s, r) => s + r.cost, 0);

  const totalCost = costRecords.reduce((s, r) => s + r.cost, 0);

  // Build chart data: daily costs stacked by provider.
  const chartDataMap = new Map<string, Record<string, string | number>>();
  for (const r of costRecords) {
    let entry = chartDataMap.get(r.date);
    if (!entry) {
      entry = { date: r.date };
      chartDataMap.set(r.date, entry);
    }
    entry[r.provider] = ((entry[r.provider] as number) || 0) + r.cost;
  }
  const chartData = Array.from(chartDataMap.values()).sort((a, b) =>
    (a.date as string).localeCompare(b.date as string)
  );

  // Active providers in the data.
  const providers = [
    ...new Set(costRecords.map((r) => r.provider)),
  ].sort();

  // Breakdown table: per-model totals.
  const modelBreakdown = new Map<
    string,
    {
      provider: string;
      model: string;
      cost: number;
      inputTokens: number;
      outputTokens: number;
      cachedTokens: number;
      requests: number;
    }
  >();
  for (const r of costRecords) {
    const key = `${r.provider}|${r.model}`;
    const existing = modelBreakdown.get(key);
    if (existing) {
      existing.cost += r.cost;
      existing.inputTokens += r.inputTokens;
      existing.outputTokens += r.outputTokens;
      existing.cachedTokens += r.cachedTokens;
      existing.requests += r.requests;
    } else {
      modelBreakdown.set(key, { ...r });
    }
  }
  const breakdown = Array.from(modelBreakdown.values()).sort(
    (a, b) => b.cost - a.cost
  );

  const fmt = (n: number) =>
    n < 0.01 && n > 0 ? `$${n.toFixed(4)}` : `$${n.toFixed(2)}`;
  const fmtTokens = (n: number) =>
    n >= 1_000_000
      ? `${(n / 1_000_000).toFixed(1)}M`
      : n >= 1_000
      ? `${(n / 1_000).toFixed(1)}K`
      : `${n}`;

  if (loading && costRecords.length === 0) {
    return (
      <div className="cost-dashboard">
        <div className="cost-loading">Loading cost data...</div>
      </div>
    );
  }

  return (
    <div className="cost-dashboard">
      {error && <div className="cost-error">{error}</div>}

      {/* Summary cards */}
      <div className="cost-summary-cards">
        <div className="cost-card">
          <div className="cost-card-label">TODAY</div>
          <div className="cost-card-value">{fmt(todayCost)}</div>
        </div>
        <div className="cost-card">
          <div className="cost-card-label">THIS WEEK</div>
          <div className="cost-card-value">{fmt(weekCost)}</div>
        </div>
        <div className="cost-card">
          <div className="cost-card-label">ALL TIME</div>
          <div className="cost-card-value">{fmt(totalCost)}</div>
        </div>
        <div className="cost-card cost-card-action">
          <button
            className="cost-edit-pricing-btn"
            style={{ fontSize: 11, padding: "4px 10px" }}
            onClick={() => setShowUserMapping(true)}
          >
            USER MAPPING
          </button>
        </div>
      </div>

      {/* Cost chart */}
      {chartData.length > 0 && (
        <div className="cost-chart-container">
          <div className="cost-chart-header">
            <h3 className="cost-section-title">
              {GRANULARITY_OPTIONS.find((o) => o.value === granularity)?.label.toUpperCase()} COSTS
            </h3>
            <div className="cost-chart-controls">
              {users.length > 0 && (
                <select
                  className="cost-granularity-select"
                  value={selectedUserID}
                  onChange={(e) => setUserFilter(e.target.value)}
                >
                  <option value="">All Users</option>
                  {users.map((u) => (
                    <option key={u.user_id} value={u.user_id}>
                      {u.user_id} ({u.count})
                    </option>
                  ))}
                </select>
              )}
              <select
                className="cost-granularity-select"
                value={granularity}
                onChange={(e) => setGranularity(e.target.value as Granularity)}
              >
                {GRANULARITY_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <ResponsiveContainer width="100%" height={300}>
            <AreaChart data={chartData}>
              <CartesianGrid strokeDasharray="3 3" stroke="#33354080" />
              <XAxis
                dataKey="date"
                tick={{ fill: "#9ca3af", fontSize: 11 }}
                tickFormatter={(v: string) => {
                  switch (granularity) {
                    case "hourly":
                      return v.substring(5).replace("T", " ") + "h";
                    case "daily":
                      return v.substring(5);
                    case "weekly":
                      return "W " + v.substring(5);
                    case "monthly":
                      return v.substring(0, 7);
                  }
                }}
              />
              <YAxis
                tick={{ fill: "#9ca3af", fontSize: 11 }}
                tickFormatter={(v: number) => `$${v.toFixed(2)}`}
              />
              <Tooltip
                contentStyle={{
                  background: "#24252f",
                  border: "1px solid #33354080",
                  borderRadius: 6,
                  color: "#e5e7eb",
                  fontSize: 12,
                }}
                formatter={(value) => [`$${Number(value).toFixed(4)}`, ""]}
              />
              <Legend />
              {providers.map((p) => (
                <Area
                  key={p}
                  type="monotone"
                  dataKey={p}
                  stackId="1"
                  stroke={providerColor(p)}
                  fill={providerColor(p)}
                  fillOpacity={0.3}
                />
              ))}
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}

      {/* User filter (shown when no chart but users exist) */}
      {chartData.length === 0 && users.length > 0 && (
        <div className="cost-chart-header">
          <select
            className="cost-granularity-select"
            value={selectedUserID}
            onChange={(e) => setUserFilter(e.target.value)}
          >
            <option value="">All Users</option>
            {users.map((u) => (
              <option key={u.user_id} value={u.user_id}>
                {u.user_id} ({u.count})
              </option>
            ))}
          </select>
        </div>
      )}

      {/* Model breakdown table */}
      {breakdown.length > 0 && (
        <div className="cost-breakdown">
          <h3 className="cost-section-title">MODEL BREAKDOWN</h3>
          <div className="cost-table-wrapper">
            <table className="cost-table">
              <thead>
                <tr>
                  <th>Provider</th>
                  <th>Model</th>
                  <th>Requests</th>
                  <th>Input</th>
                  <th>Output</th>
                  <th>Cached</th>
                  <th>Cost</th>
                </tr>
              </thead>
              <tbody>
                {breakdown.map((row) => (
                  <tr key={`${row.provider}-${row.model}`}>
                    <td>
                      <span
                        className="cost-provider-dot"
                        style={{
                          background:
                            providerColor(row.provider),
                        }}
                      />
                      {row.provider}
                    </td>
                    <td>{row.model}</td>
                    <td>{row.requests.toLocaleString()}</td>
                    <td>{fmtTokens(row.inputTokens)}</td>
                    <td>{fmtTokens(row.outputTokens)}</td>
                    <td>{fmtTokens(row.cachedTokens)}</td>
                    <td className="cost-amount">{fmt(row.cost)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Pricing table (inline) */}
      <PricingTable />

      {costRecords.length === 0 && !loading && (
        <div className="cost-empty">
          No usage data yet. Costs will appear here as you use the AI agents and
          video tools.
        </div>
      )}

      {/* External user mapping modal */}
      {showUserMapping && <ExternalUserMapping onClose={() => setShowUserMapping(false)} />}
    </div>
  );
}
