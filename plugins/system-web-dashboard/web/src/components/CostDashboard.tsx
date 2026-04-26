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
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Alert, AlertDescription } from "@/components/ui/alert";


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

  // Active providers in the data.
  const providers = [
    ...new Set(costRecords.map((r) => r.provider)),
  ].sort();

  // Build chart data: costs stacked by provider.
  // Every data point must include all providers (default 0) so lines are continuous.
  const chartDataMap = new Map<string, Record<string, string | number>>();
  for (const r of costRecords) {
    let entry = chartDataMap.get(r.date);
    if (!entry) {
      entry = { date: r.date };
      for (const p of providers) entry[p] = 0;
      chartDataMap.set(r.date, entry);
    }
    entry[r.provider] = ((entry[r.provider] as number) || 0) + r.cost;
  }
  const chartData = Array.from(chartDataMap.values()).sort((a, b) =>
    (a.date as string).localeCompare(b.date as string)
  );

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
      <div className="space-y-4">
        <div className="text-sm text-muted-foreground">Loading cost data...</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {/* Summary cards */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium tracking-wider text-muted-foreground">
              TODAY
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-semibold">{fmt(todayCost)}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium tracking-wider text-muted-foreground">
              THIS WEEK
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-semibold">{fmt(weekCost)}</div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-xs font-medium tracking-wider text-muted-foreground">
              ALL TIME
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="text-2xl font-semibold">{fmt(totalCost)}</div>
          </CardContent>
        </Card>
        <Card className="flex items-center justify-center">
          <CardContent className="p-4">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setShowUserMapping(true)}
            >
              USER MAPPING
            </Button>
          </CardContent>
        </Card>
      </div>

      {/* Cost chart */}
      {chartData.length > 0 && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between gap-4 space-y-0">
            <CardTitle className="text-sm font-semibold tracking-wider">
              {GRANULARITY_OPTIONS.find((o) => o.value === granularity)?.label.toUpperCase()} COSTS
            </CardTitle>
            <div className="flex items-center gap-2">
              {users.length > 0 && (
                <Select
                  value={selectedUserID || "__all__"}
                  onValueChange={(v) => setUserFilter(v === "__all__" ? "" : v)}
                >
                  <SelectTrigger className="w-[180px] h-8">
                    <SelectValue placeholder="All Users" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__all__">All Users</SelectItem>
                    {users.map((u) => (
                      <SelectItem key={u.user_id} value={u.user_id}>
                        {u.user_id} ({u.count})
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              <Select
                value={granularity}
                onValueChange={(v) => setGranularity(v as Granularity)}
              >
                <SelectTrigger className="w-[140px] h-8">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {GRANULARITY_OPTIONS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </CardHeader>
          <CardContent>
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
                <Legend
                  wrapperStyle={{ paddingTop: 12 }}
                  formatter={(value: string) => (
                    <span style={{ color: "#9ca3af", fontSize: 12, marginRight: 16 }}>{value}</span>
                  )}
                />
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
          </CardContent>
        </Card>
      )}

      {/* User filter (shown when no chart but users exist) */}
      {chartData.length === 0 && users.length > 0 && (
        <div className="flex items-center justify-end">
          <Select
            value={selectedUserID || "__all__"}
            onValueChange={(v) => setUserFilter(v === "__all__" ? "" : v)}
          >
            <SelectTrigger className="w-[180px] h-8">
              <SelectValue placeholder="All Users" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__all__">All Users</SelectItem>
              {users.map((u) => (
                <SelectItem key={u.user_id} value={u.user_id}>
                  {u.user_id} ({u.count})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      )}

      {/* Model breakdown table */}
      {breakdown.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-sm font-semibold tracking-wider">
              MODEL BREAKDOWN
            </CardTitle>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Provider</TableHead>
                  <TableHead>Model</TableHead>
                  <TableHead>Requests</TableHead>
                  <TableHead>Input</TableHead>
                  <TableHead>Output</TableHead>
                  <TableHead>Cached</TableHead>
                  <TableHead>Cost</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {breakdown.map((row) => (
                  <TableRow key={`${row.provider}-${row.model}`}>
                    <TableCell>
                      <span
                        className="inline-block w-2 h-2 rounded-full mr-2 align-middle"
                        style={{ background: providerColor(row.provider) }}
                      />
                      {row.provider}
                    </TableCell>
                    <TableCell>{row.model}</TableCell>
                    <TableCell>{row.requests.toLocaleString()}</TableCell>
                    <TableCell>{fmtTokens(row.inputTokens)}</TableCell>
                    <TableCell>{fmtTokens(row.outputTokens)}</TableCell>
                    <TableCell>{fmtTokens(row.cachedTokens)}</TableCell>
                    <TableCell className="font-medium tabular-nums">{fmt(row.cost)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}

      {/* Pricing table (inline) */}
      <PricingTable />

      {costRecords.length === 0 && !loading && (
        <div className="text-sm text-muted-foreground text-center py-8">
          No usage data yet. Costs will appear here as you use the AI agents and
          video tools.
        </div>
      )}

      {/* External user mapping modal */}
      {showUserMapping && <ExternalUserMapping onClose={() => setShowUserMapping(false)} />}
    </div>
  );
}
