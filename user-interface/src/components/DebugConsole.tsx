import { useState, useEffect, useRef } from "react";
import { useShallow } from "zustand/react/shallow";
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

  const handleClear = () => clear();

  return (
    <div className="console-page">
      <div className="console-toolbar">
        <div className="console-toolbar-left">
          <span
            className="section-title"
            style={{ margin: 0, padding: 0, border: "none" }}
          >
            <span className="section-icon">&gt;_</span> DEBUG CONSOLE
          </span>
          <span
            className={`console-status ${connected ? "connected" : "disconnected"}`}
          >
            {connected ? "LIVE" : "DISCONNECTED"}
          </span>
          <span className="console-count">{filtered.length} events</span>
        </div>
        <div className="console-toolbar-right">
          <label className="console-filter-label">
            <input
              type="checkbox"
              checked={hideHeartbeat}
              onChange={(e) => setHideHeartbeat(e.target.checked)}
              className="console-checkbox-input"
            />
            <span>HIDE HEARTBEAT</span>
          </label>
          <select
            className="console-filter-select"
            value={filterType}
            onChange={(e) => setFilterType(e.target.value)}
          >
            <option value="all">ALL TYPES</option>
            <option value="proxy">PROXY</option>
            <option value="register">REGISTER</option>
            <option value="deregister">DEREGISTER</option>
            <option value="heartbeat">HEARTBEAT</option>
            <option value="install">INSTALL</option>
            <option value="uninstall">UNINSTALL</option>
            <option value="enable">ENABLE</option>
            <option value="disable">DISABLE</option>
            <option value="start">START</option>
            <option value="stop">STOP</option>
            <option value="restart">RESTART</option>
            <option value="orchestrator">ORCHESTRATOR</option>
            <option value="error">ERROR</option>
            <option value="warning">WARNING</option>
            <option value="message_received">MSG RECEIVED</option>
            <option value="message_sent">MSG SENT</option>
            <option value="agent_response">AGENT RESPONSE</option>
            <option value="poll_start">POLL START</option>
            <option value="webhook">WEBHOOK</option>
            <option value="subscribe">SUBSCRIBE</option>
            <option value="dispatch">DISPATCH</option>
            <option value="dispatch_ok">DISPATCH OK</option>
            <option value="dispatch_error">DISPATCH ERR</option>
            <option value="tunnel:ready">TUNNEL READY</option>
            <option value="webhook:ready">WEBHOOK READY</option>
            <option value="webhook:url">WEBHOOK URL</option>
            <option value="webhook:register">WEBHOOK REG</option>
            <option value="webhook:unregister">WEBHOOK UNREG</option>
            <option value="webhook:error">WEBHOOK ERR</option>
            <option value="test">TEST</option>
          </select>
          <label className="console-filter-label">
            <input
              type="checkbox"
              checked={wrapText}
              onChange={(e) => setWrapText(e.target.checked)}
              className="console-checkbox-input"
            />
            <span>WRAP</span>
          </label>
          <label className="console-filter-label">
            <input
              type="checkbox"
              checked={autoscroll}
              onChange={(e) => setAutoscroll(e.target.checked)}
              className="console-checkbox-input"
            />
            <span>AUTOSCROLL</span>
          </label>
          <button className="plugin-action-btn" onClick={handleClear}>
            CLEAR
          </button>
        </div>
      </div>

      <div className="console-log" ref={logRef}>
        {filtered.length === 0 && (
          <div className="console-empty">
            Waiting for events... Plugin communication will appear here in
            real-time.
          </div>
        )}
        {filtered.map((evt, i) => (
          <div key={i} className={`console-line${wrapText ? " console-line-wrap" : ""}`}>
            <span className="console-time">{formatTime(evt.timestamp)}</span>
            <span
              className="console-type"
              style={{ color: TYPE_COLORS[evt.type] || "#e0e0e8" }}
            >
              {evt.type.toUpperCase().padEnd(10)}
            </span>
            <span className="console-plugin">{evt.plugin_id}</span>
            {evt.method && (
              <span className="console-method">{evt.method}</span>
            )}
            {evt.path && <span className="console-path">{evt.path}</span>}
            {evt.status !== undefined && evt.status > 0 && (
              <span
                className="console-status-code"
                style={{ color: statusColor(evt.status) }}
              >
                {evt.status}
              </span>
            )}
            {evt.duration_ms !== undefined && evt.duration_ms > 0 && (
              <span className="console-duration">{evt.duration_ms}ms</span>
            )}
            {evt.detail && (
              <span className="console-detail">{evt.detail}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
