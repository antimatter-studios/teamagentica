import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/tool-task-tracker";

export interface Board {
  id: string;
  name: string;
  prefix: string;
  description: string;
  created_at: number;
  updated_at: number;
}

export interface Column {
  id: string;
  board_id: string;
  name: string;
  position: number;
  created_at: number;
  updated_at: number;
}

export interface Epic {
  id: string;
  board_id: string;
  name: string;
  description: string;
  color: string;
  position: number;
  created_at: number;
  updated_at: number;
}

export interface Card {
  id: string;
  number: number;          // auto-incrementing per board
  board_id: string;
  column_id: string;
  epic_id: string;             // epic grouping ("" = ungrouped)
  title: string;
  description: string;
  card_type: "task" | "bug" | "";
  priority: "low" | "medium" | "high" | "urgent" | "";
  assignee_id: number;       // user ID (0 = unassigned)
  assignee_agent: string;    // agent alias ("" = none)
  assignee_name: string;     // resolved display name from server
  labels: string;            // comma-separated
  due_date: number | null;
  position: number;
  created_at: number;
  updated_at: number;
}

export interface Comment {
  id: string;
  card_id: string;
  author_id: number;       // user ID
  author_name: string;     // resolved display name from server
  body: string;
  created_at: number;
}

export class TasksAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  // Boards
  async listBoards(): Promise<Board[]> {
    return this.http.get<Board[]>(`${ROUTE}/boards`);
  }

  async createBoard(data: { name: string; prefix?: string; description?: string }): Promise<Board> {
    return this.http.post<Board>(`${ROUTE}/boards`, data);
  }

  async getBoard(id: string): Promise<Board> {
    return this.http.get<Board>(`${ROUTE}/boards/${id}`);
  }

  async updateBoard(id: string, data: { name?: string; prefix?: string; description?: string }): Promise<Board> {
    return this.http.put<Board>(`${ROUTE}/boards/${id}`, data);
  }

  async deleteBoard(id: string): Promise<void> {
    return this.http.delete(`${ROUTE}/boards/${id}`);
  }

  // Columns
  async listColumns(boardId: string): Promise<Column[]> {
    return this.http.get<Column[]>(`${ROUTE}/boards/${boardId}/columns`);
  }

  async createColumn(boardId: string, data: { name: string; position?: number }): Promise<Column> {
    return this.http.post<Column>(`${ROUTE}/boards/${boardId}/columns`, data);
  }

  async updateColumn(boardId: string, colId: string, data: { name?: string; position?: number }): Promise<Column> {
    return this.http.put<Column>(`${ROUTE}/boards/${boardId}/columns/${colId}`, data);
  }

  async deleteColumn(boardId: string, colId: string): Promise<void> {
    return this.http.delete(`${ROUTE}/boards/${boardId}/columns/${colId}`);
  }

  // Epics
  async listEpics(boardId: string): Promise<Epic[]> {
    return this.http.get<Epic[]>(`${ROUTE}/boards/${boardId}/epics`);
  }

  async createEpic(boardId: string, data: {
    name: string;
    description?: string;
    color?: string;
    position?: number;
  }): Promise<Epic> {
    return this.http.post<Epic>(`${ROUTE}/boards/${boardId}/epics`, data);
  }

  async updateEpic(boardId: string, epicId: string, data: {
    name?: string;
    description?: string;
    color?: string;
    position?: number;
  }): Promise<Epic> {
    return this.http.put<Epic>(`${ROUTE}/boards/${boardId}/epics/${epicId}`, data);
  }

  async deleteEpic(boardId: string, epicId: string): Promise<void> {
    return this.http.delete(`${ROUTE}/boards/${boardId}/epics/${epicId}`);
  }

  // Cards
  async listCards(boardId: string): Promise<Card[]> {
    return this.http.get<Card[]>(`${ROUTE}/boards/${boardId}/cards`);
  }

  async getCardByNumber(boardId: string, number: number): Promise<Card> {
    return this.http.get<Card>(`${ROUTE}/boards/${boardId}/cards/number/${number}`);
  }

  async searchCards(boardId: string, query: string): Promise<Card[]> {
    return this.http.get<Card[]>(`${ROUTE}/boards/${boardId}/cards/search?q=${encodeURIComponent(query)}`);
  }

  async createCard(boardId: string, data: {
    column_id: string;
    epic_id?: string;
    title: string;
    description?: string;
    card_type?: string;
    priority?: string;
    assignee_id?: number;
    assignee_agent?: string;
    labels?: string;
    due_date?: number | null;
    position?: number;
  }): Promise<Card> {
    return this.http.post<Card>(`${ROUTE}/boards/${boardId}/cards`, data);
  }

  async updateCard(boardId: string, cardId: string, data: {
    column_id?: string;
    epic_id?: string;
    clear_epic?: boolean;
    title?: string;
    description?: string;
    card_type?: string;
    priority?: string;
    assignee_id?: number;
    assignee_agent?: string;
    clear_assignee?: boolean;
    labels?: string;
    due_date?: number | null;
    clear_due?: boolean;
    position?: number;
  }): Promise<Card> {
    return this.http.put<Card>(`${ROUTE}/boards/${boardId}/cards/${cardId}`, data);
  }

  async deleteCard(boardId: string, cardId: string): Promise<void> {
    return this.http.delete(`${ROUTE}/boards/${boardId}/cards/${cardId}`);
  }

  // Comments
  async listComments(cardId: string): Promise<Comment[]> {
    return this.http.get<Comment[]>(`${ROUTE}/cards/${cardId}/comments`);
  }

  async createComment(cardId: string, body: string): Promise<Comment> {
    return this.http.post<Comment>(`${ROUTE}/cards/${cardId}/comments`, { body });
  }

  async deleteComment(cardId: string, commentId: string): Promise<void> {
    return this.http.delete(`${ROUTE}/cards/${cardId}/comments/${commentId}`);
  }
}
