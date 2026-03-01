import { useState, useCallback, useEffect } from "react";
import LoginForm from "./components/LoginForm";
import Dashboard from "./components/Dashboard";
import PluginList from "./components/PluginList";
import { getStoredToken, clearToken, getMe, type User } from "./api/auth";

type Page = "dashboard" | "plugins";

export default function App() {
  const [authenticated, setAuthenticated] = useState(!!getStoredToken());
  const [page, setPage] = useState<Page>("dashboard");
  const [user, setUser] = useState<User | null>(null);

  const handleLogin = useCallback(() => {
    setAuthenticated(true);
  }, []);

  const handleLogout = useCallback(() => {
    clearToken();
    setAuthenticated(false);
    setUser(null);
    setPage("dashboard");
  }, []);

  // Fetch user for role checking
  useEffect(() => {
    if (!authenticated) return;
    getMe()
      .then(setUser)
      .catch(() => {
        /* handled by Dashboard */
      });
  }, [authenticated]);

  // Show plugins tab for admins; extend with capability checks as needed
  const canAccessPlugins = user?.role === "admin";

  if (!authenticated) {
    return <LoginForm onLogin={handleLogin} />;
  }

  return (
    <div className="app-shell">
      <header className="dashboard-header">
        <div className="header-brand">
          <h1 className="header-title">ROBOSLOP</h1>
          <span className="header-divider" />
          <span className="header-subtitle">CONTROL PANEL</span>
        </div>

        <nav className="nav-tabs">
          <button
            className={`nav-tab ${page === "dashboard" ? "active" : ""}`}
            onClick={() => setPage("dashboard")}
          >
            DASHBOARD
          </button>
          {canAccessPlugins && (
            <button
              className={`nav-tab ${page === "plugins" ? "active" : ""}`}
              onClick={() => setPage("plugins")}
            >
              PLUGINS
            </button>
          )}
        </nav>

        <div className="header-right">
          <div className="header-user">
            <span className="user-email">{user?.email}</span>
            <span className={`user-role role-${user?.role}`}>
              {user?.role?.toUpperCase()}
            </span>
          </div>
          <button className="logout-btn" onClick={handleLogout}>
            DISCONNECT
          </button>
        </div>
      </header>

      {page === "dashboard" && <Dashboard onLogout={handleLogout} />}
      {page === "plugins" && <PluginList />}
    </div>
  );
}
