import { create } from "zustand";
import { API_BASE } from "../api/client";

export interface DebugEvent {
  timestamp: string;
  type: string;
  plugin_id: string;
  method?: string;
  path?: string;
  status?: number;
  duration_ms?: number;
  detail?: string;
}

export interface EventLogEntry {
  id: number;
  event_type: string;
  source_plugin_id: string;
  target_plugin_id: string;
  status: string;
  detail: string;
  created_at: string;
}

interface EventStore {
  auditEvents: DebugEvent[];
  eventLogEvents: EventLogEntry[];
  connected: boolean;
  connect: () => void;
  disconnect: () => void;
  clear: () => void;
}

const MAX_EVENTS = 500;

let abortController: AbortController | null = null;

async function readSSE(
  signal: AbortSignal,
  onAudit: (evt: DebugEvent) => void,
  onEvent: (entry: EventLogEntry) => void,
  onStatus: (connected: boolean) => void,
) {
  const token = localStorage.getItem("teamagentica_token");
  if (!token) return;

  const res = await fetch(`${API_BASE}/api/debug/events`, {
    headers: { Authorization: `Bearer ${token}` },
    signal,
  });

  if (!res.ok || !res.body) {
    onStatus(false);
    return;
  }

  onStatus(true);
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let currentChannel = "audit";

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() || "";

    for (const line of lines) {
      if (line.startsWith("event: ")) {
        currentChannel = line.slice(7).trim();
      } else if (line.startsWith("data: ")) {
        try {
          const parsed = JSON.parse(line.slice(6));
          if (currentChannel === "event") {
            onEvent(parsed as EventLogEntry);
          } else {
            onAudit(parsed as DebugEvent);
          }
        } catch {}
        currentChannel = "audit"; // reset after consuming data
      }
    }
  }
  onStatus(false);
}

export const useEventStore = create<EventStore>((set) => ({
  auditEvents: [],
  eventLogEvents: [],
  connected: false,

  connect: () => {
    // Abort any existing connection
    abortController?.abort();
    const ac = new AbortController();
    abortController = ac;

    const addAudit = (evt: DebugEvent) => {
      set((state) => {
        const next = [...state.auditEvents, evt];
        return { auditEvents: next.length > MAX_EVENTS ? next.slice(-MAX_EVENTS) : next };
      });
    };

    const addEventLog = (entry: EventLogEntry) => {
      set((state) => {
        const next = [entry, ...state.eventLogEvents];
        return { eventLogEvents: next.length > MAX_EVENTS ? next.slice(0, MAX_EVENTS) : next };
      });
    };

    const setConnected = (connected: boolean) => set({ connected });

    const startSSE = () => {
      readSSE(ac.signal, addAudit, addEventLog, setConnected).catch(() => {
        set({ connected: false });
        // Auto-reconnect after 3s unless aborted
        if (!ac.signal.aborted) {
          setTimeout(startSSE, 3000);
        }
      });
    };

    // Load history first, then start SSE
    const token = localStorage.getItem("teamagentica_token");
    if (token) {
      fetch(`${API_BASE}/api/debug/history`, {
        headers: { Authorization: `Bearer ${token}` },
        signal: ac.signal,
      })
        .then((res) => (res.ok ? res.json() : null))
        .then((data) => {
          if (data?.events?.length) {
            const msgs = data.events as Array<{ channel: string; data: any }>;
            const audits: DebugEvent[] = [];
            const evtLogs: EventLogEntry[] = [];
            for (const msg of msgs) {
              if (msg.channel === "event") {
                evtLogs.push(msg.data as EventLogEntry);
              } else {
                audits.push(msg.data as DebugEvent);
              }
            }
            set({
              auditEvents: audits.slice(-MAX_EVENTS),
              eventLogEvents: evtLogs.reverse().slice(0, MAX_EVENTS),
            });
          }
        })
        .catch(() => {})
        .finally(() => startSSE());
    } else {
      startSSE();
    }
  },

  disconnect: () => {
    abortController?.abort();
    abortController = null;
    set({ connected: false });
  },

  clear: () => set({ auditEvents: [], eventLogEvents: [] }),
}));
