import { useState } from "react";
import { useCostStore } from "../stores/costStore";
import type { ModelPrice } from "../api/costs";

interface Props {
  onClose: () => void;
}

const emptyRow = {
  provider: "",
  model: "",
  input_per_1m: 0,
  output_per_1m: 0,
  cached_per_1m: 0,
  per_request: 0,
  currency: "USD",
};

export default function PricingTable({ onClose }: Props) {
  const prices = useCostStore((s) => s.prices);
  const savePriceEntry = useCostStore((s) => s.savePriceEntry);
  const deletePriceEntry = useCostStore((s) => s.deletePriceEntry);
  const [newRow, setNewRow] = useState(emptyRow);
  const [saving, setSaving] = useState(false);

  const handleSave = async () => {
    if (!newRow.provider || !newRow.model) return;
    setSaving(true);
    await savePriceEntry(newRow);
    setNewRow(emptyRow);
    setSaving(false);
  };

  const handleDelete = async (price: ModelPrice) => {
    if (!confirm(`Delete pricing for ${price.provider}/${price.model}?`)) return;
    await deletePriceEntry(price.id);
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="pricing-modal" onClick={(e) => e.stopPropagation()}>
        <div className="pricing-modal-header">
          <h2>EDIT PRICING</h2>
          <button className="pricing-close-btn" onClick={onClose}>
            ×
          </button>
        </div>
        <p className="pricing-hint">
          Updating a price creates a new time window. Historical costs remain
          calculated at the rate that was active when the usage occurred.
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
                <th>Per Request $</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {prices.map((p) => (
                <tr key={p.id}>
                  <td>{p.provider}</td>
                  <td>{p.model}</td>
                  <td>${p.input_per_1m}</td>
                  <td>${p.output_per_1m}</td>
                  <td>${p.cached_per_1m}</td>
                  <td>{p.per_request > 0 ? `$${p.per_request}` : "—"}</td>
                  <td>
                    <button
                      className="pricing-delete-btn"
                      onClick={() => handleDelete(p)}
                    >
                      ✕
                    </button>
                  </td>
                </tr>
              ))}

              {/* New row */}
              <tr className="pricing-new-row">
                <td>
                  <input
                    value={newRow.provider}
                    onChange={(e) =>
                      setNewRow({ ...newRow, provider: e.target.value })
                    }
                    placeholder="openai"
                  />
                </td>
                <td>
                  <input
                    value={newRow.model}
                    onChange={(e) =>
                      setNewRow({ ...newRow, model: e.target.value })
                    }
                    placeholder="gpt-4o"
                  />
                </td>
                <td>
                  <input
                    type="number"
                    step="0.01"
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
                  <button
                    className="pricing-save-btn"
                    onClick={handleSave}
                    disabled={saving || !newRow.provider || !newRow.model}
                  >
                    {saving ? "..." : "+"}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
