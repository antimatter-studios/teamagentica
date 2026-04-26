import { useState, useEffect, useRef } from "react";
import { useShallow } from "zustand/react/shallow";
import { Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { useEventStore } from "../stores/eventStore";

const TYPE_COLORS: Record<string, string> = {
  proxy: "#00d4ff",
  register: "#00ff88",
  deregister: "#ff4757",
  heartbeat: "#6b6b80",
  test: "#ffbe00",
  install: "#a78bfa",
  uninstall: "#f97316",
  enable: "#34d399",
  disable: "#fbbf24",
  error: "#ef4444",
  warning: "#fbbf24",
  poll: "#64748b",
  poll_start: "#38bdf8",
  poll_stop: "#94a3b8",
  poll_result: "#38bdf8",
  message_received: "#22d3ee",
  message_sent: "#4ade80",
  agent_response: "#c084fc",
  chat_request: "#818cf8",
  chat_response: "#a78bfa",
  fallback: "#fb923c",
  start: "#22c55e",
  stop: "#f97316",
  restart: "#3b82f6",
  orchestrator: "#a855f7",
  webhook: "#e879f9",
  subscribe: "#2dd4bf",
  unsubscribe: "#f87171",
  dispatch: "#818cf8",
  dispatch_ok: "#4ade80",
  dispatch_error: "#ef4444",
  "tunnel:ready": "#06b6d4",
  "webhook:ready": "#14b8a6",
  "webhook:url": "#10b981",
  "webhook:register": "#8b5cf6",
  "webhook:unregister": "#f472b6",
  "webhook:error": "#ef4444",
};

function formatTime(ts: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "—";
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();
  const time = d.toLocaleTimeString("en-US", {
    hour12: false,
    fractionalSecondDigits: 3,
  } as Intl.DateTimeFormatOptions);
  if (sameDay) return time;
  const date = d.toLocaleDateString("en-US", { month: "short", day: "numeric" });
  return `${date} ${time}`;
}

function statusColor(status?: number): string {
  if (!status) return "";
  if (status < 300) return "#00ff88";
  if (status < 400) return "#ffbe00";
  return "#ff4757";
}

const TYPE_OPTIONS: [string, string][] = [
  ["all", "All types"],
  ["proxy", "Proxy"],
  ["register", "Register"],
  ["deregister", "Deregister"],
  ["heartbeat", "Heartbeat"],
  ["install", "Install"],
  ["uninstall", "Uninstall"],
  ["enable", "Enable"],
  ["disable", "Disable"],
  ["start", "Start"],
  ["stop", "Stop"],
  ["restart", "Restart"],
  ["orchestrator", "Orchestrator"],
  ["error", "Error"],
  ["warning", "Warning"],
  ["message_received", "Msg received"],
  ["message_sent", "Msg sent"],
  ["agent_response", "Agent response"],
  ["poll_start", "Poll start"],
  ["webhook", "Webhook"],
  ["subscribe", "Subscribe"],
  ["dispatch", "Dispatch"],
  ["dispatch_ok", "Dispatch ok"],
  ["dispatch_error", "Dispatch err"],
  ["tunnel:ready", "Tunnel ready"],
  ["webhook:ready", "Webhook ready"],
  ["webhook:url", "Webhook url"],
  ["webhook:register", "Webhook reg"],
  ["webhook:unregister", "Webhook unreg"],
  ["webhook:error", "Webhook err"],
  ["test", "Test"],
];

export default function DebugConsole() {
  const { events, connected } = useEventStore(
    useShallow((s) => ({ events: s.auditEvents, connected: s.connected }))
  );
  const connect = useEventStore((s) => s.connect);
  const disconnect = useEventStore((s) => s.disconnect);
  const clear = useEventStore((s) => s.clear);
  const [hideHeartbeat, setHideHeartbeat] = useState(true);
  const [filterType, setFilterType] = useState<string>("all");
  const [autoscroll, setAutoscroll] = useState(true);
  const [wrapText, setWrapText] = useState(false);
  const logRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  useEffect(() => {
    if (autoscroll && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight;
    }
  }, [events, autoscroll]);

  const filtered = events.filter((e) => {
    if (hideHeartbeat && e.type === "heartbeat") return false;
    if (filterType !== "all" && e.type !== filterType) return false;
    return true;
  });

  return (
    <div className="flex h-full w-full flex-col gap-3 p-4">
      <Card className="flex flex-wrap items-center justify-between gap-3 p-3">
        <div className="flex items-center gap-3">
          <Terminal className="h-4 w-4" />
          <span className="text-sm font-semibold uppercase tracking-wide">Debug console</span>
          <Badge variant={connected ? "default" : "destructive"}>
            {connected ? "Live" : "Disconnected"}
          </Badge>
          <span className="text-xs text-muted-foreground">{filtered.length} events</span>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <Label className="flex items-center gap-2 text-xs">
            <Checkbox
              checked={hideHeartbeat}
              onCheckedChange={(v) => setHideHeartbeat(!!v)}
            />
            Hide heartbeat
          </Label>
          <Select value={filterType} onValueChange={setFilterType}>
            <SelectTrigger className="h-8 w-44">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {TYPE_OPTIONS.map(([v, l]) => (
                <SelectItem key={v} value={v}>{l}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Label className="flex items-center gap-2 text-xs">
            <Checkbox checked={wrapText} onCheckedChange={(v) => setWrapText(!!v)} />
            Wrap
          </Label>
          <Label className="flex items-center gap-2 text-xs">
            <Checkbox checked={autoscroll} onCheckedChange={(v) => setAutoscroll(!!v)} />
            Autoscroll
          </Label>
          <Button size="sm" variant="outline" onClick={clear}>Clear</Button>
        </div>
      </Card>

      <Card className="flex-1 overflow-hidden p-0">
        <div ref={logRef} className="h-full overflow-auto">
          <div className="p-3 font-mono text-xs">
            {filtered.length === 0 && (
              <div className="p-6 text-center text-muted-foreground">
                Waiting for events... Plugin communication will appear here in real-time.
              </div>
            )}
            {filtered.map((evt, i) => (
              <div
                key={i}
                className={cn(
                  "flex items-center gap-2 py-0.5",
                  wrapText ? "flex-wrap" : "whitespace-nowrap"
                )}
              >
                <span className="shrink-0 text-muted-foreground">{formatTime(evt.timestamp)}</span>
                <span
                  className="shrink-0 font-semibold"
                  style={{ color: TYPE_COLORS[evt.type] || "#e0e0e8" }}
                >
                  {evt.type.toUpperCase().padEnd(10)}
                </span>
                <span className="shrink-0 text-muted-foreground">{evt.plugin_id}</span>
                {evt.method && <span className="shrink-0">{evt.method}</span>}
                {evt.path && <span className="shrink-0 text-muted-foreground">{evt.path}</span>}
                {evt.status !== undefined && evt.status > 0 && (
                  <span className="shrink-0 font-semibold" style={{ color: statusColor(evt.status) }}>
                    {evt.status}
                  </span>
                )}
                {evt.duration_ms !== undefined && evt.duration_ms > 0 && (
                  <span className="shrink-0 text-muted-foreground">{evt.duration_ms}ms</span>
                )}
                {evt.detail && <span className={cn("text-muted-foreground", !wrapText && "truncate")}>{evt.detail}</span>}
              </div>
            ))}
          </div>
        </div>
      </Card>
    </div>
  );
}
