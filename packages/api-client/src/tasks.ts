import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/tool-task-tracker";

export interface Board {
  id: string;
  name: string;
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

export interface Card {
  id: string;
  board_id: string;
  column_id: string;
  title: string;
  description: string;
  priority: "low" | "medium" | "high" | "urgent" | "";
  assignee: string;
  labels: string;        // comma-separated
  due_date: number | null;
  position: number;
  created_at: number;
  updated_at: number;
}

export class TasksAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  // Boards
  async listBoards(): Promise<Board[]> {
    return this.http.get<Board[]>(`${ROUTE}/boards`);
  }

  async createBoard(data: { name: string; description?: string }): Promise<Board> {
    return this.http.post<Board>(`${ROUTE}/boards`, data);
  }

  async getBoard(id: string): Promise<Board> {
    return this.http.get<Board>(`${ROUTE}/boards/${id}`);
  }

  async updateBoard(id: string, data: { name?: string; description?: string }): Promise<Board> {
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

  // Cards
  async listCards(boardId: string): Promise<Card[]> {
    return this.http.get<Card[]>(`${ROUTE}/boards/${boardId}/cards`);
  }

  async createCard(boardId: string, data: {
    column_id: string;
    title: string;
    description?: string;
    priority?: string;
    assignee?: string;
    labels?: string;
    due_date?: number | null;
    position?: number;
  }): Promise<Card> {
    return this.http.post<Card>(`${ROUTE}/boards/${boardId}/cards`, data);
  }

  async updateCard(boardId: string, cardId: string, data: {
    column_id?: string;
    title?: string;
    description?: string;
    priority?: string;
    assignee?: string;
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
}
