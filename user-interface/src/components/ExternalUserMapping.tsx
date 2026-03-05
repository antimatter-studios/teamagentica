import { useEffect, useState } from "react";
import {
  fetchExternalUsers,
  createExternalUser,
  deleteExternalUser,
  type ExternalUserMapping as Mapping,
} from "../api/costs";
import { useCostStore } from "../stores/costStore";

interface Props {
  onClose: () => void;
}

export default function ExternalUserMapping({ onClose }: Props) {
  const users = useCostStore((s) => s.users);
  const [mappings, setMappings] = useState<Mapping[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // New mapping form.
  const [newExternalID, setNewExternalID] = useState("");
  const [newSource, setNewSource] = useState("telegram");
  const [newTeamagenticaUserID, setNewTeamagenticaUserID] = useState("");
  const [newLabel, setNewLabel] = useState("");
  const [saving, setSaving] = useState(false);

  const loadMappings = async () => {
    try {
      const resp = await fetchExternalUsers();
      setMappings(resp.mappings || []);
      setLoading(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load mappings");
      setLoading(false);
    }
  };

  useEffect(() => {
    loadMappings();
  }, []);

  // Find unmapped external user IDs (in cost data but not in mappings).
  const mappedIDs = new Set(mappings.map((m) => `${m.source}:${m.external_id}`));
  const unmapped = users.filter((u) => {
    // External IDs have format "source:id" (e.g. "telegram:123456").
    if (!u.user_id.includes(":")) return false;
    const [source, extID] = u.user_id.split(":", 2);
    return !mappedIDs.has(`${source}:${extID}`);
  });

  const handleCreate = async () => {
    if (!newExternalID || !newTeamagenticaUserID) return;
    setSaving(true);
    try {
      await createExternalUser({
        external_id: newExternalID,
        source: newSource,
        teamagentica_user_id: parseInt(newTeamagenticaUserID, 10),
        label: newLabel || undefined,
      });
      setNewExternalID("");
      setNewTeamagenticaUserID("");
      setNewLabel("");
      await loadMappings();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create mapping");
    }
    setSaving(false);
  };

  const handleDelete = async (id: number) => {
    try {
      await deleteExternalUser(id);
      await loadMappings();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete mapping");
    }
  };

  const handleQuickMap = (userID: string) => {
    const [source, extID] = userID.split(":", 2);
    setNewSource(source);
    setNewExternalID(extID);
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-content" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 700 }}>
        <div className="modal-header">
          <h2>External User Mapping</h2>
          <button className="modal-close" onClick={onClose}>&times;</button>
        </div>

        {error && <div className="cost-error" style={{ margin: "8px 0" }}>{error}</div>}

        {/* Unmapped users */}
        {unmapped.length > 0 && (
          <div style={{ marginBottom: 16 }}>
            <h4 style={{ color: "#9ca3af", fontSize: 12, marginBottom: 8 }}>UNMAPPED EXTERNAL USERS</h4>
            <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
              {unmapped.map((u) => (
                <button
                  key={u.user_id}
                  className="cost-edit-pricing-btn"
                  style={{ fontSize: 11, padding: "3px 8px" }}
                  onClick={() => handleQuickMap(u.user_id)}
                >
                  {u.user_id} ({u.count})
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Add new mapping */}
        <div style={{ display: "flex", gap: 8, marginBottom: 16, alignItems: "flex-end" }}>
          <div>
            <label style={{ fontSize: 11, color: "#9ca3af" }}>Source</label>
            <select
              className="cost-granularity-select"
              value={newSource}
              onChange={(e) => setNewSource(e.target.value)}
              style={{ display: "block", marginTop: 2 }}
            >
              <option value="telegram">telegram</option>
              <option value="discord">discord</option>
              <option value="whatsapp">whatsapp</option>
            </select>
          </div>
          <div>
            <label style={{ fontSize: 11, color: "#9ca3af" }}>External ID</label>
            <input
              className="pricing-input"
              placeholder="123456789"
              value={newExternalID}
              onChange={(e) => setNewExternalID(e.target.value)}
              style={{ display: "block", marginTop: 2 }}
            />
          </div>
          <div>
            <label style={{ fontSize: 11, color: "#9ca3af" }}>Teamagentica User ID</label>
            <input
              className="pricing-input"
              placeholder="7"
              value={newTeamagenticaUserID}
              onChange={(e) => setNewTeamagenticaUserID(e.target.value)}
              style={{ display: "block", marginTop: 2 }}
            />
          </div>
          <div>
            <label style={{ fontSize: 11, color: "#9ca3af" }}>Label</label>
            <input
              className="pricing-input"
              placeholder="optional"
              value={newLabel}
              onChange={(e) => setNewLabel(e.target.value)}
              style={{ display: "block", marginTop: 2 }}
            />
          </div>
          <button
            className="cost-edit-pricing-btn"
            onClick={handleCreate}
            disabled={saving || !newExternalID || !newTeamagenticaUserID}
          >
            {saving ? "..." : "ADD"}
          </button>
        </div>

        {/* Existing mappings */}
        {loading ? (
          <div style={{ color: "#9ca3af" }}>Loading...</div>
        ) : mappings.length === 0 ? (
          <div style={{ color: "#9ca3af" }}>No mappings configured yet.</div>
        ) : (
          <div className="cost-table-wrapper">
            <table className="cost-table">
              <thead>
                <tr>
                  <th>Source</th>
                  <th>External ID</th>
                  <th>Teamagentica User</th>
                  <th>Label</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {mappings.map((m) => (
                  <tr key={m.id}>
                    <td>{m.source}</td>
                    <td>{m.external_id}</td>
                    <td>{m.teamagentica_user_id}</td>
                    <td>{m.label}</td>
                    <td>
                      <button
                        className="cost-edit-pricing-btn"
                        style={{ fontSize: 10, padding: "2px 6px", background: "#7f1d1d" }}
                        onClick={() => handleDelete(m.id)}
                      >
                        DEL
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
