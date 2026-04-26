import { useState, useEffect, useRef, useCallback, useMemo } from "react";
import { useShallow } from "zustand/react/shallow";
import { ChevronDown, Moon, Sun, Plus } from "lucide-react";
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
import ThemeManager from "./components/ThemeManager";
import { useAuthStore } from "./stores/authStore";
import { apiClient } from "./api/client";
import { useEventStore } from "./stores/eventStore";
import { useChatStore } from "./stores/chatStore";
import { useTheme } from "./hooks/useTheme";
import { useRouter, type Page } from "./hooks/useRouter";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogFooter,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogAction,
} from "@/components/ui/alert-dialog";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

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
  const inFlightCount = useChatStore((s) => Object.values(s.inFlightTasks).reduce((sum, tasks) => sum + tasks.length, 0));
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

  const lastEventCount = useRef(0);
  useEffect(() => {
    if (events.length <= lastEventCount.current) {
      lastEventCount.current = events.length;
      return;
    }
    const newEvents = events.slice(lastEventCount.current);
    lastEventCount.current = events.length;
    if (newEvents.some((e) => PLUGIN_LIFECYCLE_EVENTS.has(e.type))) {
      checkCapabilities();
    }
  }, [events, checkCapabilities]);

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
  const { baseColor, setBaseColor, mode, toggleMode, customThemes } = useTheme();

  const adminPages = useMemo(() => [
    { id: "users" as Page, label: "Users" },
    { id: "marketplace" as Page, label: "Marketplace" },
    { id: "plugins" as Page, label: "Plugins" },
    { id: "costs" as Page, label: "Costs" },
    ...(hasMemory ? [{ id: "memory" as Page, label: "Memory" }] : []),
    { id: "console" as Page, label: "Console" },
  ], [hasMemory]);

  const navItems: { id: Page; label: string; show: boolean; badge?: { count: number; variant: "default" | "secondary" } }[] = [
    { id: "dashboard", label: "Dashboard", show: true },
    {
      id: "chat",
      label: "Chat",
      show: hasChat,
      badge: page !== "chat" && inFlightCount > 0
        ? { count: inFlightCount, variant: "default" }
        : page !== "chat" && totalUnread > 0
          ? { count: totalUnread, variant: "secondary" }
          : undefined,
    },
    { id: "code", label: "Code", show: hasEditor },
    { id: "files", label: "Files", show: true },
    { id: "tasks", label: "Tasks", show: hasTasks },
    { id: "scheduler", label: "Scheduler", show: hasScheduler },
    { id: "agents", label: "Agents", show: hasAgents && !!canAccessPlugins },
  ];

  if (!authenticated) {
    return (
      <>
        <LoginForm />
        <AlertDialog open={sessionExpired} onOpenChange={(open) => { if (!open) dismissSessionExpired(); }}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Session Expired</AlertDialogTitle>
              <AlertDialogDescription>
                Your session has expired. Please log in again to continue.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogAction onClick={dismissSessionExpired}>OK</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <header className="flex flex-wrap items-center gap-4 border-b border-border bg-card px-6 py-3">
        <div className="flex items-center gap-3">
          <h1 className="text-base font-semibold tracking-wider">
            {(import.meta.env.VITE_APP_NAME || "TeamAgentica").toUpperCase()}
          </h1>
          <Separator orientation="vertical" className="h-6" />
          <span className="text-xs uppercase tracking-widest text-muted-foreground">Control Panel</span>
        </div>

        <nav className="flex flex-1 items-center gap-1 overflow-x-auto">
          {navItems.filter((i) => i.show).map((item) => (
            <a
              key={item.id}
              href={`/${item.id === "dashboard" ? "" : item.id}`}
              onClick={(e) => { if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); setPage(item.id); } }}
              className={cn(
                "inline-flex items-center gap-2 rounded-md px-3 py-1.5 text-xs font-medium uppercase tracking-wider transition-colors",
                page === item.id
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-accent hover:text-accent-foreground"
              )}
            >
              {item.label}
              {item.badge && (
                <Badge variant={item.badge.variant} className="h-5 px-1.5 text-[10px]">
                  {item.badge.count}
                </Badge>
              )}
            </a>
          ))}
        </nav>

        <div className="flex items-center gap-3">
          <Button variant="ghost" size="icon" onClick={toggleMode} title={`Switch to ${mode === "dark" ? "light" : "dark"} mode`}>
            {mode === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>
          <Select
            value={baseColor ?? "__default__"}
            onValueChange={(v) => {
              if (v === "__add__") { setPage("themes"); return; }
              setBaseColor(v === "__default__" ? null : v);
            }}
          >
            <SelectTrigger className="h-9 w-36">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">Default</SelectItem>
              {customThemes.map((t) => (
                <SelectItem key={t.id} value={t.id}>{t.label}</SelectItem>
              ))}
              {canAccessPlugins && (
                <SelectItem value="__add__">
                  <span className="flex items-center gap-2 text-muted-foreground">
                    <Plus className="h-3 w-3" /> Add new theme…
                  </span>
                </SelectItem>
              )}
            </SelectContent>
          </Select>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="sm" className="gap-2">
                <span className="max-w-[180px] truncate">{user?.email}</span>
                <Badge variant={user?.role === "admin" ? "default" : "secondary"} className="text-[10px]">
                  {user?.role?.toUpperCase()}
                </Badge>
                <ChevronDown className="h-3 w-3 opacity-60" />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-[200px]">
              {canAccessPlugins && adminPages.map((p) => (
                <DropdownMenuItem
                  key={p.id}
                  onSelect={() => setPage(p.id)}
                  className={cn(page === p.id && "bg-accent")}
                >
                  {p.label}
                </DropdownMenuItem>
              ))}
              {canAccessPlugins && <DropdownMenuSeparator />}
              <DropdownMenuItem
                onSelect={() => logout()}
                className="text-destructive focus:text-destructive"
              >
                Disconnect
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>

      <main className="flex-1">
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
        {page === "themes" && <ThemeManager />}

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
      </main>
    </div>
  );
}
