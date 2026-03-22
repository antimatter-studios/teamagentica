import type { HttpTransport } from "./client.js";

const ROUTE = "/api/route/infra-agent-persona";

export interface Persona {
  alias: string;
  system_prompt: string;
  model: string;
  backend_alias: string;
  created_at: string;
  updated_at: string;
}

export interface CreatePersonaRequest {
  alias: string;
  system_prompt: string;
  model?: string;
  backend_alias?: string;
}

export interface UpdatePersonaRequest {
  system_prompt?: string;
  model?: string;
  backend_alias?: string;
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
}
