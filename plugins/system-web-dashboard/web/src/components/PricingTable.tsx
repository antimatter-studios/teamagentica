import { useMemo, useState } from "react";
import { Plus, X } from "lucide-react";
import { useCostStore } from "../stores/costStore";
import type { ModelPrice } from "@teamagentica/api-client";
import ConfirmDialog from "./ConfirmDialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
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
import { cn } from "@/lib/utils";

const emptyRow = {
  provider: "",
  model: "",
  input_per_1m: 0,
  output_per_1m: 0,
  cached_per_1m: 0,
  per_request: 0,
  subscription: 0,
  currency: "USD",
};

function fmtDate(iso: string | null): string {
  if (!iso) return "—";
  return iso.substring(0, 16).replace("T", " ");
}

export default function PricingTable() {
  const allPrices = useCostStore((s) => s.allPrices);
  const providerMap = useCostStore((s) => s.providerMap);
  const savePriceEntry = useCostStore((s) => s.savePriceEntry);
  const deletePriceEntry = useCostStore((s) => s.deletePriceEntry);
  const [newRow, setNewRow] = useState(emptyRow);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState<ModelPrice | null>(null);

  // Sort: provider, model, effective_from ascending.
  const sorted = useMemo(
    () =>
      [...allPrices].sort(
        (a, b) =>
          a.provider.localeCompare(b.provider) ||
          a.model.localeCompare(b.model) ||
          a.effective_from.localeCompare(b.effective_from)
      ),
    [allPrices]
  );

  // Derive unique providers from usage data + existing pricing.
  const providers = useMemo(() => {
    const s = new Set<string>();
    Object.values(providerMap).forEach((p) => p && s.add(p));
    allPrices.forEach((p) => p.provider && s.add(p.provider));
    return Array.from(s).sort();
  }, [providerMap, allPrices]);

  // Derive models for the selected provider.
  const modelsForProvider = useMemo(() => {
    if (!newRow.provider) return [];
    const s = new Set<string>();
    for (const [model, prov] of Object.entries(providerMap)) {
      if (prov === newRow.provider) s.add(model);
    }
    allPrices.forEach((p) => {
      if (p.provider === newRow.provider) s.add(p.model);
    });
    return Array.from(s).sort();
  }, [newRow.provider, providerMap, allPrices]);

  const formReady = !!newRow.provider && !!newRow.model;

  const handleSave = async () => {
    if (!newRow.provider || !newRow.model) return;
    setSaving(true);
    await savePriceEntry(newRow);
    setNewRow(emptyRow);
    setSaving(false);
  };

  const handleDelete = async () => {
    if (!deleting) return;
    await deletePriceEntry(deleting.id);
    setDeleting(null);
  };

  return (
    <div className="flex flex-col gap-3">
      <h3 className="text-sm font-semibold tracking-wide">PRICING</h3>
      <p className="text-sm text-muted-foreground">
        Each row is a price window. Adding a new price for the same
        provider + model closes the previous window automatically.
      </p>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Provider</TableHead>
              <TableHead>Model</TableHead>
              <TableHead>Input $/1M</TableHead>
              <TableHead>Output $/1M</TableHead>
              <TableHead>Cached $/1M</TableHead>
              <TableHead>Per Req $</TableHead>
              <TableHead>Sub $/mo</TableHead>
              <TableHead>From</TableHead>
              <TableHead>To</TableHead>
              <TableHead className="w-12"></TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sorted.map((p) => (
              <TableRow
                key={p.id}
                className={cn(p.effective_to && "opacity-60")}
              >
                <TableCell>{p.provider}</TableCell>
                <TableCell>{p.model}</TableCell>
                <TableCell>${p.input_per_1m}</TableCell>
                <TableCell>${p.output_per_1m}</TableCell>
                <TableCell>${p.cached_per_1m}</TableCell>
                <TableCell>{p.per_request > 0 ? `$${p.per_request}` : "—"}</TableCell>
                <TableCell>{p.subscription > 0 ? `$${p.subscription}` : "—"}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{fmtDate(p.effective_from)}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{p.effective_to ? fmtDate(p.effective_to) : "now"}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon" onClick={() => setDeleting(p)}>
                    <X className="h-4 w-4" />
                  </Button>
                </TableCell>
              </TableRow>
            ))}

            {/* New row */}
            <TableRow className="bg-muted/30">
              <TableCell>
                <Select
                  value={newRow.provider}
                  onValueChange={(v) => setNewRow({ ...emptyRow, provider: v })}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="— provider —" />
                  </SelectTrigger>
                  <SelectContent>
                    {providers.map((p) => (
                      <SelectItem key={p} value={p}>{p}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </TableCell>
              <TableCell>
                <Select
                  value={newRow.model}
                  disabled={!newRow.provider}
                  onValueChange={(v) => setNewRow({ ...newRow, model: v })}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="— model —" />
                  </SelectTrigger>
                  <SelectContent>
                    {modelsForProvider.map((m) => (
                      <SelectItem key={m} value={m}>{m}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </TableCell>
              <TableCell>
                <Input
                  type="number"
                  step="0.01"
                  disabled={!formReady}
                  value={newRow.input_per_1m || ""}
                  onChange={(e) =>
                    setNewRow({
                      ...newRow,
                      input_per_1m: parseFloat(e.target.value) || 0,
                    })
                  }
                  placeholder="0.00"
                />
              </TableCell>
              <TableCell>
                <Input
                  type="number"
                  step="0.01"
                  disabled={!formReady}
                  value={newRow.output_per_1m || ""}
                  onChange={(e) =>
                    setNewRow({
                      ...newRow,
                      output_per_1m: parseFloat(e.target.value) || 0,
                    })
                  }
                  placeholder="0.00"
                />
              </TableCell>
              <TableCell>
                <Input
                  type="number"
                  step="0.01"
                  disabled={!formReady}
                  value={newRow.cached_per_1m || ""}
                  onChange={(e) =>
                    setNewRow({
                      ...newRow,
                      cached_per_1m: parseFloat(e.target.value) || 0,
                    })
                  }
                  placeholder="0.00"
                />
              </TableCell>
              <TableCell>
                <Input
                  type="number"
                  step="0.01"
                  disabled={!formReady}
                  value={newRow.per_request || ""}
                  onChange={(e) =>
                    setNewRow({
                      ...newRow,
                      per_request: parseFloat(e.target.value) || 0,
                    })
                  }
                  placeholder="0.00"
                />
              </TableCell>
              <TableCell>
                <Input
                  type="number"
                  step="0.01"
                  disabled={!formReady}
                  value={newRow.subscription || ""}
                  onChange={(e) =>
                    setNewRow({
                      ...newRow,
                      subscription: parseFloat(e.target.value) || 0,
                    })
                  }
                  placeholder="0.00"
                />
              </TableCell>
              <TableCell colSpan={2}></TableCell>
              <TableCell>
                <Button
                  variant="default"
                  size="icon"
                  onClick={handleSave}
                  disabled={saving || !formReady}
                >
                  {saving ? "..." : <Plus className="h-4 w-4" />}
                </Button>
              </TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>

      {deleting && (
        <ConfirmDialog
          title="Delete Pricing"
          confirmLabel="Delete"
          variant="danger"
          onConfirm={handleDelete}
          onCancel={() => setDeleting(null)}
        >
          Delete pricing for <strong>{deleting.provider}/{deleting.model}</strong> (from {fmtDate(deleting.effective_from)})?
        </ConfirmDialog>
      )}
    </div>
  );
}
