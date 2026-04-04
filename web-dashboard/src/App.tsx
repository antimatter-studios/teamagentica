import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import LoginForm from "./components/LoginForm";
import Dashboard from "./components/Dashboard";
import Chat from "./components/Chat";
import FileBrowser from "./components/FileBrowser";
import Marketplace from "./components/Marketplace";
import PluginSettings from "./components/PluginSettings";
import DebugConsole from "./components/DebugConsole";
import CostDashboard from "./components/CostDashboard";
import CodeEditor from "./components/CodeEditor";
import KanbanBoard from "./components/KanbanBoard";
import Agents from "./components/Agents";
import Users from "./components/Users";
import TaskScheduler from "./components/TaskScheduler";
import MemoryExplorer from "./components/MemoryExplorer";
import { useAuthStore } from "./stores/authStore";
import { apiClient } from "./api/client";
import { useEventStore } from "./stores/eventStore";
import { useChatStore } from "./stores/chatStore";
import { useTheme } from "./hooks/useTheme";
import { useRouter, type Page } from "./hooks/useRouter";

// Plugin lifecycle event types that can change which capabilities are available.
const PLUGIN_LIFECYCLE_EVENTS = new Set([
  "register", "deregister", "enable", "disable",
  "start", "stop", "install", "uninstall", "restart",
]);

export default function App() {
  const { authenticated, sessionExpired, user } = useAuthStore(
    useShallow((s) => ({ authenticated: s.authenticated, sessionExpired: s.sessionExpired, user: s.user }))
  );
  const logout = useAuthStore((s) => s.logout);
  const dismissSessionExpired = useAuthStore((s) => s.dismissSessionExpired);
  const fetchUser = useAuthStore((s) => s.fetchUser);
  const { page, subpath, navigate: setPage, setSubpath, pushSubpath, setTitleSegment } = useRouter();
  const [hasChat, setHasChat] = useState(false);
  const [hasEditor, setHasEditor] = useState(false);
  const [hasTasks, setHasTasks] = useState(false);
  const [hasAgents, setHasAgents] = useState(false);
  const [hasScheduler, setHasScheduler] = useState(false);
  const [hasMemory, setHasMemory] = useState(false);
  const [capabilitiesLoaded, setCapabilitiesLoaded] = useState(false);
  const events = useEventStore((s) => s.auditEvents);
  const connectEvents = useEventStore((s) => s.connect);
  const disconnectEvents = useEventStore((s) => s.disconnect);
  const inFlightCount = useChatStore((s) => Object.keys(s.inFlightTasks).length);
  const totalUnread = useChatStore((s) => s.conversations.reduce((sum, c) => sum + (c.unread_count ?? 0), 0));

  const checkCapabilities = useCallback(() => {
    Promise.all([
      apiClient.plugins.search("messaging:chat").then((p) => setHasChat(p.length > 0)).catch(() => {}),
      apiClient.plugins.search("workspace:manager").then((p) => setHasEditor(p.length > 0)).catch(() => {}),
      apiClient.plugins.search("system:tasks").then((p) => setHasTasks(p.length > 0)).catch(() => {}),
      apiClient.plugins.search("tool:aliases").then((p) => setHasAgents(p.length > 0)).catch(() => {}),
      apiClient.plugins.search("infra:scheduler").then((p) => setHasScheduler(p.length > 0)).catch(() => {}),
      apiClient.plugins.search("tool:memory").then((p) => setHasMemory(p.length > 0)).catch(() => {}),
    ]).finally(() => setCapabilitiesLoaded(true));
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
      setHasEditor(false);
      setHasTasks(false);
      setHasAgents(false);
      setHasScheduler(false);
      setHasMemory(false);
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

  // If user is on a capability page and it becomes unavailable, redirect to dashboard.
  // Only check after capabilities have loaded at least once to avoid premature redirects.
  useEffect(() => {
    if (!capabilitiesLoaded) return;
    if (!hasChat && page === "chat") setPage("dashboard");
    if (!hasEditor && page === "code") setPage("dashboard");
    if (!hasTasks && page === "tasks") setPage("dashboard");
    if (!hasAgents && page === "agents") setPage("dashboard");
    if (!hasScheduler && page === "scheduler") setPage("dashboard");
    if (!hasMemory && page === "memory") setPage("dashboard");
  }, [capabilitiesLoaded, hasChat, hasEditor, hasTasks, hasAgents, hasScheduler, hasMemory, page]);

  const canAccessPlugins = user?.role === "admin";
  const { theme, setTheme, themes } = useTheme();
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const userMenuRef = useRef<HTMLDivElement>(null);

  // Close user menu when clicking outside.
  useEffect(() => {
    if (!userMenuOpen) return;
    const handleClick = (e: MouseEvent) => {
      if (userMenuRef.current && !userMenuRef.current.contains(e.target as Node)) {
        setUserMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [userMenuOpen]);

  const adminPages = useMemo(() => [
    { id: "users" as Page, label: "Users" },
    { id: "marketplace" as Page, label: "Marketplace" },
    { id: "plugins" as Page, label: "Plugins" },
    { id: "costs" as Page, label: "Costs" },
    ...(hasMemory ? [{ id: "memory" as Page, label: "Memory" }] : []),
    { id: "console" as Page, label: "Console" },
  ], [hasMemory]);

  if (!authenticated) {
    return (
      <>
        <LoginForm />
        {sessionExpired && (
          <div className="session-expired-overlay" onClick={dismissSessionExpired}>
            <div className="session-expired-modal" onClick={(e) => e.stopPropagation()}>
              <h3>Session Expired</h3>
              <p>Your session has expired. Please log in again to continue.</p>
              <button onClick={dismissSessionExpired}>OK</button>
            </div>
          </div>
        )}
      </>
    );
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
          <a
            href="/"
            className={`nav-tab ${page === "dashboard" ? "active" : ""}`}
            onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("dashboard"); } }}
          >
            DASHBOARD
          </a>
          {hasChat && (
            <a
              href="/chat"
              className={`nav-tab ${page === "chat" ? "active" : ""}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("chat"); } }}
            >
              CHAT
              {inFlightCount > 0 && page !== "chat" && (
                <span className="nav-badge nav-badge-inflight">{inFlightCount}</span>
              )}
              {totalUnread > 0 && inFlightCount === 0 && page !== "chat" && (
                <span className="nav-badge nav-badge-resolved">{totalUnread}</span>
              )}
            </a>
          )}
          {hasEditor && (
            <a
              href="/code"
              className={`nav-tab ${page === "code" ? "active" : ""}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("code"); } }}
            >
              CODE
            </a>
          )}
          <a
            href="/files"
            className={`nav-tab ${page === "files" ? "active" : ""}`}
            onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("files"); } }}
          >
            FILES
          </a>
          {hasTasks && (
            <a
              href="/tasks"
              className={`nav-tab ${page === "tasks" ? "active" : ""}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("tasks"); } }}
            >
              TASKS
            </a>
          )}
          {hasScheduler && (
            <a
              href="/scheduler"
              className={`nav-tab ${page === "scheduler" ? "active" : ""}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("scheduler"); } }}
            >
              SCHEDULER
            </a>
          )}
          {hasAgents && canAccessPlugins && (
            <a
              href="/agents"
              className={`nav-tab ${page === "agents" ? "active" : ""}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage("agents"); } }}
            >
              AGENTS
            </a>
          )}
        </nav>

        <div className="header-right">
          <select
            className="theme-select"
            value={theme}
            onChange={(e) => setTheme(e.target.value as typeof theme)}
          >
            {themes.map((t) => (
              <option key={t.id} value={t.id}>{t.label}</option>
            ))}
          </select>
          <div className="user-menu" ref={userMenuRef}>
            <button
              className={`user-menu-btn ${userMenuOpen ? "open" : ""}`}
              onClick={() => setUserMenuOpen((v) => !v)}
            >
              <span className="user-menu-email">{user?.email}</span>
              <span className={`user-role role-${user?.role}`}>
                {user?.role?.toUpperCase()}
              </span>
              <span className="user-menu-chevron">{userMenuOpen ? "▲" : "▼"}</span>
            </button>
            {userMenuOpen && (
              <div className="user-menu-dropdown">
                {canAccessPlugins && adminPages.map((p) => (
                  <a
                    key={p.id}
                    href={`/${p.id}`}
                    className={`user-menu-item ${page === p.id ? "active" : ""}`}
                    onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage(p.id); setUserMenuOpen(false); } }}
                  >
                    {p.label}
                  </a>
                ))}
                {canAccessPlugins && <div className="user-menu-divider" />}
                <button
                  className="user-menu-item user-menu-disconnect"
                  onClick={() => { setUserMenuOpen(false); logout(); }}
                >
                  Disconnect
                </button>
              </div>
            )}
          </div>
        </div>
      </header>

      {page === "dashboard" && <Dashboard />}
      {page === "files" && <FileBrowser initialPath={subpath} onPathChange={pushSubpath} onTitleChange={setTitleSegment} />}
      {page === "tasks" && <KanbanBoard initialSlug={subpath} onBoardChange={setSubpath} />}
      {page === "agents" && <Agents subpath={subpath} onNavigate={pushSubpath} />}
      {page === "marketplace" && <Marketplace />}
      {page === "plugins" && <PluginSettings initialPluginId={subpath} onPluginChange={setSubpath} />}
      {page === "costs" && <CostDashboard />}
      {page === "console" && <DebugConsole />}
      {page === "users" && <Users />}
      {page === "scheduler" && <TaskScheduler />}
      {page === "memory" && <MemoryExplorer />}

      {/* Chat and Code stay mounted (hidden) to preserve iframe/websocket state */}
      {hasChat && (
        <div style={{ display: page === "chat" ? "contents" : "none" }}>
          <Chat activePage={page} subpath={subpath} onConversationChange={setSubpath} />
        </div>
      )}
      {hasEditor && (
        <div style={{ display: page === "code" ? "contents" : "none" }}>
          <CodeEditor initialWorkspace={subpath} onWorkspaceChange={setSubpath} />
        </div>
      )}
    </div>
  );
}
