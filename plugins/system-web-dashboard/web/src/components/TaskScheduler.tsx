import { useState, useEffect } from "react";
import { useSchedulerStore } from "../stores/schedulerStore";
import { useEventStore } from "../stores/eventStore";
import type { ScheduledEvent } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { Clock, Plus } from "lucide-react";

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
  const {
    jobs, logs, loading, error: storeError,
    fetch, createJob, updateJob, deleteJob, toggleJob,
  } = useSchedulerStore();

  const [view, setView] = useState<View>("jobs");
  const [error, setError] = useState("");
  const [selectedJob, setSelectedJob] = useState<ScheduledEvent | null>(null);
  const [form, setForm] = useState(emptyForm);
  const [saving, setSaving] = useState(false);

  const sseEvents = useEventStore((s) => s.eventLogEvents);

  useEffect(() => { fetch(); }, [fetch]);

  useEffect(() => {
    const id = setInterval(fetch, 10_000);
    return () => clearInterval(id);
  }, [fetch]);

  useEffect(() => {
    const schedulerEvents = sseEvents.filter((e) => e.event_type === "scheduler:fired");
    if (schedulerEvents.length > 0) fetch();
  }, [sseEvents, fetch]);

  // Keep selectedJob in sync with store
  useEffect(() => {
    if (selectedJob) {
      const fresh = jobs.find((j) => j.id === selectedJob.id);
      if (fresh) setSelectedJob(fresh);
    }
  }, [jobs, selectedJob?.id]);

  const canSubmitCreate = form.name && (form.triggerType === "timer" ? form.schedule : form.eventPattern);

  const handleCreate = async () => {
    if (!canSubmitCreate) return;
    setSaving(true);
    try {
      setError("");
      await createJob({
        name: form.name,
        text: form.text,
        type: form.type,
        trigger_type: form.triggerType,
        schedule: form.triggerType === "timer" ? form.schedule : undefined,
        event_pattern: form.triggerType === "event" ? form.eventPattern : undefined,
      });
      setForm(emptyForm);
      setView("jobs");
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
      await updateJob(selectedJob.id, {
        name: form.name || undefined,
        text: form.text,
        schedule: form.schedule || undefined,
        event_pattern: form.eventPattern || undefined,
      });
      setView("jobs");
      setForm(emptyForm);
    } catch (e: any) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  };

  const handleToggle = async (job: ScheduledEvent) => {
    try {
      await toggleJob(job);
    } catch (e: any) {
      setError(e.message);
    }
  };

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this job?")) return;
    try {
      setError("");
      await deleteJob(id);
      if (selectedJob?.id === id) {
        setSelectedJob(null);
        setView("jobs");
      }
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

  const displayError = error || storeError || "";

  if (loading) {
    return (
      <div className="p-6">
        <div className="text-sm text-muted-foreground">Loading scheduler…</div>
      </div>
    );
  }

  return (
    <div className="flex h-full gap-4 p-4">
      {/* ===== SIDEBAR ===== */}
      <aside className="flex w-72 flex-col gap-3">
        <div className="flex-1 overflow-y-auto">
          <div className="flex flex-col gap-2">
            <div className="flex items-center justify-between px-1">
              <span className="text-sm font-semibold">Jobs</span>
              <Badge variant="secondary">{jobs.length}</Badge>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="justify-start"
              onClick={() => { setView("new-job"); setError(""); setForm(emptyForm); }}
            >
              <Plus className="h-4 w-4" /> Add Job
            </Button>
            <div className="flex flex-col gap-1">
              {jobs.map((job) => {
                const isActive = selectedJob?.id === job.id && view === "jobs";
                return (
                  <button
                    key={job.id}
                    className={cn(
                      "flex w-full items-start gap-2 rounded-md border border-transparent px-2 py-2 text-left text-sm transition-colors hover:bg-accent",
                      isActive && "border-border bg-accent"
                    )}
                    onClick={() => selectJob(job)}
                  >
                    <span
                      className={cn(
                        "mt-1.5 inline-block h-2 w-2 shrink-0 rounded-full",
                        job.enabled ? "bg-green-500" : "bg-muted-foreground"
                      )}
                    />
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate font-medium">{job.name}</span>
                      <span className="truncate text-xs text-muted-foreground">
                        {(job.trigger_type || "timer") === "timer"
                          ? `${job.schedule} · ${job.type} · ${relativeTime(job.next_fire)}`
                          : `on ${job.event_pattern} · ${job.type}`
                        }
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        </div>

        <Separator />

        <Button
          variant={view === "log" ? "secondary" : "ghost"}
          className="justify-start"
          onClick={() => setView("log")}
        >
          <Clock className="h-4 w-4" />
          Execution Log
          <Badge variant="secondary" className="ml-auto">{logs.length}</Badge>
        </Button>
      </aside>

      {/* ===== CONTENT ===== */}
      <div className="flex-1 overflow-y-auto">
        {displayError && (
          <Alert variant="destructive" className="mb-4">
            <AlertDescription>{displayError}</AlertDescription>
          </Alert>
        )}

        {/* --- Job detail view --- */}
        {view === "jobs" && selectedJob && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle>{selectedJob.name}</CardTitle>
              <div className="flex gap-2">
                <Button variant="secondary" size="sm" onClick={() => startEdit(selectedJob)}>Edit</Button>
                <Button variant="secondary" size="sm" onClick={() => handleToggle(selectedJob)}>
                  {selectedJob.enabled ? "Disable" : "Enable"}
                </Button>
                <Button variant="secondary" size="sm" onClick={() => handleDelete(selectedJob.id)}>Delete</Button>
              </div>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="grid grid-cols-2 gap-4 md:grid-cols-3">
                <div className="flex flex-col gap-1">
                  <span className="text-xs uppercase text-muted-foreground">Trigger</span>
                  <span className="text-sm">{selectedJob.trigger_type || "timer"}</span>
                </div>
                {(selectedJob.trigger_type || "timer") === "timer" ? (
                  <div className="flex flex-col gap-1">
                    <span className="text-xs uppercase text-muted-foreground">Schedule</span>
                    <span className="text-sm">{selectedJob.schedule} ({selectedJob.schedule_type})</span>
                  </div>
                ) : (
                  <div className="flex flex-col gap-1">
                    <span className="text-xs uppercase text-muted-foreground">Event Pattern</span>
                    <span className="text-sm">{selectedJob.event_pattern}</span>
                  </div>
                )}
                <div className="flex flex-col gap-1">
                  <span className="text-xs uppercase text-muted-foreground">Type</span>
                  <span className="text-sm">{selectedJob.type}</span>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs uppercase text-muted-foreground">Status</span>
                  <Badge variant={selectedJob.enabled ? "default" : "secondary"} className="w-fit">
                    {selectedJob.enabled ? "Enabled" : "Disabled"}
                  </Badge>
                </div>
                <div className="flex flex-col gap-1">
                  <span className="text-xs uppercase text-muted-foreground">Fired</span>
                  <span className="text-sm">{selectedJob.fired_count}x</span>
                </div>
                {(selectedJob.trigger_type || "timer") === "timer" && (
                  <div className="flex flex-col gap-1">
                    <span className="text-xs uppercase text-muted-foreground">Next Fire</span>
                    <span className="text-sm">
                      {selectedJob.enabled ? `${formatTime(selectedJob.next_fire)} (${relativeTime(selectedJob.next_fire)})` : "—"}
                    </span>
                  </div>
                )}
              </div>

              {selectedJob.text && (
                <div className="flex flex-col gap-1">
                  <span className="text-xs uppercase text-muted-foreground">Description</span>
                  <span className="text-sm">{selectedJob.text}</span>
                </div>
              )}
            </CardContent>
          </Card>
        )}

        {/* --- Empty state --- */}
        {view === "jobs" && !selectedJob && (
          <Card>
            <CardContent className="py-8 text-sm text-muted-foreground">
              Select a job from the sidebar, or create a new one.
            </CardContent>
          </Card>
        )}

        {/* --- Create job form --- */}
        {view === "new-job" && (
          <Card>
            <CardHeader>
              <CardTitle>New Job</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label>Name</Label>
                <Input
                  placeholder="My scheduled job"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label>Trigger</Label>
                <Select
                  value={form.triggerType}
                  onValueChange={(v) => setForm({ ...form, triggerType: v as "timer" | "event" })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="timer">Timer (cron / interval)</SelectItem>
                    <SelectItem value="event">Event (SDK event pattern)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {form.triggerType === "timer" ? (
                <div className="flex flex-col gap-2">
                  <Label>Schedule</Label>
                  <Input
                    placeholder="5m, 1h30m, or */5 * * * *"
                    value={form.schedule}
                    onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                  />
                  <span className="text-xs text-muted-foreground">Go duration (10s, 5m, 1h) or cron expression (*/5 * * * *)</span>
                </div>
              ) : (
                <div className="flex flex-col gap-2">
                  <Label>Event Pattern</Label>
                  <Input
                    placeholder="task-tracking:assign"
                    value={form.eventPattern}
                    onChange={(e) => setForm({ ...form, eventPattern: e.target.value })}
                  />
                  <span className="text-xs text-muted-foreground">SDK event type to listen for (e.g. task-tracking:assign)</span>
                </div>
              )}
              <div className="flex flex-col gap-2">
                <Label>Type</Label>
                <Select
                  value={form.type}
                  onValueChange={(v) => setForm({ ...form, type: v as "once" | "repeat" })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="repeat">Repeat</SelectItem>
                    <SelectItem value="once">Once</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-2">
                <Label>Description</Label>
                <Textarea
                  placeholder="Optional description or message text"
                  value={form.text}
                  rows={3}
                  onChange={(e) => setForm({ ...form, text: e.target.value })}
                />
              </div>
              <div className="flex justify-end gap-2">
                <Button variant="secondary" onClick={() => setView("jobs")}>Cancel</Button>
                <Button
                  disabled={saving || !canSubmitCreate}
                  onClick={handleCreate}
                >
                  {saving ? "Creating…" : "Create Job"}
                </Button>
              </div>
            </CardContent>
          </Card>
        )}

        {/* --- Edit job form --- */}
        {view === "edit-job" && selectedJob && (
          <Card>
            <CardHeader>
              <CardTitle>Edit Job</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-4">
              <div className="flex flex-col gap-2">
                <Label>Name</Label>
                <Input
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                />
              </div>
              {(selectedJob.trigger_type || "timer") === "timer" ? (
                <div className="flex flex-col gap-2">
                  <Label>Schedule</Label>
                  <Input
                    value={form.schedule}
                    onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                  />
                  <span className="text-xs text-muted-foreground">Go duration (10s, 5m, 1h) or cron expression (*/5 * * * *)</span>
                </div>
              ) : (
                <div className="flex flex-col gap-2">
                  <Label>Event Pattern</Label>
                  <Input
                    value={form.eventPattern}
                    onChange={(e) => setForm({ ...form, eventPattern: e.target.value })}
                  />
                  <span className="text-xs text-muted-foreground">SDK event type to listen for</span>
                </div>
              )}
              <div className="flex flex-col gap-2">
                <Label>Description</Label>
                <Textarea
                  value={form.text}
                  rows={3}
                  onChange={(e) => setForm({ ...form, text: e.target.value })}
                />
              </div>
              <div className="flex justify-end gap-2">
                <Button variant="secondary" onClick={() => { setView("jobs"); }}>Cancel</Button>
                <Button
                  disabled={saving || !form.name}
                  onClick={handleUpdate}
                >
                  {saving ? "Saving…" : "Save Changes"}
                </Button>
              </div>
            </CardContent>
          </Card>
        )}

        {/* --- Execution log --- */}
        {view === "log" && (
          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle>Execution Log</CardTitle>
              <Badge variant="secondary">{logs.length}</Badge>
            </CardHeader>
            <CardContent>
              {logs.length === 0 ? (
                <div className="py-4 text-sm text-muted-foreground">No events have fired yet.</div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Time</TableHead>
                      <TableHead>Job</TableHead>
                      <TableHead>Text</TableHead>
                      <TableHead>Result</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {logs.map((entry) => (
                      <TableRow key={entry.id}>
                        <TableCell className="whitespace-nowrap font-mono text-xs text-muted-foreground">
                          {formatTime(entry.fired_at)}
                        </TableCell>
                        <TableCell className="text-xs font-medium">
                          {entry.job_name}
                        </TableCell>
                        <TableCell className="max-w-[300px] truncate text-xs text-muted-foreground">
                          {entry.text || "—"}
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline">{entry.result}</Badge>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        )}
      </div>
    </div>
  );
}
