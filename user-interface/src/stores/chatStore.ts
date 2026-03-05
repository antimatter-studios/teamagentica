import { create } from "zustand";
import {
  fetchAgents,
  fetchConversations,
  getConversation,
  createConversation,
  deleteConversation,
  sendMessage,
  type Agent,
  type Conversation,
  type ChatMessage,
} from "../api/chat";

interface ChatStore {
  agents: Agent[];
  hasCoordinator: boolean;
  conversations: Conversation[];
  activeConversationId: number | null;
  messages: ChatMessage[];
  selectedAgent: string;
  sending: boolean;
  loading: boolean;
  error: string | null;

  loadAgents: () => Promise<void>;
  loadConversations: () => Promise<void>;
  selectConversation: (id: number) => Promise<void>;
  newConversation: () => void;
  removeConversation: (id: number) => Promise<void>;
  setSelectedAgent: (alias: string) => void;
  send: (content: string, attachmentIds?: string[]) => Promise<void>;
}

let _nextTempId = -1;

export const useChatStore = create<ChatStore>((set, get) => ({
  agents: [],
  hasCoordinator: false,
  conversations: [],
  activeConversationId: null,
  messages: [],
  selectedAgent: "",
  sending: false,
  loading: false,
  error: null,

  loadAgents: async () => {
    try {
      const { agents, has_coordinator } = await fetchAgents();
      set({ agents, hasCoordinator: has_coordinator });
      // Auto-select: prefer "auto" when coordinator available, else first agent.
      if (!get().selectedAgent || get().selectedAgent === "") {
        if (has_coordinator) {
          set({ selectedAgent: "auto" });
        } else if (agents.length > 0) {
          set({ selectedAgent: agents[0].alias });
        }
      }
    } catch (e: unknown) {
      console.error("Failed to load agents:", e);
    }
  },

  loadConversations: async () => {
    try {
      const conversations = await fetchConversations();
      set({ conversations });
    } catch (e: unknown) {
      console.error("Failed to load conversations:", e);
    }
  },

  selectConversation: async (id: number) => {
    set({ loading: true, error: null });
    try {
      const data = await getConversation(id);
      set({
        activeConversationId: id,
        messages: data.messages || [],
        loading: false,
      });
      if (data.conversation.default_agent) {
        set({ selectedAgent: data.conversation.default_agent });
      }
    } catch (e: unknown) {
      set({
        loading: false,
        error: e instanceof Error ? e.message : "Failed to load conversation",
      });
    }
  },

  newConversation: () => {
    const { hasCoordinator, agents } = get();
    const defaultAgent = hasCoordinator
      ? "auto"
      : agents.length > 0
        ? agents[0].alias
        : "";
    set({
      activeConversationId: null,
      messages: [],
      error: null,
      selectedAgent: defaultAgent,
    });
  },

  removeConversation: async (id: number) => {
    try {
      await deleteConversation(id);
      const { activeConversationId } = get();
      if (activeConversationId === id) {
        set({ activeConversationId: null, messages: [] });
      }
      get().loadConversations();
    } catch (e: unknown) {
      console.error("Failed to delete conversation:", e);
    }
  },

  setSelectedAgent: (alias: string) => set({ selectedAgent: alias }),

  send: async (content: string, attachmentIds?: string[]) => {
    let { activeConversationId, selectedAgent, hasCoordinator, messages } = get();
    // Default to "auto" when coordinator is available but nothing explicitly picked.
    if (!selectedAgent && hasCoordinator) {
      selectedAgent = "auto";
      set({ selectedAgent });
    }
    if (!selectedAgent) {
      set({ error: "No agent selected" });
      return;
    }

    set({ sending: true, error: null });

    try {
      // Create conversation lazily if needed.
      let convId = activeConversationId;
      if (!convId) {
        const conv = await createConversation(selectedAgent);
        convId = conv.id;
        set({ activeConversationId: convId });
      }

      // Optimistic user message.
      const tempUserMsg: ChatMessage = {
        id: _nextTempId--,
        conversation_id: convId,
        role: "user",
        content,
        agent_alias: selectedAgent,
        created_at: new Date().toISOString(),
      };
      set({ messages: [...messages, tempUserMsg] });

      const resp = await sendMessage(
        convId,
        content,
        selectedAgent,
        attachmentIds
      );

      // Replace optimistic message with real ones.
      set((state) => ({
        messages: [
          ...state.messages.filter((m) => m.id !== tempUserMsg.id),
          resp.user_message,
          resp.assistant_message,
        ],
        sending: false,
      }));

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
