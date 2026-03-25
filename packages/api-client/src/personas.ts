import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/infra-agent-persona";

export interface Persona {
  alias: string;
  system_prompt: string;
  model: string;
  backend_alias: string;
  role: string;
  created_at: string;
  updated_at: string;
}

export interface PersonaRole {
  id: string;
  label: string;
  max_count: number;
  system_prompt: string;
}

export interface CreatePersonaRequest {
  alias: string;
  system_prompt: string;
  model?: string;
  backend_alias?: string;
  role?: string;
}

export interface UpdatePersonaRequest {
  system_prompt?: string;
  model?: string;
  backend_alias?: string;
  role?: string;
}

export class PersonaAPI {
  private http: HttpTransport;
  constructor(http: HttpTransport) { this.http = http; }

  async list(): Promise<Persona[]> {
    const res = await this.http.get<{ personas: Persona[] }>(`${ROUTE}/personas`);
    return res.personas || [];
  }

  async get(alias: string): Promise<Persona> {
    return this.http.get<Persona>(`${ROUTE}/personas/${encodeURIComponent(alias)}`);
  }

  async create(req: CreatePersonaRequest): Promise<Persona> {
    return this.http.post<Persona>(`${ROUTE}/personas`, req);
  }

  async update(alias: string, req: UpdatePersonaRequest): Promise<Persona> {
    return this.http.put<Persona>(`${ROUTE}/personas/${encodeURIComponent(alias)}`, req);
  }

  async delete(alias: string): Promise<void> {
    await this.http.delete(`${ROUTE}/personas/${encodeURIComponent(alias)}`);
  }

  async listRoles(): Promise<PersonaRole[]> {
    const res = await this.http.get<{ roles: PersonaRole[] }>(`${ROUTE}/roles`);
    return res.roles || [];
  }

  async getRole(id: string): Promise<PersonaRole> {
    return this.http.get<PersonaRole>(`${ROUTE}/roles/${encodeURIComponent(id)}`);
  }

  async createRole(req: { id: string; label: string; max_count: number; system_prompt?: string }): Promise<PersonaRole> {
    return this.http.post<PersonaRole>(`${ROUTE}/roles`, req);
  }

  async updateRole(id: string, req: { label?: string; max_count?: number; system_prompt?: string }): Promise<PersonaRole> {
    return this.http.put<PersonaRole>(`${ROUTE}/roles/${encodeURIComponent(id)}`, req);
  }

  async deleteRole(id: string): Promise<void> {
    await this.http.delete(`${ROUTE}/roles/${encodeURIComponent(id)}`);
  }

  async resetRolePrompts(): Promise<{ message: string }> {
    return this.http.post<{ message: string }>(`${ROUTE}/roles/reset-prompts`, {});
  }

  async getPersonasByRole(role: string): Promise<Persona[]> {
    const res = await this.http.get<{ personas: Persona[] }>(`${ROUTE}/personas/by-role/${encodeURIComponent(role)}`);
    return res.personas || [];
  }
}
