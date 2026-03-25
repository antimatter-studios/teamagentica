import { useEffect, useState, useCallback } from "react";
import { apiClient } from "../api/client";
import type { UserDetails, ServiceToken, AuditLogEntry } from "@teamagentica/api-client";

type View = "users" | "tokens" | "audit" | "new-user" | "new-token" | "edit-user" | "user-detail";

export default function Users() {
  const [view, setView] = useState<View>("users");
  const [users, setUsers] = useState<UserDetails[]>([]);
  const [tokens, setTokens] = useState<ServiceToken[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLogEntry[]>([]);
  const [auditTotal, setAuditTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  // --- Selected items ---
  const [selectedUser, setSelectedUser] = useState<UserDetails | null>(null);
  const [selectedToken, setSelectedToken] = useState<ServiceToken | null>(null);

  // --- Edit user ---
  const [editDisplayName, setEditDisplayName] = useState("");
  const [editRole, setEditRole] = useState("");

  // --- New user ---
  const [newEmail, setNewEmail] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [newDisplayName, setNewDisplayName] = useState("");

  // --- New service token ---
  const [tokenName, setTokenName] = useState("");
  const [tokenCaps, setTokenCaps] = useState<string[]>(["plugins:search"]);
  const [tokenDays, setTokenDays] = useState(365);
  const [createdToken, setCreatedToken] = useState("");

  // --- Ban modal ---
  const [banTarget, setBanTarget] = useState<UserDetails | null>(null);
  const [banReason, setBanReason] = useState("");

  const fetchUsers = useCallback(async () => {
    try {
      const list = await apiClient.users.listUsers();
      setUsers(list);
    } catch (e: any) {
      setError(e.message);
    }
  }, []);

  const fetchTokens = useCallback(async () => {
    try {
      const list = await apiClient.users.listServiceTokens();
      setTokens(list);
    } catch (e: any) {
      setError(e.message);
    }
  }, []);

  const fetchAudit = useCallback(async () => {
    try {
      const res = await apiClient.users.listAuditLogs({ limit: 100 });
      setAuditLogs(res.logs);
      setAuditTotal(res.total);
    } catch (e: any) {
      setError(e.message);
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    Promise.all([fetchUsers(), fetchTokens(), fetchAudit()]).finally(() => setLoading(false));
  }, [fetchUsers, fetchTokens, fetchAudit]);

  // --- User actions ---
  const handleEditUser = (u: UserDetails) => {
    setSelectedUser(u);
    setEditDisplayName(u.display_name);
    setEditRole(u.role);
    setView("edit-user");
  };

  const handleSaveUser = async () => {
    if (!selectedUser) return;
    try {
      setError("");
      await apiClient.users.updateUser(Number(selectedUser.id), {
        display_name: editDisplayName,
        role: editRole,
      });
      setView("users");
      setSelectedUser(null);
      fetchUsers();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleBanUser = async () => {
    if (!banTarget) return;
    try {
      setError("");
      await apiClient.users.banUser(Number(banTarget.id), !banTarget.banned, banReason);
      setBanTarget(null);
      setBanReason("");
      fetchUsers();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDeleteUser = async (u: UserDetails) => {
    if (!confirm(`Delete user ${u.email}? This cannot be undone.`)) return;
    try {
      setError("");
      await apiClient.users.deleteUser(Number(u.id));
      if (selectedUser?.id === u.id) {
        setSelectedUser(null);
        setView("users");
      }
      fetchUsers();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleCreateUser = async () => {
    try {
      setError("");
      await apiClient.auth.register(newEmail, newPassword, newDisplayName);
      setNewEmail("");
      setNewPassword("");
      setNewDisplayName("");
      setView("users");
      fetchUsers();
    } catch (e: any) {
      setError(e.message);
    }
  };

  // --- Token actions ---
  const handleCreateToken = async () => {
    try {
      setError("");
      const res = await apiClient.users.createServiceToken(tokenName, tokenCaps, tokenDays);
      setCreatedToken(res.token);
      setTokenName("");
      fetchTokens();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleRevokeToken = async (id: number) => {
    if (!confirm("Revoke this service token?")) return;
    try {
      setError("");
      await apiClient.users.revokeServiceToken(id);
      fetchTokens();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const toggleCap = (cap: string) => {
    setTokenCaps((prev) =>
      prev.includes(cap) ? prev.filter((c) => c !== cap) : [...prev, cap]
    );
  };

  const selectUserDetail = (u: UserDetails) => {
    setSelectedUser(u);
    setView("user-detail");
  };

  if (loading) {
    return (
      <div className="plugin-settings">
        <div className="plugin-loading">Loading user data…</div>
      </div>
    );
  }

  return (
    <div className="um-layout">
      {/* ===== LEFT SIDEBAR ===== */}
      <div className="um-sidebar">
        <div className="um-sidebar-scroll">
          {/* Users group */}
          <div className="um-sidebar-group">
            <div className="um-sidebar-group-header">
              <span className="um-sidebar-group-name">Users</span>
              <span className="um-sidebar-count">{users.length}</span>
            </div>
            <button
              className="um-sidebar-add"
              onClick={() => { setView("new-user"); setError(""); }}
            >
              + Add User
            </button>
            {users.map((u) => (
              <button
                key={u.id}
                className={`um-sidebar-item ${view === "user-detail" && selectedUser?.id === u.id ? "active" : ""}`}
                onClick={() => selectUserDetail(u)}
              >
                <span className="um-sidebar-item-avatar">
                  {(u.display_name || u.email).charAt(0).toUpperCase()}
                </span>
                <span className="um-sidebar-item-info">
                  <span className="um-sidebar-item-name">{u.display_name || u.email.split("@")[0]}</span>
                  <span className="um-sidebar-item-meta">{u.email}</span>
                </span>
                <span className={`um-sidebar-item-dot ${u.banned ? "banned" : "active"}`} />
              </button>
            ))}
          </div>

          {/* Service Accounts group */}
          <div className="um-sidebar-group">
            <div className="um-sidebar-group-header">
              <span className="um-sidebar-group-name">Service Accounts</span>
              <span className="um-sidebar-count">{tokens.length}</span>
            </div>
            <button
              className="um-sidebar-add"
              onClick={() => { setView("new-token"); setCreatedToken(""); setError(""); }}
            >
              + Add Service Account
            </button>
            {tokens.map((t) => (
              <button
                key={t.id}
                className={`um-sidebar-item ${selectedToken?.id === t.id ? "active" : ""}`}
                onClick={() => { setSelectedToken(t); setView("tokens"); }}
              >
                <span className="um-sidebar-item-avatar svc">S</span>
                <span className="um-sidebar-item-info">
                  <span className="um-sidebar-item-name">{t.name}</span>
                  <span className="um-sidebar-item-meta">
                    {t.revoked ? "Revoked" : `Expires ${new Date(t.expires_at).toLocaleDateString()}`}
                  </span>
                </span>
                <span className={`um-sidebar-item-dot ${t.revoked ? "banned" : "active"}`} />
              </button>
            ))}
          </div>
        </div>

        {/* Audit log at bottom */}
        <div className="um-sidebar-footer">
          <button
            className={`um-sidebar-footer-btn ${view === "audit" ? "active" : ""}`}
            onClick={() => { setView("audit"); fetchAudit(); }}
          >
            <span className="um-sidebar-footer-icon">&#x1D56;</span>
            Audit Log
            {auditTotal > 0 && <span className="um-sidebar-count">{auditTotal}</span>}
          </button>
        </div>
      </div>

      {/* ===== MAIN CONTENT ===== */}
      <div className="um-content">
        {error && <div className="plugin-error" style={{ margin: "16px 24px 0" }}>{error}</div>}

        {/* --- Users list (default) --- */}
        {view === "users" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Users</h2>
            </div>
            <table className="cost-table">
              <thead>
                <tr>
                  <th>ID</th>
                  <th>Email</th>
                  <th>Display Name</th>
                  <th>Role</th>
                  <th>Status</th>
                  <th>Created</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.id} className="um-table-row" onClick={() => selectUserDetail(u)}>
                    <td>{u.id}</td>
                    <td>{u.email}</td>
                    <td>{u.display_name || "—"}</td>
                    <td>
                      <span className={`user-role role-${u.role}`}>{u.role.toUpperCase()}</span>
                    </td>
                    <td>
                      {u.banned ? (
                        <span style={{ color: "var(--error)" }}>BANNED</span>
                      ) : (
                        <span style={{ color: "var(--success)" }}>Active</span>
                      )}
                    </td>
                    <td>{new Date(u.created_at).toLocaleDateString()}</td>
                    <td>
                      <div style={{ display: "flex", gap: 4 }} onClick={(e) => e.stopPropagation()}>
                        <button className="plugin-action-btn" onClick={() => handleEditUser(u)}>Edit</button>
                        <button
                          className="plugin-action-btn"
                          onClick={() => { setBanTarget(u); setBanReason(u.ban_reason || ""); }}
                          style={{ color: u.banned ? "var(--success)" : "var(--warning, orange)" }}
                        >
                          {u.banned ? "Unban" : "Ban"}
                        </button>
                        <button
                          className="plugin-action-btn"
                          onClick={() => handleDeleteUser(u)}
                          style={{ color: "var(--error)" }}
                        >
                          Delete
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* --- User detail --- */}
        {view === "user-detail" && selectedUser && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">{selectedUser.display_name || selectedUser.email}</h2>
              <div className="um-panel-actions">
                <button className="plugin-action-btn" onClick={() => handleEditUser(selectedUser)}>Edit</button>
                <button
                  className="plugin-action-btn"
                  onClick={() => { setBanTarget(selectedUser); setBanReason(selectedUser.ban_reason || ""); }}
                  style={{ color: selectedUser.banned ? "var(--success)" : "var(--warning, orange)" }}
                >
                  {selectedUser.banned ? "Unban" : "Ban"}
                </button>
                <button
                  className="plugin-action-btn"
                  onClick={() => handleDeleteUser(selectedUser)}
                  style={{ color: "var(--error)" }}
                >
                  Delete
                </button>
              </div>
            </div>
            <div className="um-detail-grid">
              <div className="um-detail-field">
                <span className="um-detail-label">Email</span>
                <span className="um-detail-value">{selectedUser.email}</span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Display Name</span>
                <span className="um-detail-value">{selectedUser.display_name || "—"}</span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Role</span>
                <span className="um-detail-value">
                  <span className={`user-role role-${selectedUser.role}`}>{selectedUser.role.toUpperCase()}</span>
                </span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Status</span>
                <span className="um-detail-value">
                  {selectedUser.banned ? (
                    <span style={{ color: "var(--error)" }}>BANNED{selectedUser.ban_reason ? ` — ${selectedUser.ban_reason}` : ""}</span>
                  ) : (
                    <span style={{ color: "var(--success)" }}>Active</span>
                  )}
                </span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Created</span>
                <span className="um-detail-value">{new Date(selectedUser.created_at).toLocaleString()}</span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Updated</span>
                <span className="um-detail-value">{new Date(selectedUser.updated_at).toLocaleString()}</span>
              </div>
            </div>
          </div>
        )}

        {/* --- Edit user form --- */}
        {view === "edit-user" && selectedUser && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Edit User</h2>
            </div>
            <div className="um-form">
              <div className="um-form-field">
                <label className="um-form-label">Email</label>
                <div className="um-form-static">{selectedUser.email}</div>
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Display Name</label>
                <input
                  className="um-form-input"
                  value={editDisplayName}
                  onChange={(e) => setEditDisplayName(e.target.value)}
                  placeholder="Enter display name"
                />
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Role</label>
                <select
                  className="um-form-input"
                  value={editRole}
                  onChange={(e) => setEditRole(e.target.value)}
                >
                  <option value="admin">Admin</option>
                  <option value="user">User</option>
                </select>
              </div>
              <div className="um-form-actions">
                <button className="um-btn um-btn-secondary" onClick={() => { setView("user-detail"); }}>Cancel</button>
                <button className="um-btn um-btn-primary" onClick={handleSaveUser}>Save Changes</button>
              </div>
            </div>
          </div>
        )}

        {/* --- New user form --- */}
        {view === "new-user" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Create User</h2>
            </div>
            <div className="um-form">
              <div className="um-form-field">
                <label className="um-form-label">Email</label>
                <input
                  className="um-form-input"
                  type="email"
                  value={newEmail}
                  onChange={(e) => setNewEmail(e.target.value)}
                  placeholder="user@example.com"
                />
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Display Name</label>
                <input
                  className="um-form-input"
                  value={newDisplayName}
                  onChange={(e) => setNewDisplayName(e.target.value)}
                  placeholder="John Doe"
                />
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Password</label>
                <input
                  className="um-form-input"
                  type="password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder="Minimum 8 characters"
                />
                {newPassword.length > 0 && newPassword.length < 8 && (
                  <span className="um-form-hint um-form-hint-error">Password must be at least 8 characters</span>
                )}
              </div>
              <div className="um-form-actions">
                <button className="um-btn um-btn-secondary" onClick={() => setView("users")}>Cancel</button>
                <button
                  className="um-btn um-btn-primary"
                  onClick={handleCreateUser}
                  disabled={!newEmail || newPassword.length < 8}
                >
                  Create User
                </button>
              </div>
            </div>
          </div>
        )}

        {/* --- Token detail --- */}
        {view === "tokens" && selectedToken && (() => {
          let caps: string[] = [];
          try { caps = JSON.parse(selectedToken.capabilities); } catch { /* */ }
          return (
            <div className="um-panel">
              <div className="um-panel-header">
                <h2 className="um-panel-title">{selectedToken.name}</h2>
                <div className="um-panel-actions">
                  {!selectedToken.revoked && (
                    <button
                      className="plugin-action-btn"
                      onClick={() => handleRevokeToken(selectedToken.id)}
                      style={{ color: "var(--error)" }}
                    >
                      Revoke
                    </button>
                  )}
                </div>
              </div>
              <div className="um-detail-grid">
                <div className="um-detail-field">
                  <span className="um-detail-label">Status</span>
                  <span className="um-detail-value">
                    {selectedToken.revoked ? (
                      <span style={{ color: "var(--error)" }}>REVOKED</span>
                    ) : (
                      <span style={{ color: "var(--success)" }}>Active</span>
                    )}
                  </span>
                </div>
                <div className="um-detail-field">
                  <span className="um-detail-label">Expires</span>
                  <span className="um-detail-value">{new Date(selectedToken.expires_at).toLocaleString()}</span>
                </div>
                <div className="um-detail-field">
                  <span className="um-detail-label">Capabilities</span>
                  <div className="um-caps-list">
                    {caps.map((c) => (
                      <span key={c} className="um-cap-badge">{c}</span>
                    ))}
                  </div>
                </div>
              </div>
            </div>
          );
        })()}

        {/* --- New service token form --- */}
        {view === "new-token" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Create Service Account</h2>
            </div>
            {createdToken ? (
              <div className="um-form">
                <div className="um-token-created">
                  <div className="um-token-created-label">Token created — copy now, it won't be shown again:</div>
                  <div className="um-token-created-value">{createdToken}</div>
                </div>
                <div className="um-form-actions">
                  <button className="um-btn um-btn-secondary" onClick={() => { setCreatedToken(""); setView("users"); }}>
                    Done
                  </button>
                </div>
              </div>
            ) : (
              <div className="um-form">
                <div className="um-form-field">
                  <label className="um-form-label">Token Name</label>
                  <input
                    className="um-form-input"
                    value={tokenName}
                    onChange={(e) => setTokenName(e.target.value)}
                    placeholder="e.g. CI Pipeline, Monitoring Bot"
                  />
                </div>
                <div className="um-form-field">
                  <label className="um-form-label">Expiry (days)</label>
                  <input
                    className="um-form-input"
                    type="number"
                    value={tokenDays}
                    onChange={(e) => setTokenDays(Number(e.target.value))}
                    min={1}
                  />
                </div>
                <div className="um-form-field">
                  <label className="um-form-label">Capabilities</label>
                  <div className="um-caps-grid">
                    {["plugins:search", "plugins:manage", "users:read", "system:admin"].map((cap) => (
                      <label key={cap} className="um-cap-check">
                        <input
                          type="checkbox"
                          className="um-checkbox"
                          checked={tokenCaps.includes(cap)}
                          onChange={() => toggleCap(cap)}
                        />
                        <span>{cap}</span>
                      </label>
                    ))}
                  </div>
                </div>
                <div className="um-form-actions">
                  <button className="um-btn um-btn-secondary" onClick={() => setView("users")}>Cancel</button>
                  <button
                    className="um-btn um-btn-primary"
                    onClick={handleCreateToken}
                    disabled={!tokenName || tokenCaps.length === 0}
                  >
                    Create Token
                  </button>
                </div>
              </div>
            )}
          </div>
        )}

        {/* --- Audit log --- */}
        {view === "audit" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Audit Log</h2>
              <div className="um-panel-actions">
                <button className="plugin-action-btn" onClick={fetchAudit}>Refresh</button>
              </div>
            </div>
            <table className="cost-table">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Action</th>
                  <th>Actor</th>
                  <th>Resource</th>
                  <th>IP</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {auditLogs.map((log) => (
                  <tr key={log.id}>
                    <td style={{ whiteSpace: "nowrap", fontSize: 11 }}>
                      {new Date(log.timestamp).toLocaleString()}
                    </td>
                    <td>
                      <span className="um-cap-badge">{log.action}</span>
                    </td>
                    <td style={{ fontSize: 12 }}>{log.actor_type}:{log.actor_id}</td>
                    <td style={{ fontSize: 12 }}>{log.resource || "—"}</td>
                    <td style={{ fontSize: 11, color: "var(--text-muted)" }}>{log.ip || "—"}</td>
                    <td>
                      {log.success ? (
                        <span style={{ color: "var(--success)", fontSize: 11 }}>OK</span>
                      ) : (
                        <span style={{ color: "var(--error)", fontSize: 11 }}>FAIL</span>
                      )}
                    </td>
                  </tr>
                ))}
                {auditLogs.length === 0 && (
                  <tr><td colSpan={6} style={{ textAlign: "center", color: "var(--text-muted)" }}>No audit logs</td></tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* ===== BAN MODAL ===== */}
      {banTarget && (
        <div className="modal-overlay" onClick={() => setBanTarget(null)}>
          <div className="modal-content" onClick={(e) => e.stopPropagation()} style={{ maxWidth: 400 }}>
            <h3 style={{ margin: "0 0 16px", color: "var(--text-primary)" }}>
              {banTarget.banned ? "Unban" : "Ban"} User: {banTarget.email}
            </h3>
            {!banTarget.banned && (
              <div className="um-form-field">
                <label className="um-form-label">Reason</label>
                <input
                  className="um-form-input"
                  value={banReason}
                  onChange={(e) => setBanReason(e.target.value)}
                  placeholder="Optional ban reason"
                />
              </div>
            )}
            <div style={{ display: "flex", gap: 8, marginTop: 16, justifyContent: "flex-end" }}>
              <button className="um-btn um-btn-secondary" onClick={() => setBanTarget(null)}>Cancel</button>
              <button
                className="um-btn"
                onClick={handleBanUser}
                style={{
                  background: banTarget.banned ? "var(--success)" : "var(--error)",
                  color: "#fff",
                  border: "none",
                }}
              >
                {banTarget.banned ? "Unban" : "Ban User"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
