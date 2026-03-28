import { useMemo, useState } from "react";
import { useCostStore } from "../stores/costStore";
import type { ModelPrice } from "@teamagentica/api-client";
import ConfirmDialog from "./ConfirmDialog";

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
    <div className="cost-breakdown">
      <h3 className="cost-section-title">PRICING</h3>
      <p className="pricing-hint">
        Each row is a price window. Adding a new price for the same
        provider + model closes the previous window automatically.
      </p>

      <div className="pricing-table-wrapper">
        <table className="cost-table pricing-edit-table">
          <thead>
            <tr>
              <th>Provider</th>
              <th>Model</th>
              <th>Input $/1M</th>
              <th>Output $/1M</th>
              <th>Cached $/1M</th>
              <th>Per Req $</th>
              <th>Sub $/mo</th>
              <th>From</th>
              <th>To</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((p) => (
              <tr key={p.id} className={p.effective_to ? "pricing-row-historical" : ""}>
                <td>{p.provider}</td>
                <td>{p.model}</td>
                <td>${p.input_per_1m}</td>
                <td>${p.output_per_1m}</td>
                <td>${p.cached_per_1m}</td>
                <td>{p.per_request > 0 ? `$${p.per_request}` : "—"}</td>
                <td>{p.subscription > 0 ? `$${p.subscription}` : "—"}</td>
                <td className="pricing-date">{fmtDate(p.effective_from)}</td>
                <td className="pricing-date">{p.effective_to ? fmtDate(p.effective_to) : "now"}</td>
                <td>
                  <button
                    className="pricing-delete-btn"
                    onClick={() => setDeleting(p)}
                  >
                    ✕
                  </button>
                </td>
              </tr>
            ))}

            {/* New row */}
            <tr className="pricing-new-row">
              <td>
                <select
                  value={newRow.provider}
                  onChange={(e) =>
                    setNewRow({ ...emptyRow, provider: e.target.value })
                  }
                >
                  <option value="">— provider —</option>
                  {providers.map((p) => (
                    <option key={p} value={p}>{p}</option>
                  ))}
                </select>
              </td>
              <td>
                <select
                  value={newRow.model}
                  disabled={!newRow.provider}
                  onChange={(e) =>
                    setNewRow({ ...newRow, model: e.target.value })
                  }
                >
                  <option value="">— model —</option>
                  {modelsForProvider.map((m) => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </select>
              </td>
              <td>
                <input
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
              </td>
              <td>
                <input
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
              </td>
              <td>
                <input
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
              </td>
              <td>
                <input
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
              </td>
              <td>
                <input
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
              </td>
              <td colSpan={2}></td>
              <td>
                <button
                  className="pricing-save-btn"
                  onClick={handleSave}
                  disabled={saving || !formReady}
                >
                  {saving ? "..." : "+"}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
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
