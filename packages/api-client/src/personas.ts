import type { HttpTransport } from "./client.js";
import { sanitizeAlias } from "./sanitize.js";

const ROUTE = "/api/route/infra-agent-persona";

export interface Persona {
  alias: string;
  system_prompt: string;
  model: string;
  backend_alias: string;
  role: string;
  is_default?: boolean | null;
  created_at: string;
  updated_at: string;
}

export interface PersonaRole {
  id: string;
  label: string;
  system_prompt: string;
}

export interface CreatePersonaRequest {
  alias: string;
  system_prompt: string;
  model?: string;
  backend_alias?: string;
  role?: string;
  is_default?: boolean;
}

export interface UpdatePersonaRequest {
  alias?: string;
  system_prompt?: string;
  model?: string;
  backend_alias?: string;
  role?: string;
  is_default?: boolean;
}

export interface CreateRoleRequest {
  id: string;
  label: string;
  system_prompt?: string;
}

export interface UpdateRoleRequest {
  label?: string;
  system_prompt?: string;
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
    req.alias = sanitizeAlias(req.alias);
    return this.http.post<Persona>(`${ROUTE}/personas`, req);
  }

  async update(alias: string, req: UpdatePersonaRequest): Promise<Persona> {
    if (req.alias) req.alias = sanitizeAlias(req.alias);
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

  async createRole(req: CreateRoleRequest): Promise<PersonaRole> {
    return this.http.post<PersonaRole>(`${ROUTE}/roles`, req);
  }

  async updateRole(id: string, req: UpdateRoleRequest): Promise<PersonaRole> {
    return this.http.put<PersonaRole>(`${ROUTE}/roles/${encodeURIComponent(id)}`, req);
  }

  async deleteRole(id: string): Promise<void> {
    await this.http.delete(`${ROUTE}/roles/${encodeURIComponent(id)}`);
  }

  async getDefault(): Promise<Persona> {
    return this.http.get<Persona>(`${ROUTE}/personas/default`);
  }

  async setDefault(alias: string): Promise<Persona> {
    return this.http.post<Persona>(`${ROUTE}/personas/${encodeURIComponent(alias)}/set-default`, {});
  }

  async getPersonasByRole(role: string): Promise<Persona[]> {
    const res = await this.http.get<{ personas: Persona[] }>(`${ROUTE}/personas/by-role/${encodeURIComponent(role)}`);
    return res.personas || [];
  }
}
