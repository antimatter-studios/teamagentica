import type { HttpTransport } from "./client.js";

const CAPABILITY_SEMANTIC = "^tool:memory:bank:semantic$";
const CAPABILITY_EPISODIC = "^tool:memory:bank:episodic$";

export interface Memory {
  id: string;
  memory: string;
  user_id: string;
  agent_id: string;
  app_id: string;
  run_id: string;
  metadata: Record<string, unknown> | null;
  categories: string[] | null;
  immutable: boolean;
  created_at: string;
  updated_at: string;
  score: number;
}

export interface MemoryEntity {
  type: string;
  id: string;
}

export interface LCMConversation {
  id: number;
  session_id: string;
  title: string | null;
  message_count: number;
  last_message_at: string;
  created_at: string;
}

export interface LCMMessage {
  id: number;
  seq: number;
  role: string;
  content: string;
  token_count: number;
  created_at: string;
}

export class MemoryAPI {
  private http: HttpTransport;
  private routeCache: Record<string, string> = {};

  constructor(http: HttpTransport) { this.http = http; }

  /** Resolve a memory plugin by capability, cache the route. */
  private async resolveRoute(capability: string): Promise<string> {
    if (this.routeCache[capability]) return this.routeCache[capability];

    const res = await this.http.get<{ plugins: { id: string }[] }>(
      `/api/plugins/search?capability=${capability}`
    );
    const plugins = res?.plugins;
    if (!plugins || plugins.length === 0) {
      throw new Error("no memory plugin found with capability: " + capability);
    }
    this.routeCache[capability] = `/api/route/${plugins[0].id}`;
    return this.routeCache[capability];
  }

  private semanticRoute(): Promise<string> { return this.resolveRoute(CAPABILITY_SEMANTIC); }
  private episodicRoute(): Promise<string> { return this.resolveRoute(CAPABILITY_EPISODIC); }

  // ── Semantic (Mem0) endpoints ──

  async list(opts?: {
    user_id?: string;
    agent_id?: string;
    run_id?: string;
    page?: number;
    page_size?: number;
  }): Promise<Memory[]> {
    const route = await this.semanticRoute();
    const res = await this.http.post<{ results: Memory[] }>(`${route}/mcp/get_memories`, opts ?? {});
    return res.results || [];
  }

  async search(query: string, opts?: {
    user_id?: string;
    agent_id?: string;
    run_id?: string;
    top_k?: number;
    threshold?: number;
    rerank?: boolean;
    keyword_search?: boolean;
  }): Promise<Memory[]> {
    const route = await this.semanticRoute();
    const res = await this.http.post<{ results: Memory[] }>(`${route}/mcp/search_memories`, { query, ...opts });
    return res.results || [];
  }

  async get(memoryId: string): Promise<Memory> {
    const route = await this.semanticRoute();
    return this.http.post<Memory>(`${route}/mcp/get_memory`, { memory_id: memoryId });
  }

  async delete(memoryId: string): Promise<void> {
    const route = await this.semanticRoute();
    await this.http.post(`${route}/mcp/delete_memory`, { memory_id: memoryId });
  }

  async deleteAll(scope: {
    user_id?: string;
    agent_id?: string;
    app_id?: string;
    run_id?: string;
  }): Promise<void> {
    const route = await this.semanticRoute();
    await this.http.post(`${route}/mcp/delete_all_memories`, scope);
  }

  async listEntities(): Promise<MemoryEntity[]> {
    const route = await this.semanticRoute();
    const res = await this.http.post<{ results: MemoryEntity[] }>(`${route}/mcp/list_entities`, {});
    return res.results || [];
  }

  async update(memoryId: string, text: string): Promise<void> {
    const route = await this.semanticRoute();
    await this.http.post(`${route}/mcp/update_memory`, { memory_id: memoryId, text });
  }

  // ── Episodic (LCM) endpoints ──

  async listConversations(): Promise<LCMConversation[]> {
    const route = await this.episodicRoute();
    const res = await this.http.get<{ conversations: LCMConversation[] }>(`${route}/conversations`);
    return res.conversations || [];
  }

  async getConversationMessages(conversationId: number, opts?: {
    limit?: number;
    offset?: number;
  }): Promise<{ messages: LCMMessage[]; total: number }> {
    const route = await this.episodicRoute();
    const params = new URLSearchParams();
    if (opts?.limit) params.set("limit", String(opts.limit));
    if (opts?.offset) params.set("offset", String(opts.offset));
    const qs = params.toString();
    return this.http.get<{ messages: LCMMessage[]; total: number }>(
      `${route}/conversations/${conversationId}/messages${qs ? "?" + qs : ""}`
    );
  }
}
