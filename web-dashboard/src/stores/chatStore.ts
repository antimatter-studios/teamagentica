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

  /** Map of conversationId → in-flight task. Multiple convs can be sending simultaneously. */
  inFlightTasks: Record<number, InFlightTask>;

  // Derived helpers (not stored, computed from inFlightTasks + activeConversationId).
  /** Whether the *active* conversation is sending. */
  readonly sending: boolean;
  readonly activeTaskGroupId: string | null;
  readonly sendStartedAt: number | null;

  loadConversations: () => Promise<void>;
  selectConversation: (id: number) => Promise<void>;
  newConversation: () => void;
  removeConversation: (id: number) => Promise<void>;
  send: (content: string, attachmentIds?: string[]) => Promise<void>;
  refreshMessages: () => Promise<void>;
  shelfTask: () => void;
  revealShelved: (taskGroupId: string) => void;
}

let _nextTempId = -1;

/** Derive sending state for the active conversation. */
function deriveSending(inFlightTasks: Record<number, InFlightTask>, activeConversationId: number | null) {
  if (!activeConversationId) return { sending: false, activeTaskGroupId: null as string | null, sendStartedAt: null as number | null };
  const task = inFlightTasks[activeConversationId];
  if (!task) return { sending: false, activeTaskGroupId: null as string | null, sendStartedAt: null as number | null };
  return { sending: true, activeTaskGroupId: task.taskGroupId, sendStartedAt: task.startedAt };
}

export const useChatStore = create<ChatStore>((set, get) => ({
  conversations: [],
  activeConversationId: null,
  messages: [],
  loading: false,
  error: null,
  shelvedTasks: [],
  inFlightTasks: {},
  // Derived — initial values, updated via deriveSending() on every relevant set().
  sending: false,
  activeTaskGroupId: null,
  sendStartedAt: null,

  loadConversations: async () => {
    try {
      const conversations = await apiClient.chat.fetchConversations();
      set({ conversations });
    } catch (e: unknown) {
      console.error("Failed to load conversations:", e);
    }
  },

  selectConversation: async (id: number) => {
    const { inFlightTasks } = get();
    set({ loading: true, error: null, activeConversationId: id, ...deriveSending(inFlightTasks, id) });
    try {
      const data = await apiClient.chat.getConversation(id);
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
      // No active conv → not sending.
      sending: false,
      activeTaskGroupId: null,
      sendStartedAt: null,
    });
  },

  removeConversation: async (id: number) => {
    try {
      await apiClient.chat.deleteConversation(id);
      const { activeConversationId, inFlightTasks } = get();
      if (activeConversationId === id) {
        // Also remove any in-flight task for this conversation.
        const { [id]: _, ...rest } = inFlightTasks;
        set({ activeConversationId: null, messages: [], inFlightTasks: rest, sending: false, activeTaskGroupId: null, sendStartedAt: null });
      }
      get().loadConversations();
    } catch (e: unknown) {
      console.error("Failed to delete conversation:", e);
    }
  },

  refreshMessages: async () => {
    const { activeConversationId, inFlightTasks } = get();
    if (!activeConversationId) return;
    const inFlight = inFlightTasks[activeConversationId];
    try {
      const data = await apiClient.chat.getConversation(activeConversationId);
      const msgs = data.messages || [];
      const updates: Partial<ChatStore> = { messages: msgs };

      // If we're waiting for a response and an assistant message appeared, stop.
      if (inFlight) {
        const lastMsg = msgs[msgs.length - 1];
        if (lastMsg && lastMsg.role === "assistant") {
          const { [activeConversationId]: _, ...rest } = inFlightTasks;
          updates.inFlightTasks = rest;
        }
      }

      set(updates as ChatStore);
      // Also refresh the sidebar conversation list to keep unread badges current.
      get().loadConversations();
    } catch {
      // silent — polling failure shouldn't disrupt UX
    }
  },

  shelfTask: () => {
    const { activeConversationId, inFlightTasks } = get();
    if (!activeConversationId) return;
    const inFlight = inFlightTasks[activeConversationId];
    if (!inFlight) return;

    const task: ShelvedTask = {
      taskGroupId: inFlight.taskGroupId,
      message: "Processing...",
      startedAt: inFlight.startedAt,
      status: "processing",
    };

    const { [activeConversationId]: _, ...rest } = inFlightTasks;
    set((state) => ({
      shelvedTasks: [...state.shelvedTasks, task],
      inFlightTasks: rest,
      sending: false,
      activeTaskGroupId: null,
      sendStartedAt: null,
    }));

    // Remove progress messages from the visible chat since the task is shelved.
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

    set({ sending: true, error: null, sendStartedAt: Date.now() });

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

      // Register the in-flight task for this conversation.
      set((state) => {
        const newInFlight = { ...state.inFlightTasks };
        if (taskGroupId) {
          newInFlight[convId!] = { taskGroupId, startedAt: now, conversationId: convId! };
        }
        return {
          messages: [
            ...state.messages.filter((m) => m.id !== tempUserMsg.id),
            resp.user_message,
          ],
          inFlightTasks: newInFlight,
          activeTaskGroupId: taskGroupId,
          sendStartedAt: now,
        };
      });

      // Refresh sidebar.
      get().loadConversations();
    } catch (e: unknown) {
      set({
        sending: false,
        activeTaskGroupId: null,
        sendStartedAt: null,
        error: e instanceof Error ? e.message : "Failed to send message",
      });
    }
  },
}));

// ---------- Periodic background refresh ----------
// Safety net: poll active conversation + conversation list every 10s.
// Catches anything SSE missed (reconnect gaps, events without in-flight tasks).
const POLL_INTERVAL_MS = 10_000;
let _pollTimer: ReturnType<typeof setInterval> | null = null;

function startBackgroundPoll() {
  if (_pollTimer) return;
  _pollTimer = setInterval(() => {
    const state = useChatStore.getState();
    state.loadConversations();
    if (state.activeConversationId) {
      state.refreshMessages();
    }
  }, POLL_INTERVAL_MS);
}

// Auto-start polling when the module loads.
startBackgroundPoll();

// Track which event IDs we've already processed so we scan all new events,
// not just the single newest one (which could be a non-progress event).
let _lastSeenEventId = 0;

useEventStore.subscribe((state, prevState) => {
  const events = state.eventLogEvents;
  const prevEvents = prevState.eventLogEvents;
  if (events.length === 0 || events === prevEvents) return;

  const newCount = events.length - prevEvents.length;
  console.log("[chat-sub] eventLogEvents changed, new events:", newCount, "total:", events.length);

  const chatState = useChatStore.getState();
  const inFlightTasks = chatState.inFlightTasks;
  const shelved = chatState.shelvedTasks;

  // Build set of task group IDs we care about, mapped to their conversation IDs.
  const watchedIds = new Map<string, number>();
  for (const [convIdStr, task] of Object.entries(inFlightTasks)) {
    watchedIds.set(task.taskGroupId, Number(convIdStr));
  }
  const shelvedIds = new Set<string>();
  for (const t of shelved) {
    if (t.status === "processing") shelvedIds.add(t.taskGroupId);
  }

  if (watchedIds.size === 0 && shelvedIds.size === 0) {
    // Still update high-water mark even when not watching.
    if (events[0]?.id > _lastSeenEventId) {
      _lastSeenEventId = events[0].id;
    }
    return;
  }

  // Scan all new events (newest-first) until we hit one we've already seen.
  const refreshedConvs = new Set<number>();
  for (const evt of events) {
    if (evt.id <= _lastSeenEventId) break;
    if (evt.event_type !== "relay:progress") continue;

    try {
      const detail = JSON.parse(evt.detail);
      const tgId = detail.task_group_id;
      if (!tgId) continue;

      // In-flight task — refresh messages if it's the active conversation.
      const convId = watchedIds.get(tgId);
      if (convId !== undefined && !refreshedConvs.has(convId)) {
        const currentState = useChatStore.getState();
        if (currentState.activeConversationId === convId) {
          console.log("[chat-sub] MATCH active conv", convId, "— calling refreshMessages()");
          currentState.refreshMessages();
          refreshedConvs.add(convId);
        } else if (detail.status === "completed" || detail.status === "failed") {
          // Non-active conv completed — clear in-flight + bump unread on that conversation.
          const { [convId]: _, ...rest } = currentState.inFlightTasks;
          useChatStore.setState((s) => ({
            inFlightTasks: rest,
            conversations: s.conversations.map((c) =>
              c.id === convId ? { ...c, unread_count: (c.unread_count ?? 0) + 1 } : c
            ),
          }));
        }
      }

      // Shelved task — update status and message.
      if (shelvedIds.has(tgId)) {
        const shelvedTask = shelved.find((t) => t.taskGroupId === tgId);
        if (shelvedTask) {
          const newStatus = detail.status === "completed" ? "completed"
            : detail.status === "failed" ? "failed"
            : "processing";
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

  // Update high-water mark.
  if (events[0]?.id > _lastSeenEventId) {
    _lastSeenEventId = events[0].id;
  }
});
