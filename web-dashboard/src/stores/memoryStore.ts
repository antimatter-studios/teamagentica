import { create } from "zustand";
import { apiClient } from "../api/client";
import type { Memory, MemoryEntity, LCMConversation, LCMMessage } from "@teamagentica/api-client";

interface MemoryStore {
  memories: Memory[];
  entities: MemoryEntity[];
  searchResults: Memory[] | null;
  loading: boolean;
  searching: boolean;
  error: string | null;

  // LCM (episodic) state
  conversations: LCMConversation[];
  conversationMessages: LCMMessage[];
  conversationTotal: number;
  selectedConversationId: number | null;
  loadingConversations: boolean;
  loadingMessages: boolean;

  // Filters
  selectedUserId: string;
  selectedAgentId: string;

  // Actions
  fetch: () => Promise<void>;
  fetchEntities: () => Promise<void>;
  search: (query: string) => Promise<void>;
  clearSearch: () => void;
  deleteMemory: (id: string) => Promise<void>;
  setFilter: (key: "selectedUserId" | "selectedAgentId", value: string) => void;

  // LCM actions
  fetchConversations: () => Promise<void>;
  selectConversation: (id: number | null) => Promise<void>;
  loadMoreMessages: () => Promise<void>;
}

export const useMemoryStore = create<MemoryStore>((set, get) => ({
  memories: [],
  entities: [],
  searchResults: null,
  loading: true,
  searching: false,
  error: null,
  selectedUserId: "",
  selectedAgentId: "",

  // LCM state
  conversations: [],
  conversationMessages: [],
  conversationTotal: 0,
  selectedConversationId: null,
  loadingConversations: false,
  loadingMessages: false,

  fetch: async () => {
    const { selectedUserId, selectedAgentId } = get();
    if (get().memories.length === 0) set({ loading: true });
    try {
      const opts: Record<string, unknown> = { page_size: 200 };
      if (selectedUserId) opts.user_id = selectedUserId;
      if (selectedAgentId) opts.agent_id = selectedAgentId;
      const memories = await apiClient.memory.list(opts as Parameters<typeof apiClient.memory.list>[0]);
      set({ memories, loading: false, error: null });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Failed to load memories" });
    }
  },

  fetchEntities: async () => {
    try {
      const entities = await apiClient.memory.listEntities();
      set({ entities });
    } catch {
      // non-critical
    }
  },

  search: async (query: string) => {
    if (!query.trim()) {
      set({ searchResults: null });
      return;
    }
    set({ searching: true });
    try {
      const { selectedUserId, selectedAgentId } = get();
      const opts: Record<string, unknown> = { top_k: 50 };
      if (selectedUserId) opts.user_id = selectedUserId;
      if (selectedAgentId) opts.agent_id = selectedAgentId;
      const results = await apiClient.memory.search(query, opts as Parameters<typeof apiClient.memory.search>[1]);
      set({ searchResults: results, searching: false });
    } catch (err) {
      set({ searching: false, error: err instanceof Error ? err.message : "Search failed" });
    }
  },

  clearSearch: () => set({ searchResults: null }),

  deleteMemory: async (id: string) => {
    await apiClient.memory.delete(id);
    set((s) => ({
      memories: s.memories.filter((m) => m.id !== id),
      searchResults: s.searchResults?.filter((m) => m.id !== id) ?? null,
    }));
  },

  setFilter: (key, value) => {
    set({ [key]: value, searchResults: null });
    // Re-fetch after filter change
    setTimeout(() => get().fetch(), 0);
  },

  // ── LCM actions ──

  fetchConversations: async () => {
    set({ loadingConversations: true });
    try {
      const conversations = await apiClient.memory.listConversations();
      set({ conversations, loadingConversations: false });
    } catch (err) {
      set({
        loadingConversations: false,
        error: err instanceof Error ? err.message : "Failed to load conversations",
      });
    }
  },

  selectConversation: async (id: number | null) => {
    set({ selectedConversationId: id, conversationMessages: [], conversationTotal: 0 });
    if (id === null) return;

    set({ loadingMessages: true });
    try {
      const res = await apiClient.memory.getConversationMessages(id, { limit: 100 });
      set({ conversationMessages: res.messages, conversationTotal: res.total, loadingMessages: false });
    } catch (err) {
      set({
        loadingMessages: false,
        error: err instanceof Error ? err.message : "Failed to load messages",
      });
    }
  },

  loadMoreMessages: async () => {
    const { selectedConversationId, conversationMessages, conversationTotal } = get();
    if (!selectedConversationId || conversationMessages.length >= conversationTotal) return;

    set({ loadingMessages: true });
    try {
      const res = await apiClient.memory.getConversationMessages(selectedConversationId, {
        limit: 100,
        offset: conversationMessages.length,
      });
      set((s) => ({
        conversationMessages: [...s.conversationMessages, ...res.messages],
        loadingMessages: false,
      }));
    } catch (err) {
      set({
        loadingMessages: false,
        error: err instanceof Error ? err.message : "Failed to load messages",
      });
    }
  },
}));
