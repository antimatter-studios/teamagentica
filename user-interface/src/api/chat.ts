import { apiGet, apiPost, apiPut, apiDelete, API_BASE } from "./client";

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

export async function fetchAgents(): Promise<{ agents: Agent[]; has_coordinator: boolean }> {
  const resp = await apiGet<{ agents: Agent[]; has_coordinator: boolean }>("/api/route/messaging-chat/agents");
  return { agents: resp.agents || [], has_coordinator: resp.has_coordinator || false };
}

export async function fetchConversations(): Promise<Conversation[]> {
  const resp = await apiGet<{ conversations: Conversation[] }>(
    "/api/route/messaging-chat/conversations"
  );
  return resp.conversations || [];
}

export async function createConversation(
  agentAlias: string,
  title?: string
): Promise<Conversation> {
  const body: Record<string, unknown> = {};
  if (agentAlias && agentAlias !== "auto") body.agent_alias = agentAlias;
  if (title) body.title = title;
  return apiPost<Conversation>("/api/route/messaging-chat/conversations", body);
}

export async function getConversation(
  id: number
): Promise<{ conversation: Conversation; messages: ChatMessage[] }> {
  return apiGet(`/api/route/messaging-chat/conversations/${id}`);
}

export async function updateConversation(
  id: number,
  title: string
): Promise<Conversation> {
  return apiPut<Conversation>(`/api/route/messaging-chat/conversations/${id}`, { title });
}

export async function deleteConversation(id: number): Promise<void> {
  return apiDelete(`/api/route/messaging-chat/conversations/${id}`);
}

export async function sendMessage(
  conversationId: number,
  content: string,
  agentAlias: string,
  attachmentIds?: string[]
): Promise<{ user_message: ChatMessage; assistant_message: ChatMessage }> {
  const body: Record<string, unknown> = { content };
  if (agentAlias && agentAlias !== "auto") body.agent_alias = agentAlias;
  if (attachmentIds && attachmentIds.length > 0) body.attachment_ids = attachmentIds;
  return apiPost(`/api/route/messaging-chat/conversations/${conversationId}/messages`, body);
}

export async function uploadFile(
  file: File
): Promise<{ file_id: string; filename: string }> {
  const formData = new FormData();
  formData.append("file", file);

  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/route/messaging-chat/upload`, {
    method: "POST",
    headers,
    body: formData,
  });

  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.json();
}

export function filePath(fileIdOrKey: string): string {
  return `${API_BASE}/api/route/messaging-chat/files/${fileIdOrKey}`;
}

export async function fetchFileBlob(fileIdOrKey: string): Promise<string> {
  const token = localStorage.getItem("teamagentica_token");
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(filePath(fileIdOrKey), { headers });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const blob = await res.blob();
  return URL.createObjectURL(blob);
}

export async function downloadFile(fileIdOrKey: string, filename: string): Promise<void> {
  const blobUrl = await fetchFileBlob(fileIdOrKey);
  const a = document.createElement("a");
  a.href = blobUrl;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(blobUrl);
}
