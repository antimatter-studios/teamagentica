import { useState, useEffect, useRef, useCallback } from "react";
import { useShallow } from "zustand/react/shallow";
import LoginForm from "./components/LoginForm";
import Dashboard from "./components/Dashboard";
import Chat from "./components/Chat";
import FileBrowser from "./components/FileBrowser";
import Marketplace from "./components/Marketplace";
import PluginSettings from "./components/PluginSettings";
import DebugConsole from "./components/DebugConsole";
import CostDashboard from "./components/CostDashboard";
import { useAuthStore } from "./stores/authStore";
import { searchPlugins } from "./api/plugins";
import { useEventStore } from "./stores/eventStore";

type Page = "dashboard" | "chat" | "files" | "marketplace" | "plugins" | "costs" | "console";

// Plugin lifecycle event types that can change which capabilities are available.
const PLUGIN_LIFECYCLE_EVENTS = new Set([
  "register", "deregister", "enable", "disable",
  "start", "stop", "install", "uninstall", "restart",
]);

export default function App() {
  const { authenticated, user } = useAuthStore(
    useShallow((s) => ({ authenticated: s.authenticated, user: s.user }))
  );
  const logout = useAuthStore((s) => s.logout);
  const fetchUser = useAuthStore((s) => s.fetchUser);
  const [page, setPage] = useState<Page>("dashboard");
  const [hasChat, setHasChat] = useState(false);
  const events = useEventStore((s) => s.auditEvents);
  const connectEvents = useEventStore((s) => s.connect);
  const disconnectEvents = useEventStore((s) => s.disconnect);

  const checkCapabilities = useCallback(() => {
    searchPlugins("system:chat")
      .then((p) => setHasChat(p.length > 0))
      .catch(() => {});
  }, []);

  // Connect SSE event stream and run initial capability check on auth.
  useEffect(() => {
    if (authenticated) {
      fetchUser();
      connectEvents();
      checkCapabilities();
    } else {
      disconnectEvents();
      setHasChat(false);
    }
    return () => disconnectEvents();
  }, [authenticated, fetchUser, connectEvents, disconnectEvents, checkCapabilities]);

  // Re-check capabilities when plugin lifecycle events arrive.
  const lastEventCount = useRef(0);
  useEffect(() => {
    if (events.length <= lastEventCount.current) {
      lastEventCount.current = events.length;
      return;
    }
    // Only inspect new events since last check.
    const newEvents = events.slice(lastEventCount.current);
    lastEventCount.current = events.length;
    if (newEvents.some((e) => PLUGIN_LIFECYCLE_EVENTS.has(e.type))) {
      checkCapabilities();
    }
  }, [events, checkCapabilities]);

  // If user is on the chat page and chat becomes unavailable, redirect to dashboard.
  useEffect(() => {
    if (!hasChat && page === "chat") {
      setPage("dashboard");
    }
  }, [hasChat, page]);

  const canAccessPlugins = user?.role === "admin";

  if (!authenticated) {
    return <LoginForm />;
  }

  return (
    <div className="app-shell">
      <header className="dashboard-header">
        <div className="header-brand">
          <h1 className="header-title">{(import.meta.env.VITE_APP_NAME || "TeamAgentica").toUpperCase()}</h1>
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
          {hasChat && (
            <button
              className={`nav-tab ${page === "chat" ? "active" : ""}`}
              onClick={() => setPage("chat")}
            >
              CHAT
            </button>
          )}
          <button
            className={`nav-tab ${page === "files" ? "active" : ""}`}
            onClick={() => setPage("files")}
          >
            FILES
          </button>
          {canAccessPlugins && (
            <button
              className={`nav-tab ${page === "marketplace" ? "active" : ""}`}
              onClick={() => setPage("marketplace")}
            >
              MARKETPLACE
            </button>
          )}
          {canAccessPlugins && (
            <button
              className={`nav-tab ${page === "plugins" ? "active" : ""}`}
              onClick={() => setPage("plugins")}
            >
              PLUGINS
            </button>
          )}
          {canAccessPlugins && (
            <button
              className={`nav-tab ${page === "costs" ? "active" : ""}`}
              onClick={() => setPage("costs")}
            >
              COSTS
            </button>
          )}
          {canAccessPlugins && (
            <button
              className={`nav-tab ${page === "console" ? "active" : ""}`}
              onClick={() => setPage("console")}
            >
              CONSOLE
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
          <button className="logout-btn" onClick={logout}>
            DISCONNECT
          </button>
        </div>
      </header>

      {page === "dashboard" && <Dashboard />}
      {page === "chat" && hasChat && <Chat />}
      {page === "files" && <FileBrowser />}
      {page === "marketplace" && <Marketplace />}
      {page === "plugins" && <PluginSettings />}
      {page === "costs" && <CostDashboard />}
      {page === "console" && <DebugConsole />}
    </div>
  );
}
