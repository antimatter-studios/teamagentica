import { useEffect, useState } from "react";
import { useShallow } from "zustand/react/shallow";
import { useAuthStore } from "../stores/authStore";
import { usePluginStore } from "../stores/pluginStore";
import { useEventStore, type DebugEvent, type EventLogEntry } from "../stores/eventStore";
import { apiClient } from "../api/client";
import { parseCapabilities } from "@teamagentica/api-client";
import type { Plugin, AliasInfo } from "@teamagentica/api-client";

export default function Dashboard() {
  const { user, users } = useAuthStore(
    useShallow((s) => ({ user: s.user, users: s.users }))
  );
  const logout = useAuthStore((s) => s.logout);
  const fetchUsers = useAuthStore((s) => s.fetchUsers);

  const plugins = usePluginStore((s) => s.plugins);
  const fetchPlugins = usePluginStore((s) => s.fetch);

  const auditEvents = useEventStore((s) => s.auditEvents);
  const eventLogEvents = useEventStore((s) => s.eventLogEvents);
  const eventsConnected = useEventStore((s) => s.connected);

  const [aliases, setAliases] = useState<AliasInfo[]>([]);

  useEffect(() => {
    fetchUsers();
    fetchPlugins();
    apiClient.aliases.list()
      .then((aliases) => setAliases(aliases))
      .catch(() => {});
  }, []);

  if (!user) {
    return (
      <div className="dashboard-content">
        <div className="loading-screen">
          <p className="form-error">Session expired or user not found</p>
          <button className="login-submit" onClick={logout}>
            RETURN TO LOGIN
          </button>
        </div>
      </div>
    );
  }

  const isAdmin = user.role === "admin";
  const running = plugins.filter((p) => p.status === "running");
  const errored = plugins.filter((p) => p.status === "error" || p.status === "unhealthy");
  const recentEvents = auditEvents.slice(-20).reverse();

  return (
    <div className="dashboard-content">
      <main className="dashboard-main">
        {/* ── Summary stats ── */}
        <section className="dashboard-section">
          <div className="dash-stats">
            <StatCard
              label="PLUGINS ONLINE"
              value={String(running.length)}
              sub={`${plugins.length} installed`}
              accent="var(--success)"
            />
            <StatCard
              label="ALIASES"
              value={String(aliases.length)}
              sub={`across ${new Set(aliases.map((a) => a.plugin_id || "admin")).size} source${aliases.length === 1 ? "" : "s"}`}
              accent="var(--accent)"
            />
            {errored.length > 0 ? (
              <StatCard
                label="ERRORS"
                value={String(errored.length)}
                sub={errored.map((p) => p.name).join(", ")}
                accent="var(--error)"
              />
            ) : (
              <StatCard
                label="STATUS"
                value="OK"
                sub="no errors"
                accent="var(--success)"
              />
            )}
            <StatCard
              label="EVENTS"
              value={eventsConnected ? "LIVE" : "OFF"}
              sub={`${auditEvents.length} in buffer`}
              accent={eventsConnected ? "var(--success)" : "var(--text-muted)"}
            />
          </div>
        </section>

        {/* ── Plugin fleet ── */}
        {isAdmin && plugins.length > 0 && (
          <section className="dashboard-section">
            <h2 className="section-title">
              <span className="section-icon">[=]</span>
              PLUGIN FLEET
            </h2>
            <div className="dash-fleet">
              {plugins.map((p) => (
                <PluginCard key={p.id} plugin={p} />
              ))}
            </div>
          </section>
        )}

        {/* ── Active aliases ── */}
        {aliases.length > 0 && (
          <section className="dashboard-section">
            <h2 className="section-title">
              <span className="section-icon">[@]</span>
              ACTIVE ALIASES
              <span className="section-count">{aliases.length}</span>
            </h2>
            <div className="dash-aliases">
              {aliases.map((a) => (
                <div className="dash-alias" key={a.name}>
                  <span className="dash-alias-name">@{a.name}</span>
                  <span className="dash-alias-arrow">&rarr;</span>
                  <span className="dash-alias-target">{a.target}</span>
                  {a.capabilities && a.capabilities.length > 0 && (
                    <span className="dash-alias-caps">
                      {a.capabilities.map((c) => (
                        <span className="capability-tag" key={c}>{c}</span>
                      ))}
                    </span>
                  )}
                </div>
              ))}
            </div>
          </section>
        )}

        {/* ── Two-column: Activity + Event Log ── */}
        <div className="dash-columns">
          {/* Recent activity (live stream) */}
          <section className="dashboard-section dash-col">
            <h2 className="section-title">
              <span className="section-icon">[~]</span>
              RECENT ACTIVITY
              {eventsConnected && <span className="dash-live-dot" />}
            </h2>
            {recentEvents.length === 0 ? (
              <div className="dash-empty">No events yet.</div>
            ) : (
              <div className="dash-activity">
                {recentEvents.map((evt, i) => (
                  <EventRow key={i} event={evt} />
                ))}
              </div>
            )}
          </section>

          {/* Persistent event log (inter-plugin communication) */}
          {isAdmin && (
            <section className="dashboard-section dash-col">
              <h2 className="section-title">
                <span className="section-icon">[&gt;]</span>
                EVENT LOG
                <span className="section-count">{eventLogEvents.length}</span>
                {eventsConnected && <span className="dash-live-dot" />}
              </h2>
              {eventLogEvents.length === 0 ? (
                <div className="dash-empty">No inter-plugin events recorded yet.</div>
              ) : (
                <div className="dash-activity">
                  {eventLogEvents.slice(0, 50).map((entry) => (
                    <EventLogRow key={entry.id} entry={entry} />
                  ))}
                </div>
              )}
            </section>
          )}
        </div>

        {/* ── Operators ── */}
        {isAdmin && users.length > 0 && (
          <section className="dashboard-section">
            <h2 className="section-title">
              <span className="section-icon">[#]</span>
              OPERATORS
              <span className="section-count">{users.length}</span>
            </h2>
            <div className="dash-operators">
              {users.map((u) => (
                <div className="dash-operator" key={u.id}>
                  <span className="dash-op-name">{u.display_name || u.email}</span>
                  <span className={`user-role role-${u.role}`}>{u.role.toUpperCase()}</span>
                </div>
              ))}
            </div>
          </section>
        )}
      </main>
    </div>
  );
}

function StatCard({
  label,
  value,
  sub,
  accent,
}: {
  label: string;
  value: string;
  sub: string;
  accent: string;
}) {
  return (
    <div className="dash-stat" style={{ borderColor: accent }}>
      <span className="dash-stat-label">{label}</span>
      <span className="dash-stat-value" style={{ color: accent }}>{value}</span>
      <span className="dash-stat-sub">{sub}</span>
    </div>
  );
}

function PluginCard({ plugin }: { plugin: Plugin }) {
  const caps = parseCapabilities(plugin);
  const statusClass = (() => {
    switch (plugin.status) {
      case "running": return "status-running";
      case "starting": return "status-starting";
      case "error":
      case "unhealthy": return "status-error";
      default: return "status-stopped";
    }
  })();

  return (
    <div className={`dash-plugin ${statusClass}`}>
      <div className="dash-plugin-header">
        <span className={`plugin-status-dot ${statusClass}`} />
        <span className="dash-plugin-name">{plugin.name}</span>
        <span className="dash-plugin-ver">v{plugin.version}</span>
      </div>
      {caps.length > 0 && (
        <div className="dash-plugin-caps">
          {caps.slice(0, 3).map((c) => (
            <span className="capability-tag" key={c}>{c}</span>
          ))}
        </div>
      )}
    </div>
  );
}

function EventRow({ event }: { event: DebugEvent }) {
  const time = event.timestamp
    ? new Date(event.timestamp).toLocaleTimeString()
    : "";

  const typeClass = event.type?.includes("error") ? "evt-error"
    : event.type === "dispatch" ? "evt-dispatch"
    : event.type?.startsWith("alias") ? "evt-alias"
    : "";

  return (
    <div className={`dash-evt ${typeClass}`}>
      <span className="dash-evt-time">{time}</span>
      <span className="dash-evt-type">{event.type}</span>
      {event.plugin_id && (
        <span className="dash-evt-plugin">{event.plugin_id}</span>
      )}
      {event.detail && (
        <span className="dash-evt-detail">{event.detail.slice(0, 80)}</span>
      )}
    </div>
  );
}

const STATUS_CLASSES: Record<string, string> = {
  delivered: "evtlog-delivered",
  dispatched: "evtlog-delivered",
  queued: "evtlog-queued",
  failed: "evtlog-failed",
  evicted: "evtlog-evicted",
};

function EventLogRow({ entry }: { entry: EventLogEntry }) {
  const time = entry.created_at
    ? new Date(entry.created_at).toLocaleTimeString()
    : "";

  const statusClass = STATUS_CLASSES[entry.status] || "";

  return (
    <div className={`dash-evt ${statusClass}`}>
      <span className="dash-evt-time">{time}</span>
      <span className="evtlog-type">{entry.event_type}</span>
      <span className="evtlog-flow">
        <span className="evtlog-source">{entry.source_plugin_id}</span>
        <span className="evtlog-arrow">&rarr;</span>
        <span className="evtlog-target">{entry.target_plugin_id}</span>
      </span>
      <span className={`evtlog-status ${statusClass}`}>{entry.status}</span>
      {entry.detail && (
        <span className="dash-evt-detail">{entry.detail.slice(0, 60)}</span>
      )}
    </div>
  );
}
