import { useEffect, useState } from "react";
import { Loader2, Plus, X } from "lucide-react";
import { apiClient } from "../api/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface PricingEntry {
  provider: string;
  model: string;
  input_per_1m: number;
  output_per_1m: number;
  cached_per_1m: number;
  per_request: number;
  currency: string;
}

interface Props {
  pluginId: string;
}

export default function PluginPricing({ pluginId }: Props) {
  const [prices, setPrices] = useState<PricingEntry[]>([]);
  const [editing, setEditing] = useState<PricingEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [success, setSuccess] = useState("");

  useEffect(() => {
    loadPricing();
  }, [pluginId]);

  async function loadPricing() {
    setLoading(true);
    setError("");
    try {
      const data = await apiClient.plugins.getPricing(pluginId) as { prices: PricingEntry[] };
      setPrices(data.prices || []);
      setEditing(structuredClone(data.prices || []));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load pricing");
    } finally {
      setLoading(false);
    }
  }

  function updateField(
    idx: number,
    field: keyof PricingEntry,
    value: string | number
  ) {
    const updated = [...editing];
    (updated[idx] as unknown as Record<string, string | number>)[field] = value;
    setEditing(updated);
  }

  function addRow() {
    setEditing([
      ...editing,
      {
        provider: "",
        model: "",
        input_per_1m: 0,
        output_per_1m: 0,
        cached_per_1m: 0,
        per_request: 0,
        currency: "USD",
      },
    ]);
  }

  function removeRow(idx: number) {
    setEditing(editing.filter((_, i) => i !== idx));
  }

  async function handleSave() {
    const valid = editing.filter((e) => e.provider && e.model);
    if (valid.length === 0) return;

    setSaving(true);
    setError("");
    setSuccess("");
    try {
      await apiClient.plugins.updatePricing(pluginId, valid);
      setPrices(structuredClone(valid));
      setEditing(structuredClone(valid));
      setSuccess("Prices saved and pushed to kernel");
      setTimeout(() => setSuccess(""), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save pricing");
    } finally {
      setSaving(false);
    }
  }

  const hasChanges =
    JSON.stringify(editing) !== JSON.stringify(prices);

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading pricing...
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold tracking-wide">MODEL PRICING</h3>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={addRow}>
            <Plus className="h-4 w-4" />
            ADD MODEL
          </Button>
          <Button
            size="sm"
            onClick={handleSave}
            disabled={saving || !hasChanges}
          >
            {saving ? "SAVING..." : "SAVE"}
          </Button>
        </div>
      </div>

      <p className="text-sm text-muted-foreground">
        Edit prices here. Changes are pushed to the kernel and create a new
        pricing window for accurate historical cost tracking.
      </p>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {success && (
        <Alert>
          <AlertDescription>{success}</AlertDescription>
        </Alert>
      )}

      {editing.length === 0 ? (
        <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
          No pricing configured. Click "ADD MODEL" to add pricing for this
          plugin's models.
        </div>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Provider</TableHead>
                <TableHead>Model</TableHead>
                <TableHead>Input $/1M</TableHead>
                <TableHead>Output $/1M</TableHead>
                <TableHead>Cached $/1M</TableHead>
                <TableHead>Per Request $</TableHead>
                <TableHead className="w-12"></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {editing.map((row, idx) => (
                <TableRow key={idx}>
                  <TableCell>
                    <Input
                      value={row.provider}
                      onChange={(e) => updateField(idx, "provider", e.target.value)}
                      placeholder="openai"
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      value={row.model}
                      onChange={(e) => updateField(idx, "model", e.target.value)}
                      placeholder="gpt-4o"
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      type="number"
                      step="0.01"
                      value={row.input_per_1m || ""}
                      onChange={(e) =>
                        updateField(idx, "input_per_1m", parseFloat(e.target.value) || 0)
                      }
                      placeholder="0.00"
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      type="number"
                      step="0.01"
                      value={row.output_per_1m || ""}
                      onChange={(e) =>
                        updateField(idx, "output_per_1m", parseFloat(e.target.value) || 0)
                      }
                      placeholder="0.00"
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      type="number"
                      step="0.01"
                      value={row.cached_per_1m || ""}
                      onChange={(e) =>
                        updateField(idx, "cached_per_1m", parseFloat(e.target.value) || 0)
                      }
                      placeholder="0.00"
                    />
                  </TableCell>
                  <TableCell>
                    <Input
                      type="number"
                      step="0.01"
                      value={row.per_request || ""}
                      onChange={(e) =>
                        updateField(idx, "per_request", parseFloat(e.target.value) || 0)
                      }
                      placeholder="0.00"
                    />
                  </TableCell>
                  <TableCell>
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={() => removeRow(idx)}
                    >
                      <X className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
