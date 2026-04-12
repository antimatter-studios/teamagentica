import type { HttpTransport } from "./client.js";
import { sanitizeAlias } from "./sanitize.js";

const ROUTE = "/api/route/infra-agent-registry";

export interface AgentEntry {
  alias: string;
  type: string;
  plugin: string;
  model: string;
  system_prompt: string;
  is_default?: boolean | null;
  created_at: string;
  updated_at: string;
}

export interface CreateAgentEntryRequest {
  alias: string;
  type?: string;
  plugin?: string;
  model?: string;
  system_prompt?: string;
  is_default?: boolean;
}

export interface UpdateAgentEntryRequest {
  alias?: string;
  type?: string;
  plugin?: string;
  model?: string;
  system_prompt?: string;
  is_default?: boolean;
}

export class AgentAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(): Promise<AgentEntry[]> {
    const res = await this.http.get<{ agents: AgentEntry[] }>(`${ROUTE}/agents`);
    return res.agents || [];
  }

  async get(alias: string): Promise<AgentEntry> {
    return this.http.get<AgentEntry>(`${ROUTE}/agents/${encodeURIComponent(alias)}`);
  }

  async create(req: CreateAgentEntryRequest): Promise<AgentEntry> {
    req.alias = sanitizeAlias(req.alias);
    return this.http.post<AgentEntry>(`${ROUTE}/agents`, req);
  }

  async update(alias: string, req: UpdateAgentEntryRequest): Promise<AgentEntry> {
    if (req.alias) req.alias = sanitizeAlias(req.alias);
    return this.http.put<AgentEntry>(`${ROUTE}/agents/${encodeURIComponent(alias)}`, req);
  }

  async delete(alias: string): Promise<void> {
    await this.http.delete(`${ROUTE}/agents/${encodeURIComponent(alias)}`);
  }

  async getDefault(): Promise<AgentEntry> {
    return this.http.get<AgentEntry>(`${ROUTE}/agents/default`);
  }

  async setDefault(alias: string): Promise<AgentEntry> {
    return this.http.post<AgentEntry>(`${ROUTE}/agents/${encodeURIComponent(alias)}/set-default`, {});
  }
}
