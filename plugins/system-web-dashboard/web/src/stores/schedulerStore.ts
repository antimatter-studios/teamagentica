import { create } from "zustand";
import { apiClient } from "../api/client";
import type { ScheduledEvent, EventLogEntry } from "@teamagentica/api-client";

interface SchedulerStore {
  jobs: ScheduledEvent[];
  logs: EventLogEntry[];
  loading: boolean;
  error: string | null;

  fetch: () => Promise<void>;

  createJob: (req: Parameters<typeof apiClient.scheduler.createEvent>[0]) => Promise<void>;
  updateJob: (id: string, req: Parameters<typeof apiClient.scheduler.updateEvent>[1]) => Promise<void>;
  deleteJob: (id: string) => Promise<void>;
  toggleJob: (job: ScheduledEvent) => Promise<void>;
}

export const useSchedulerStore = create<SchedulerStore>((set, get) => ({
  jobs: [],
  logs: [],
  loading: true,
  error: null,

  fetch: async () => {
    if (!apiClient.scheduler) return;
    if (get().jobs.length === 0) set({ loading: true });
    try {
      const errors: string[] = [];
      const [evRes, logRes] = await Promise.all([
        apiClient.scheduler.listEvents().catch((e: Error) => { errors.push(`jobs: ${e.message}`); return { events: [] as any[] }; }),
        apiClient.scheduler.getLogs(50).catch((e: Error) => { errors.push(`logs: ${e.message}`); return { entries: [] as any[] }; }),
      ]);
      set({ jobs: evRes.events || [], logs: logRes.entries || [], loading: false, error: errors.length ? errors.join("; ") : null });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Failed to load scheduler" });
    }
  },

  createJob: async (req) => {
    await apiClient.scheduler.createEvent(req);
    await get().fetch();
  },

  updateJob: async (id, req) => {
    await apiClient.scheduler.updateEvent(id, req);
    await get().fetch();
  },

  deleteJob: async (id) => {
    await apiClient.scheduler.deleteEvent(id);
    await get().fetch();
  },

  toggleJob: async (job) => {
    await apiClient.scheduler.updateEvent(job.id, { enabled: !job.enabled });
    await get().fetch();
  },
}));
