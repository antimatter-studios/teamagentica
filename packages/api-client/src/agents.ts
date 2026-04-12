import type { HttpTransport } from "./client.js";
import { sanitizeAlias } from "./sanitize.js";

const ROUTE = "/api/route/infra-agent-registry";

export type AgentType = "agent" | "tool_agent" | "tool";

export interface RegistryAlias {
  name: string;
  type: AgentType;
  plugin: string;
  model: string;
  system_prompt: string;
  created_at: string;
  updated_at: string;
}

export interface CreateAliasRequest {
  name: string;
  type: AgentType;
  plugin: string;
  model?: string;
  system_prompt?: string;
}

export interface UpdateAliasRequest {
  name?: string;
  type?: AgentType;
  plugin?: string;
  model?: string;
  system_prompt?: string;
}

export interface PluginModels {
  models: string[];
  current: string;
}

export class AgentRegistryAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(type?: AgentType, excludeAgents?: boolean): Promise<RegistryAlias[]> {
    const params: string[] = [];
    if (type) params.push(`type=${encodeURIComponent(type)}`);
    if (excludeAgents) params.push("exclude_agents=true");
    const qs = params.length ? `?${params.join("&")}` : "";
    const res = await this.http.get<{ aliases: RegistryAlias[] }>(`${ROUTE}/aliases${qs}`);
    return res.aliases || [];
  }

  async create(req: CreateAliasRequest): Promise<RegistryAlias> {
    req.name = sanitizeAlias(req.name);
    return this.http.post<RegistryAlias>(`${ROUTE}/aliases`, req);
  }

  async update(name: string, req: UpdateAliasRequest): Promise<RegistryAlias> {
    if (req.name) req.name = sanitizeAlias(req.name);
    return this.http.put<RegistryAlias>(`${ROUTE}/aliases/${encodeURIComponent(name)}`, req);
  }

  async delete(name: string): Promise<void> {
    await this.http.delete(`${ROUTE}/aliases/${encodeURIComponent(name)}`);
  }

  /** Fetch available models from an agent plugin via its /models endpoint. */
  async pluginModels(pluginId: string): Promise<PluginModels> {
    return this.http.get<PluginModels>(`/api/route/${encodeURIComponent(pluginId)}/models`);
  }
}
