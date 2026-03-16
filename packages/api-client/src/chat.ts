import type { HttpTransport } from "./client.js";

export interface Agent {
  alias: string;
  plugin_id: string;
  model: string;
}

export interface Conversation {
  id: number;
  user_id: number;
  title: string;
  default_agent: string;
  created_at: string;
  updated_at: string;
}

export interface ChatMessage {
  id: number;
  conversation_id: number;
  role: "user" | "assistant";
  content: string;
  agent_alias?: string;
  agent_plugin?: string;
  model?: string;
  provider?: string;
  input_tokens?: number;
  output_tokens?: number;
  duration_ms?: number;
  attachments?: string;
  created_at: string;
}

export interface Attachment {
  type: string;
  filename: string;
  file_id?: string;
  storage_key?: string;
  mime_type: string;
  url?: string;
}

const ROUTE = "/api/route/messaging-chat";

export class ChatAPI {
  constructor(private http: HttpTransport) {}

  async fetchAgents(): Promise<{ agents: Agent[]; has_coordinator: boolean }> {
    const resp = await this.http.get<{
      agents: Agent[];
      has_coordinator: boolean;
    }>(`${ROUTE}/agents`);
    return {
      agents: resp.agents || [],
      has_coordinator: resp.has_coordinator || false,
    };
  }

  async fetchConversations(): Promise<Conversation[]> {
    const resp = await this.http.get<{ conversations: Conversation[] }>(
      `${ROUTE}/conversations`
    );
    return resp.conversations || [];
  }

  async createConversation(
    agentAlias: string,
    title?: string
  ): Promise<Conversation> {
    const body: Record<string, unknown> = {};
    if (agentAlias && agentAlias !== "auto") body.agent_alias = agentAlias;
    if (title) body.title = title;
    return this.http.post<Conversation>(`${ROUTE}/conversations`, body);
  }

  async getConversation(
    id: number
  ): Promise<{ conversation: Conversation; messages: ChatMessage[] }> {
    return this.http.get(`${ROUTE}/conversations/${id}`);
  }

  async updateConversation(id: number, title: string): Promise<Conversation> {
    return this.http.put<Conversation>(`${ROUTE}/conversations/${id}`, {
      title,
    });
  }

  async deleteConversation(id: number): Promise<void> {
    return this.http.delete(`${ROUTE}/conversations/${id}`);
  }

  async sendMessage(
    conversationId: number,
    content: string,
    agentAlias: string,
    attachmentIds?: string[]
  ): Promise<{ user_message: ChatMessage; assistant_message: ChatMessage }> {
    const body: Record<string, unknown> = { content };
    if (agentAlias && agentAlias !== "auto") body.agent_alias = agentAlias;
    if (attachmentIds && attachmentIds.length > 0)
      body.attachment_ids = attachmentIds;
    return this.http.post(
      `${ROUTE}/conversations/${conversationId}/messages`,
      body
    );
  }

  async uploadFile(
    formData: FormData
  ): Promise<{ file_id: string; filename: string }> {
    return this.http.postFormData(`${ROUTE}/upload`, formData);
  }

  filePath(fileIdOrKey: string): string {
    return `${this.http.baseUrl}${ROUTE}/files/${fileIdOrKey}`;
  }

  async fetchFileBlob(fileIdOrKey: string): Promise<Blob> {
    const res = await this.http.getRaw(`${ROUTE}/files/${fileIdOrKey}`);
    return res.blob();
  }
}
