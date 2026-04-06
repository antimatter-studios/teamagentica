import { create } from "zustand";
import { apiClient } from "../api/client";
import type { Conversation, ChatMessage } from "@teamagentica/api-client";
import { useEventStore } from "./eventStore";

export interface ShelvedTask {
  taskGroupId: string;
  message: string;
  startedAt: number;
  status: "processing" | "completed" | "failed";
}

/** SSE-driven progress for a conversation — updated directly from relay:progress events. */
export interface ProgressInfo {
  status: string;   // thinking, running, streaming, planning, synthesizing
  message: string;
  taskGroupId: string;
  updatedAt: number;
}

/** Per-conversation in-flight task state. */
interface InFlightTask {
  taskGroupId: string;
  startedAt: number;
  conversationId: number;
}

interface ChatStore {
  conversations: Conversation[];
  activeConversationId: number | null;
  messages: ChatMessage[];
  loading: boolean;
  error: string | null;
  shelvedTasks: ShelvedTask[];

  /** SSE-driven progress per conversation — multiple tasks can be in-flight simultaneously. */
  progressInfo: Record<number, ProgressInfo[]>;

  /** Map of conversationId → in-flight tasks. Multiple tasks per conv supported. */
  inFlightTasks: Record<number, InFlightTask[]>;

  /** True only during the HTTP send call (prevents double-click). */
  sending: boolean;

  loadConversations: () => Promise<void>;
  selectConversation: (id: number) => Promise<void>;
  newConversation: () => void;
  removeConversation: (id: number) => Promise<void>;
  send: (content: string, attachmentIds?: string[]) => Promise<void>;
  refreshMessages: () => Promise<void>;
  setProgressInfo: (convId: number, info: ProgressInfo) => void;
  clearProgressInfo: (convId: number, taskGroupId?: string) => void;
  shelfTask: (taskGroupId: string) => void;
  revealShelved: (taskGroupId: string) => void;
}

let _nextTempId = -1;

// Persist in-flight and shelved tasks across browser refreshes so SSE matching survives.
const INFLIGHT_KEY = "teamagentica_inflight_tasks";
const SHELVED_KEY = "teamagentica_shelved_tasks";

function loadPersistedInFlight(): Record<number, InFlightTask[]> {
  try {
    const raw = localStorage.getItem(INFLIGHT_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch { return {}; }
}

function loadPersistedShelved(): ShelvedTask[] {
  try {
    const raw = localStorage.getItem(SHELVED_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch { return []; }
}

function persistInFlight(tasks: Record<number, InFlightTask[]>) {
  try { localStorage.setItem(INFLIGHT_KEY, JSON.stringify(tasks)); } catch {}
}

function persistShelved(tasks: ShelvedTask[]) {
  try { localStorage.setItem(SHELVED_KEY, JSON.stringify(tasks)); } catch {}
}

const _restoredInFlight = loadPersistedInFlight();
const _restoredShelved = loadPersistedShelved();

export const useChatStore = create<ChatStore>((set, get) => ({
  conversations: [],
  activeConversationId: null,
  messages: [],
  loading: false,
  error: null,
  shelvedTasks: _restoredShelved,
  progressInfo: {},
  inFlightTasks: _restoredInFlight,
  sending: false,

  loadConversations: async () => {
    try {
      const conversations = await apiClient.chat.fetchConversations();
      conversations.sort((a, b) => new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime());
      set({ conversations });
    } catch (e: unknown) {
      console.error("Failed to load conversations:", e);
    }
  },

  selectConversation: async (id: number) => {
    set({ loading: true, error: null, activeConversationId: id, messages: [] });
    try {
      const data = await apiClient.chat.getConversation(id);
      // Guard: only apply if this conversation is still active (user may have clicked another).
      if (get().activeConversationId !== id) {
        set({ loading: false });
        return;
      }
      set({
        messages: data.messages || [],
        loading: false,
        // Clear unread count for this conversation in local state.
        conversations: get().conversations.map((c) =>
          c.id === id ? { ...c, unread_count: 0 } : c
        ),
      });
      // Mark as read on server (fire-and-forget).
      apiClient.chat.markRead(id).catch(() => {});
    } catch (e: unknown) {
      set({
        loading: false,
        error: e instanceof Error ? e.message : "Failed to load conversation",
      });
    }
  },

  newConversation: () => {
    set({
      activeConversationId: null,
      messages: [],
      error: null,
    });
  },

  removeConversation: async (id: number) => {
    try {
      await apiClient.chat.deleteConversation(id);
      const { activeConversationId, inFlightTasks } = get();
      if (activeConversationId === id) {
        const { [id]: _, ...rest } = inFlightTasks;
        set({ activeConversationId: null, messages: [], inFlightTasks: rest });
      }
      get().loadConversations();
    } catch (e: unknown) {
      console.error("Failed to delete conversation:", e);
    }
  },

  setProgressInfo: (convId: number, info: ProgressInfo) => {
    set((state) => {
      const existing = state.progressInfo[convId] || [];
      const idx = existing.findIndex((p) => p.taskGroupId === info.taskGroupId);
      const updated = idx >= 0
        ? existing.map((p, i) => (i === idx ? info : p))
        : [...existing, info];
      return { progressInfo: { ...state.progressInfo, [convId]: updated } };
    });
  },

  clearProgressInfo: (convId: number, taskGroupId?: string) => {
    set((state) => {
      if (!taskGroupId) {
        const { [convId]: _, ...rest } = state.progressInfo;
        return { progressInfo: rest };
      }
      const existing = state.progressInfo[convId] || [];
      const filtered = existing.filter((p) => p.taskGroupId !== taskGroupId);
      if (filtered.length === 0) {
        const { [convId]: _, ...rest } = state.progressInfo;
        return { progressInfo: rest };
      }
      return { progressInfo: { ...state.progressInfo, [convId]: filtered } };
    });
  },

  refreshMessages: async () => {
    const { activeConversationId } = get();
    if (!activeConversationId) return;
    try {
      const data = await apiClient.chat.getConversation(activeConversationId);
      const msgs = data.messages || [];
      set({ messages: msgs });
      get().loadConversations();
    } catch {
      // silent — polling failure shouldn't disrupt UX
    }
  },

  shelfTask: (taskGroupId: string) => {
    const { activeConversationId, inFlightTasks } = get();
    if (!activeConversationId) return;
    const tasks = inFlightTasks[activeConversationId] || [];
    const inFlight = tasks.find((t) => t.taskGroupId === taskGroupId);
    if (!inFlight) return;

    const shelved: ShelvedTask = {
      taskGroupId: inFlight.taskGroupId,
      message: "Processing...",
      startedAt: inFlight.startedAt,
      status: "processing",
    };

    const remaining = tasks.filter((t) => t.taskGroupId !== taskGroupId);
    set((state) => ({
      shelvedTasks: [...state.shelvedTasks, shelved],
      inFlightTasks: {
        ...state.inFlightTasks,
        ...(remaining.length > 0
          ? { [activeConversationId]: remaining }
          : (() => { const { [activeConversationId]: _, ...rest } = state.inFlightTasks; return rest; })()),
      },
    }));

    get().clearProgressInfo(activeConversationId, taskGroupId);
    get().refreshMessages();
  },

  revealShelved: (taskGroupId: string) => {
    set((state) => ({
      shelvedTasks: state.shelvedTasks.filter((t) => t.taskGroupId !== taskGroupId),
    }));
    // Refresh to show the completed message that the backend already stored.
    get().refreshMessages();
  },

  send: async (content: string, attachmentIds?: string[]) => {
    const { activeConversationId, messages } = get();

    set({ sending: true, error: null });

    try {
      // Create conversation lazily if needed.
      let convId = activeConversationId;
      if (!convId) {
        const conv = await apiClient.chat.createConversation();
        convId = conv.id;
        set({ activeConversationId: convId });
      }

      // Optimistic user message.
      const tempUserMsg: ChatMessage = {
        id: _nextTempId--,
        conversation_id: convId,
        role: "user",
        content,
        created_at: new Date().toISOString(),
      };
      set({ messages: [...messages, tempUserMsg] });

      const resp = await apiClient.chat.sendMessage(
        convId,
        content,
        attachmentIds
      );

      const taskGroupId = resp.task_group_id || null;
      const now = Date.now();

      // Register the in-flight task and show instant progress bubble.
      set((state) => {
        const existing = state.inFlightTasks[convId!] || [];
        const existingProgress = state.progressInfo[convId!] || [];
        return {
          sending: false,
          messages: [
            ...state.messages.filter((m) => m.id !== tempUserMsg.id),
            resp.user_message,
          ],
          ...(taskGroupId ? {
            inFlightTasks: {
              ...state.inFlightTasks,
              [convId!]: [...existing, { taskGroupId, startedAt: now, conversationId: convId! }],
            },
            progressInfo: {
              ...state.progressInfo,
              [convId!]: [...existingProgress, {
                status: "thinking",
                message: "Sending to agent...",
                taskGroupId,
                updatedAt: now,
              }],
            },
          } : {}),
        };
      });

      // Refresh sidebar.
      get().loadConversations();
    } catch (e: unknown) {
      set({
        sending: false,
        error: e instanceof Error ? e.message : "Failed to send message",
      });
    }
  },
}));

// Persist in-flight and shelved tasks to localStorage whenever they change.
useChatStore.subscribe((state, prev) => {
  if (state.inFlightTasks !== prev.inFlightTasks) persistInFlight(state.inFlightTasks);
  if (state.shelvedTasks !== prev.shelvedTasks) persistShelved(state.shelvedTasks);
});

// ---------- Periodic background refresh ----------
// Safety net: poll active conversation + conversation list every 10s.
// Catches anything SSE missed (reconnect gaps, events without in-flight tasks).
// Demoted to 30s — SSE now drives real-time updates; polling is just a safety net.
const POLL_INTERVAL_MS = 30_000;
let _pollTimer: ReturnType<typeof setInterval> | null = null;

const STALE_TASK_MS = 120_000; // 2 minutes — clear zombie bubbles

function startBackgroundPoll() {
  if (_pollTimer) return;
  _pollTimer = setInterval(() => {
    const state = useChatStore.getState();
    state.loadConversations();
    if (state.activeConversationId) {
      state.refreshMessages();
    }

    // Reap zombie in-flight tasks — if a task has been pending for > 2 min,
    // the completed event was likely missed. Clear it and refresh.
    const now = Date.now();
    const inFlight = state.inFlightTasks;
    let needsRefresh = false;
    for (const [convIdStr, tasks] of Object.entries(inFlight)) {
      const convId = Number(convIdStr);
      const stale = tasks.filter((t) => now - t.startedAt > STALE_TASK_MS);
      for (const t of stale) {
        console.log("[chat-poll] reaping stale task", t.taskGroupId, "for conv", convId);
        state.clearProgressInfo(convId, t.taskGroupId);
        needsRefresh = true;
      }
      if (stale.length > 0) {
        const remaining = tasks.filter((t) => now - t.startedAt <= STALE_TASK_MS);
        const currentInFlight = useChatStore.getState().inFlightTasks;
        if (remaining.length > 0) {
          useChatStore.setState({ inFlightTasks: { ...currentInFlight, [convId]: remaining } });
        } else {
          const { [convId]: _, ...rest } = currentInFlight;
          useChatStore.setState({ inFlightTasks: rest });
        }
      }
    }
    if (needsRefresh) state.refreshMessages();
  }, POLL_INTERVAL_MS);
}

// Auto-start polling when the module loads.
startBackgroundPoll();

// Track which task_group_id + status combos we've already handled.
const _processedEvents = new Set<string>();

useEventStore.subscribe((state, prevState) => {
  const events = state.eventLogEvents;
  const prevEvents = prevState.eventLogEvents;
  if (events.length === 0 || events === prevEvents) return;

  const chatState = useChatStore.getState();
  const inFlightTasks = chatState.inFlightTasks;
  const shelved = chatState.shelvedTasks;

  // Build set of task group IDs we care about.
  const watchedIds = new Map<string, number>();
  for (const [convIdStr, tasks] of Object.entries(inFlightTasks)) {
    for (const task of tasks) {
      watchedIds.set(task.taskGroupId, Number(convIdStr));
    }
  }
  const shelvedIds = new Set<string>();
  for (const t of shelved) {
    if (t.status === "processing") shelvedIds.add(t.taskGroupId);
  }

  if (watchedIds.size === 0 && shelvedIds.size === 0) return;

  // Scan ALL events — deduplicate by tg+status+timestamp to avoid reprocessing.
  const refreshedConvs = new Set<number>();
  for (const evt of events) {
    if (evt.event_type !== "relay:progress") continue;
    const evtKey = `${evt.created_at}:${evt.detail?.substring(0, 80)}`;
    if (_processedEvents.has(evtKey)) continue;
    _processedEvents.add(evtKey);

    try {
      const detail = JSON.parse(evt.detail);
      const tgId = detail.task_group_id;
      if (!tgId) continue;

      const isTerminal = detail.status === "completed" || detail.status === "failed";

      // In-flight task — update progress directly from SSE data.
      const convId = watchedIds.get(tgId);
      if (convId !== undefined) {
        const currentState = useChatStore.getState();

        if (isTerminal) {
          // Terminal: clear progress + in-flight task for this specific taskGroupId.
          console.log("[chat-sub] MATCH conv", convId, "terminal:", detail.status);
          currentState.clearProgressInfo(convId, tgId);
          const tasks = currentState.inFlightTasks[convId] || [];
          const remaining = tasks.filter((t) => t.taskGroupId !== tgId);
          if (remaining.length > 0) {
            useChatStore.setState({
              inFlightTasks: { ...currentState.inFlightTasks, [convId]: remaining },
            });
          } else {
            const { [convId]: _, ...rest } = currentState.inFlightTasks;
            useChatStore.setState({ inFlightTasks: rest });
          }
          if (currentState.activeConversationId === convId && !refreshedConvs.has(convId)) {
            currentState.refreshMessages();
            refreshedConvs.add(convId);
          } else if (currentState.activeConversationId !== convId) {
            useChatStore.setState((s) => ({
              conversations: s.conversations.map((c) =>
                c.id === convId ? { ...c, unread_count: (c.unread_count ?? 0) + 1 } : c
              ),
            }));
          }
        } else {
          // Intermediate: update progress directly — no REST call needed.
          console.log("[chat-sub] SSE progress conv", convId, ":", detail.status, detail.message);
          currentState.setProgressInfo(convId, {
            status: detail.status,
            message: detail.message || `${detail.status}...`,
            taskGroupId: tgId,
            updatedAt: Date.now(),
          });
        }
      }

      // Shelved task — update status and message.
      if (shelvedIds.has(tgId)) {
        const shelvedTask = shelved.find((t) => t.taskGroupId === tgId);
        if (shelvedTask) {
          const newStatus = isTerminal ? (detail.status === "completed" ? "completed" : "failed") : "processing";
          const newMessage = detail.message || shelvedTask.message;

          if (newStatus !== shelvedTask.status || newMessage !== shelvedTask.message) {
            useChatStore.setState((s) => ({
              shelvedTasks: s.shelvedTasks.map((t) =>
                t.taskGroupId === tgId
                  ? { ...t, status: newStatus, message: newMessage }
                  : t
              ),
            }));
          }
        }
      }
    } catch {
      // skip unparseable
    }
  }

  // Prune old entries from the processed set to prevent memory leak.
  if (_processedEvents.size > 1000) {
    const entries = Array.from(_processedEvents);
    entries.splice(0, entries.length - 500);
    _processedEvents.clear();
    entries.forEach((e) => _processedEvents.add(e));
  }
});
