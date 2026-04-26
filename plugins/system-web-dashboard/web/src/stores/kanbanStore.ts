import { create } from "zustand";
import { apiClient } from "../api/client";
import type { Board, Column, Epic, Card, Comment } from "@teamagentica/api-client";

interface KanbanStore {
  boards: Board[];
  columns: Column[];
  epics: Epic[];
  cards: Card[];
  activeBoardId: string | null;
  loading: boolean;
  error: string | null;

  // Board actions
  fetchBoards: () => Promise<Board[]>;
  createBoard: (req: { name: string; prefix?: string; description?: string }) => Promise<Board>;
  updateBoard: (id: string, req: { name?: string; prefix?: string; description?: string }) => Promise<Board>;
  setActiveBoard: (id: string) => void;

  // Board content
  fetchBoard: (boardId: string) => Promise<void>;

  // Column actions
  createColumn: (boardId: string, req: { name: string; position: number }) => Promise<Column>;
  updateColumn: (boardId: string, colId: string, req: { name?: string }) => Promise<Column>;
  deleteColumn: (boardId: string, colId: string) => Promise<void>;

  // Epic actions
  createEpic: (boardId: string, req: { name: string; description?: string; color?: string; position?: number }) => Promise<Epic>;
  updateEpic: (boardId: string, epicId: string, req: { name?: string; description?: string; color?: string; position?: number }) => Promise<Epic>;
  deleteEpic: (boardId: string, epicId: string) => Promise<void>;

  // Card actions
  createCard: (boardId: string, req: Parameters<typeof apiClient.tasks.createCard>[1]) => Promise<Card>;
  updateCard: (boardId: string, cardId: string, req: Parameters<typeof apiClient.tasks.updateCard>[2]) => Promise<Card>;
  deleteCard: (boardId: string, cardId: string) => Promise<void>;

  // Local state updates (for optimistic drag-drop)
  setCards: (fn: (cards: Card[]) => Card[]) => void;
  setColumns: (fn: (columns: Column[]) => Column[]) => void;

  // Comment actions
  listComments: (cardId: string) => Promise<Comment[]>;
  createComment: (cardId: string, body: string) => Promise<Comment>;
  deleteComment: (cardId: string, commentId: string) => Promise<void>;
}

export const useKanbanStore = create<KanbanStore>((set) => ({
  boards: [],
  columns: [],
  epics: [],
  cards: [],
  activeBoardId: null,
  loading: true,
  error: null,

  fetchBoards: async () => {
    const boards = await apiClient.tasks.listBoards();
    set({ boards, loading: false });
    return boards;
  },

  createBoard: async (req) => {
    const board = await apiClient.tasks.createBoard(req);
    set((s) => ({ boards: [...s.boards, board] }));
    return board;
  },

  updateBoard: async (id, req) => {
    const board = await apiClient.tasks.updateBoard(id, req);
    set((s) => ({ boards: s.boards.map((b) => (b.id === board.id ? board : b)) }));
    return board;
  },

  setActiveBoard: (id) => set({ activeBoardId: id }),

  fetchBoard: async (boardId) => {
    const [columns, epics, cards] = await Promise.all([
      apiClient.tasks.listColumns(boardId),
      apiClient.tasks.listEpics(boardId).catch(() => [] as Epic[]),
      apiClient.tasks.listCards(boardId),
    ]);
    set({ columns, epics, cards });
  },

  createColumn: async (boardId, req) => {
    const col = await apiClient.tasks.createColumn(boardId, req);
    set((s) => ({ columns: [...s.columns, col] }));
    return col;
  },

  updateColumn: async (boardId, colId, req) => {
    const updated = await apiClient.tasks.updateColumn(boardId, colId, req);
    set((s) => ({ columns: s.columns.map((c) => (c.id === updated.id ? updated : c)) }));
    return updated;
  },

  deleteColumn: async (boardId, colId) => {
    await apiClient.tasks.deleteColumn(boardId, colId);
    set((s) => ({ columns: s.columns.filter((c) => c.id !== colId) }));
  },

  createEpic: async (boardId, req) => {
    const epic = await apiClient.tasks.createEpic(boardId, req);
    set((s) => ({ epics: [...s.epics, epic] }));
    return epic;
  },

  updateEpic: async (boardId, epicId, req) => {
    const updated = await apiClient.tasks.updateEpic(boardId, epicId, req);
    set((s) => ({ epics: s.epics.map((e) => (e.id === updated.id ? updated : e)) }));
    return updated;
  },

  deleteEpic: async (boardId, epicId) => {
    await apiClient.tasks.deleteEpic(boardId, epicId);
    set((s) => ({
      epics: s.epics.filter((e) => e.id !== epicId),
      cards: s.cards.map((c) => c.epic_id === epicId ? { ...c, epic_id: "" } : c),
    }));
  },

  createCard: async (boardId, req) => {
    const card = await apiClient.tasks.createCard(boardId, req);
    set((s) => ({ cards: [...s.cards, card] }));
    return card;
  },

  updateCard: async (boardId, cardId, req) => {
    const updated = await apiClient.tasks.updateCard(boardId, cardId, req);
    set((s) => ({ cards: s.cards.map((c) => (c.id === updated.id ? updated : c)) }));
    return updated;
  },

  deleteCard: async (boardId, cardId) => {
    await apiClient.tasks.deleteCard(boardId, cardId);
    set((s) => ({ cards: s.cards.filter((c) => c.id !== cardId) }));
  },

  setCards: (fn) => set((s) => ({ cards: fn(s.cards) })),
  setColumns: (fn) => set((s) => ({ columns: fn(s.columns) })),

  listComments: (cardId) => apiClient.tasks.listComments(cardId),
  createComment: (cardId, body) => apiClient.tasks.createComment(cardId, body),
  deleteComment: (cardId, commentId) => apiClient.tasks.deleteComment(cardId, commentId),
}));
