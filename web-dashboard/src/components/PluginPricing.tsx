import { useEffect, useState } from "react";
import { apiClient } from "../api/client";

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
      <div className="plugin-pricing">
        <div className="spinner" /> Loading pricing...
      </div>
    );
  }

  return (
    <div className="plugin-pricing">
      <div className="pricing-header-row">
        <h3 className="pricing-section-title">MODEL PRICING</h3>
        <div className="pricing-actions">
          <button className="plugin-action-btn" onClick={addRow}>
            + ADD MODEL
          </button>
          <button
            className="plugin-action-btn btn-success"
            onClick={handleSave}
            disabled={saving || !hasChanges}
          >
            {saving ? "SAVING..." : "SAVE"}
          </button>
        </div>
      </div>

      <p className="pricing-hint">
        Edit prices here. Changes are pushed to the kernel and create a new
        pricing window for accurate historical cost tracking.
      </p>

      {error && <div className="form-error">{error}</div>}
      {success && <div className="form-success">{success}</div>}

      {editing.length === 0 ? (
        <div className="pricing-empty">
          No pricing configured. Click "+ ADD MODEL" to add pricing for this
          plugin's models.
        </div>
      ) : (
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
              {editing.map((row, idx) => (
                <tr key={idx}>
                  <td>
                    <input
                      value={row.provider}
                      onChange={(e) =>
                        updateField(idx, "provider", e.target.value)
                      }
                      placeholder="openai"
                    />
                  </td>
                  <td>
                    <input
                      value={row.model}
                      onChange={(e) =>
                        updateField(idx, "model", e.target.value)
                      }
                      placeholder="gpt-4o"
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      step="0.01"
                      value={row.input_per_1m || ""}
                      onChange={(e) =>
                        updateField(
                          idx,
                          "input_per_1m",
                          parseFloat(e.target.value) || 0
                        )
                      }
                      placeholder="0.00"
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      step="0.01"
                      value={row.output_per_1m || ""}
                      onChange={(e) =>
                        updateField(
                          idx,
                          "output_per_1m",
                          parseFloat(e.target.value) || 0
                        )
                      }
                      placeholder="0.00"
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      step="0.01"
                      value={row.cached_per_1m || ""}
                      onChange={(e) =>
                        updateField(
                          idx,
                          "cached_per_1m",
                          parseFloat(e.target.value) || 0
                        )
                      }
                      placeholder="0.00"
                    />
                  </td>
                  <td>
                    <input
                      type="number"
                      step="0.01"
                      value={row.per_request || ""}
                      onChange={(e) =>
                        updateField(
                          idx,
                          "per_request",
                          parseFloat(e.target.value) || 0
                        )
                      }
                      placeholder="0.00"
                    />
                  </td>
                  <td>
                    <button
                      className="pricing-delete-btn"
                      onClick={() => removeRow(idx)}
                    >
                      ✕
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
