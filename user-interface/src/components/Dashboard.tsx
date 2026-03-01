import { useEffect, useState } from "react";
import { getMe, getUsers, type User } from "../api/auth";

interface Props {
  onLogout: () => void;
}

export default function Dashboard({ onLogout }: Props) {
  const [user, setUser] = useState<User | null>(null);
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function load() {
      try {
        const me = await getMe();
        setUser(me);
        if (me.role === "admin") {
          const allUsers = await getUsers();
          setUsers(allUsers);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  if (loading) {
    return (
      <div className="dashboard-content">
        <div className="loading-screen">
          <div className="spinner large" />
          <p>LOADING SYSTEM DATA...</p>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="dashboard-content">
        <div className="loading-screen">
          <p className="form-error">{error}</p>
          <button className="login-submit" onClick={onLogout}>
            RETURN TO LOGIN
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="dashboard-content">
      <main className="dashboard-main">
        <section className="dashboard-section">
          <h2 className="section-title">
            <span className="section-icon">&gt;_</span>
            OPERATOR PROFILE
          </h2>
          <div className="info-grid">
            <InfoCard label="ID" value={user?.id || "---"} />
            <InfoCard label="EMAIL" value={user?.email || "---"} />
            <InfoCard
              label="DISPLAY NAME"
              value={user?.display_name || "---"}
            />
            <InfoCard
              label="ROLE"
              value={user?.role?.toUpperCase() || "---"}
            />
            <InfoCard
              label="CREATED"
              value={
                user?.created_at
                  ? new Date(user.created_at).toLocaleString()
                  : "---"
              }
            />
          </div>
        </section>

        {user?.role === "admin" && users.length > 0 && (
          <section className="dashboard-section">
            <h2 className="section-title">
              <span className="section-icon">[#]</span>
              REGISTERED OPERATORS
              <span className="section-count">{users.length}</span>
            </h2>
            <div className="users-table-wrapper">
              <table className="users-table">
                <thead>
                  <tr>
                    <th>EMAIL</th>
                    <th>DISPLAY NAME</th>
                    <th>ROLE</th>
                    <th>CREATED</th>
                  </tr>
                </thead>
                <tbody>
                  {users.map((u) => (
                    <tr key={u.id}>
                      <td>{u.email}</td>
                      <td>{u.display_name}</td>
                      <td>
                        <span className={`user-role role-${u.role}`}>
                          {u.role.toUpperCase()}
                        </span>
                      </td>
                      <td>{new Date(u.created_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        )}
      </main>
    </div>
  );
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="info-card">
      <span className="info-label">{label}</span>
      <span className="info-value">{value}</span>
    </div>
  );
}
