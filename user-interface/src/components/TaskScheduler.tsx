import { useState, useEffect, useCallback } from "react";
import { apiClient } from "../api/client";
import { useEventStore } from "../stores/eventStore";
import type { ScheduledEvent, EventLogEntry } from "@teamagentica/api-client";

function relativeTime(unixMs: number): string {
  if (!unixMs) return "—";
  const diff = unixMs - Date.now();
  if (diff < 0) return "overdue";
  if (diff < 60_000) return `in ${Math.ceil(diff / 1000)}s`;
  if (diff < 3_600_000) return `in ${Math.ceil(diff / 60_000)}m`;
  if (diff < 86_400_000) return `in ${Math.round(diff / 3_600_000)}h`;
  return `in ${Math.round(diff / 86_400_000)}d`;
}

function formatTime(unixMs: number): string {
  if (!unixMs) return "—";
  return new Date(unixMs).toLocaleString();
}

type View = "jobs" | "log" | "new-job" | "edit-job";

const emptyForm = { name: "", text: "", type: "repeat" as "once" | "repeat", triggerType: "timer" as "timer" | "event", schedule: "", eventPattern: "" };

export default function TaskScheduler() {
  const [view, setView] = useState<View>("jobs");
  const [jobs, setJobs] = useState<ScheduledEvent[]>([]);
  const [logs, setLogs] = useState<EventLogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedJob, setSelectedJob] = useState<ScheduledEvent | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [saving, setSaving] = useState(false);

  const sseEvents = useEventStore((s) => s.eventLogEvents);

  const refresh = useCallback(() => {
    if (!apiClient.scheduler) return;
    Promise.all([
      apiClient.scheduler.listEvents(),
      apiClient.scheduler.getLogs(50),
    ])
      .then(([evRes, logRes]) => {
        setJobs(evRes.events || []);
        setLogs(logRes.entries || []);
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  useEffect(() => {
    const id = setInterval(refresh, 10_000);
    return () => clearInterval(id);
  }, [refresh]);

  useEffect(() => {
    const schedulerEvents = sseEvents.filter((e) => e.event_type === "scheduler:fired");
    if (schedulerEvents.length > 0) refresh();
  }, [sseEvents, refresh]);

  const canSubmitCreate = form.name && (form.triggerType === "timer" ? form.schedule : form.eventPattern);

  const handleCreate = async () => {
    if (!canSubmitCreate) return;
    setSaving(true);
    try {
      setError("");
      await apiClient.scheduler.createEvent({
        name: form.name,
        text: form.text,
        type: form.type,
        trigger_type: form.triggerType,
        schedule: form.triggerType === "timer" ? form.schedule : undefined,
        event_pattern: form.triggerType === "event" ? form.eventPattern : undefined,
      });
      setForm(emptyForm);
      setView("jobs");
      refresh();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleUpdate = async () => {
    if (!selectedJob) return;
    setSaving(true);
    try {
      setError("");
      await apiClient.scheduler.updateEvent(selectedJob.id, {
        name: form.name || undefined,
        text: form.text,
        schedule: form.schedule || undefined,
        event_pattern: form.eventPattern || undefined,
      });
      setView("jobs");
      setSelectedJob(null);
      setForm(emptyForm);
      refresh();
    } catch (e: any) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (job: ScheduledEvent) => {
    try {
      await apiClient.scheduler.updateEvent(job.id, { enabled: !job.enabled });
      refresh();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this job?")) return;
    try {
      setError("");
      await apiClient.scheduler.deleteEvent(id);
      if (selectedJob?.id === id) {
        setSelectedJob(null);
        setView("jobs");
      }
      refresh();
    } catch (e: any) {
      setError(e.message);
    }
  };

  const selectJob = (job: ScheduledEvent) => {
    setSelectedJob(job);
    setView("jobs");
  };

  const startEdit = (job: ScheduledEvent) => {
    setSelectedJob(job);
    setForm({ name: job.name, text: job.text, type: job.type, triggerType: job.trigger_type || "timer", schedule: job.schedule, eventPattern: job.event_pattern });
    setView("edit-job");
  };

  if (loading) {
    return (
      <div className="plugin-settings">
        <div className="plugin-loading">Loading scheduler…</div>
      </div>
    );
  }

  return (
    <div className="um-layout">
      {/* ===== SIDEBAR ===== */}
      <div className="um-sidebar">
        <div className="um-sidebar-scroll">
          {/* Jobs group */}
          <div className="um-sidebar-group">
            <div className="um-sidebar-group-header">
              <span className="um-sidebar-group-name">Jobs</span>
              <span className="um-sidebar-count">{jobs.length}</span>
            </div>
            <button
              className="um-sidebar-add"
              onClick={() => { setView("new-job"); setError(""); setForm(emptyForm); }}
            >
              + Add Job
            </button>
            {jobs.map((job) => (
              <button
                key={job.id}
                className={`um-sidebar-item ${selectedJob?.id === job.id && view === "jobs" ? "active" : ""}`}
                onClick={() => selectJob(job)}
              >
                <span className={`um-sidebar-item-dot ${job.enabled ? "active" : "banned"}`} />
                <span className="um-sidebar-item-info">
                  <span className="um-sidebar-item-name">{job.name}</span>
                  <span className="um-sidebar-item-meta">
                    {(job.trigger_type || "timer") === "timer"
                      ? `${job.schedule} · ${job.type} · ${relativeTime(job.next_fire)}`
                      : `on ${job.event_pattern} · ${job.type}`
                    }
                  </span>
                </span>
              </button>
            ))}
          </div>
        </div>

        {/* Log button at bottom */}
        <div className="um-sidebar-footer">
          <button
            className={`um-sidebar-footer-btn ${view === "log" ? "active" : ""}`}
            onClick={() => setView("log")}
          >
            <span className="um-sidebar-footer-icon">&#x23F1;</span>
            Execution Log
            <span className="um-sidebar-count">{logs.length}</span>
          </button>
        </div>
      </div>

      {/* ===== CONTENT ===== */}
      <div className="um-content">
        {error && (
          <div className="um-panel" style={{ paddingBottom: 0 }}>
            <div className="um-form-hint um-form-hint-error">{error}</div>
          </div>
        )}

        {/* --- Job detail view --- */}
        {view === "jobs" && selectedJob && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">{selectedJob.name}</h2>
              <div className="um-panel-actions">
                <button className="um-btn um-btn-secondary" onClick={() => startEdit(selectedJob)}>Edit</button>
                <button className="um-btn um-btn-secondary" onClick={() => handleToggle(selectedJob)}>
                  {selectedJob.enabled ? "Disable" : "Enable"}
                </button>
                <button className="um-btn um-btn-secondary" onClick={() => handleDelete(selectedJob.id)}>Delete</button>
              </div>
            </div>

            <div className="um-detail-grid">
              <div className="um-detail-field">
                <span className="um-detail-label">Trigger</span>
                <span className="um-detail-value">{selectedJob.trigger_type || "timer"}</span>
              </div>
              {(selectedJob.trigger_type || "timer") === "timer" ? (
                <div className="um-detail-field">
                  <span className="um-detail-label">Schedule</span>
                  <span className="um-detail-value">{selectedJob.schedule} ({selectedJob.schedule_type})</span>
                </div>
              ) : (
                <div className="um-detail-field">
                  <span className="um-detail-label">Event Pattern</span>
                  <span className="um-detail-value">{selectedJob.event_pattern}</span>
                </div>
              )}
              <div className="um-detail-field">
                <span className="um-detail-label">Type</span>
                <span className="um-detail-value">{selectedJob.type}</span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Status</span>
                <span className="um-detail-value">{selectedJob.enabled ? "Enabled" : "Disabled"}</span>
              </div>
              <div className="um-detail-field">
                <span className="um-detail-label">Fired</span>
                <span className="um-detail-value">{selectedJob.fired_count}x</span>
              </div>
              {(selectedJob.trigger_type || "timer") === "timer" && (
                <div className="um-detail-field">
                  <span className="um-detail-label">Next Fire</span>
                  <span className="um-detail-value">
                    {selectedJob.enabled ? `${formatTime(selectedJob.next_fire)} (${relativeTime(selectedJob.next_fire)})` : "—"}
                  </span>
                </div>
              )}
            </div>

            {selectedJob.text && (
              <div className="um-detail-field" style={{ marginTop: 20 }}>
                <span className="um-detail-label">Description</span>
                <span className="um-detail-value">{selectedJob.text}</span>
              </div>
            )}
          </div>
        )}

        {/* --- Empty state --- */}
        {view === "jobs" && !selectedJob && (
          <div className="um-panel">
            <div className="um-form-static">Select a job from the sidebar, or create a new one.</div>
          </div>
        )}

        {/* --- Create job form --- */}
        {view === "new-job" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">New Job</h2>
            </div>
            <div className="um-form">
              <div className="um-form-field">
                <label className="um-form-label">Name</label>
                <input
                  className="um-form-input"
                  placeholder="My scheduled job"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Trigger</label>
                <select
                  className="um-form-input"
                  value={form.triggerType}
                  onChange={(e) => setForm({ ...form, triggerType: e.target.value as "timer" | "event" })}
                >
                  <option value="timer">Timer (cron / interval)</option>
                  <option value="event">Event (SDK event pattern)</option>
                </select>
              </div>
              {form.triggerType === "timer" ? (
                <div className="um-form-field">
                  <label className="um-form-label">Schedule</label>
                  <input
                    className="um-form-input"
                    placeholder="5m, 1h30m, or */5 * * * *"
                    value={form.schedule}
                    onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                  />
                  <span className="um-form-hint">Go duration (10s, 5m, 1h) or cron expression (*/5 * * * *)</span>
                </div>
              ) : (
                <div className="um-form-field">
                  <label className="um-form-label">Event Pattern</label>
                  <input
                    className="um-form-input"
                    placeholder="task-tracking:assign"
                    value={form.eventPattern}
                    onChange={(e) => setForm({ ...form, eventPattern: e.target.value })}
                  />
                  <span className="um-form-hint">SDK event type to listen for (e.g. task-tracking:assign)</span>
                </div>
              )}
              <div className="um-form-field">
                <label className="um-form-label">Type</label>
                <select
                  className="um-form-input"
                  value={form.type}
                  onChange={(e) => setForm({ ...form, type: e.target.value as "once" | "repeat" })}
                >
                  <option value="repeat">Repeat</option>
                  <option value="once">Once</option>
                </select>
              </div>
              <div className="um-form-field">
                <label className="um-form-label">Description</label>
                <textarea
                  className="um-form-input"
                  placeholder="Optional description or message text"
                  value={form.text}
                  rows={3}
                  onChange={(e) => setForm({ ...form, text: e.target.value })}
                />
              </div>
              <div className="um-form-actions">
                <button className="um-btn um-btn-secondary" onClick={() => setView("jobs")}>Cancel</button>
                <button
                  className="um-btn um-btn-primary"
                  disabled={saving || !canSubmitCreate}
                  onClick={handleCreate}
                >
                  {saving ? "Creating…" : "Create Job"}
                </button>
              </div>
            </div>
          </div>
        )}

        {/* --- Edit job form --- */}
        {view === "edit-job" && selectedJob && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Edit Job</h2>
            </div>
            <div className="um-form">
              <div className="um-form-field">
                <label className="um-form-label">Name</label>
                <input
                  className="um-form-input"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </div>
              {(selectedJob.trigger_type || "timer") === "timer" ? (
                <div className="um-form-field">
                  <label className="um-form-label">Schedule</label>
                  <input
                    className="um-form-input"
                    value={form.schedule}
                    onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                  />
                  <span className="um-form-hint">Go duration (10s, 5m, 1h) or cron expression (*/5 * * * *)</span>
                </div>
              ) : (
                <div className="um-form-field">
                  <label className="um-form-label">Event Pattern</label>
                  <input
                    className="um-form-input"
                    value={form.eventPattern}
                    onChange={(e) => setForm({ ...form, eventPattern: e.target.value })}
                  />
                  <span className="um-form-hint">SDK event type to listen for</span>
                </div>
              )}
              <div className="um-form-field">
                <label className="um-form-label">Description</label>
                <textarea
                  className="um-form-input"
                  value={form.text}
                  rows={3}
                  onChange={(e) => setForm({ ...form, text: e.target.value })}
                />
              </div>
              <div className="um-form-actions">
                <button className="um-btn um-btn-secondary" onClick={() => { setView("jobs"); }}>Cancel</button>
                <button
                  className="um-btn um-btn-primary"
                  disabled={saving || !form.name}
                  onClick={handleUpdate}
                >
                  {saving ? "Saving…" : "Save Changes"}
                </button>
              </div>
            </div>
          </div>
        )}

        {/* --- Execution log --- */}
        {view === "log" && (
          <div className="um-panel">
            <div className="um-panel-header">
              <h2 className="um-panel-title">Execution Log</h2>
              <span className="um-sidebar-count">{logs.length}</span>
            </div>
            {logs.length === 0 ? (
              <div className="um-form-static">No events have fired yet.</div>
            ) : (
              <table style={{ width: "100%", borderCollapse: "collapse" }}>
                <thead>
                  <tr>
                    <th className="um-detail-label" style={{ textAlign: "left", padding: "8px 12px" }}>Time</th>
                    <th className="um-detail-label" style={{ textAlign: "left", padding: "8px 12px" }}>Job</th>
                    <th className="um-detail-label" style={{ textAlign: "left", padding: "8px 12px" }}>Text</th>
                    <th className="um-detail-label" style={{ textAlign: "left", padding: "8px 12px" }}>Result</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.map((entry) => (
                    <tr key={entry.id} className="um-table-row">
                      <td style={{ padding: "8px 12px", whiteSpace: "nowrap", fontFamily: "var(--font-mono)", fontSize: 12, color: "var(--text-muted)" }}>
                        {formatTime(entry.fired_at)}
                      </td>
                      <td style={{ padding: "8px 12px", fontSize: 12, color: "var(--text-primary)", fontWeight: 500 }}>
                        {entry.job_name}
                      </td>
                      <td style={{ padding: "8px 12px", fontSize: 12, color: "var(--text-secondary)", maxWidth: 300, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {entry.text || "—"}
                      </td>
                      <td style={{ padding: "8px 12px" }}>
                        <span className="um-cap-badge">{entry.result}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
