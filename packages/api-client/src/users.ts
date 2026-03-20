import type { HttpTransport } from "./client.js";
import type { User } from "./auth.js";

export interface UserDetails extends User {
  banned: boolean;
  ban_reason: string;
  updated_at: string;
}

export interface ServiceToken {
  id: number;
  name: string;
  capabilities: string;
  issued_by: number;
  expires_at: string;
  revoked: boolean;
  created_at: string;
}

export interface ExternalUserMapping {
  id: number;
  teamagentica_user_id: number;
  external_id: string;
  source: string;
  label: string;
  created_at: string;
  updated_at: string;
}

export interface AuditLogEntry {
  id: number;
  timestamp: string;
  actor_type: string;
  actor_id: string;
  action: string;
  resource: string;
  detail: string;
  ip: string;
  success: boolean;
}

export class UsersAPI {
  private http: HttpTransport;
  private pluginId = "system-user-manager";

  constructor(http: HttpTransport) {
    this.http = http;
  }

  private route(path: string): string {
    return `/api/route/${this.pluginId}${path}`;
  }

  // --- User management ---

  async listUsers(): Promise<UserDetails[]> {
    const res = await this.http.get<{ users: UserDetails[] }>(this.route("/users"));
    return res.users;
  }

  async getUser(id: number): Promise<UserDetails> {
    const res = await this.http.get<{ user: UserDetails }>(this.route(`/users/${id}`));
    return res.user;
  }

  async updateUser(id: number, data: { display_name?: string; role?: string }): Promise<UserDetails> {
    const res = await this.http.put<{ user: UserDetails }>(this.route(`/users/${id}`), data);
    return res.user;
  }

  async banUser(id: number, banned: boolean, reason?: string): Promise<UserDetails> {
    const res = await this.http.put<{ user: UserDetails }>(
      this.route(`/users/${id}/ban`),
      { banned, reason: reason || "" }
    );
    return res.user;
  }

  async deleteUser(id: number): Promise<void> {
    await this.http.delete(this.route(`/users/${id}`));
  }

  // --- Service tokens ---

  async listServiceTokens(): Promise<ServiceToken[]> {
    const res = await this.http.get<{ tokens: ServiceToken[] }>(this.route("/auth/service-tokens"));
    return res.tokens;
  }

  async createServiceToken(name: string, capabilities: string[], expiresInDays?: number): Promise<{ token: string; name: string; expires_at: string }> {
    return this.http.post(this.route("/auth/service-token"), {
      name,
      capabilities,
      expires_in_days: expiresInDays || 365,
    });
  }

  async revokeServiceToken(id: number): Promise<void> {
    await this.http.delete(this.route(`/auth/service-token/${id}`));
  }

  // --- External user mappings ---

  async listExternalUsers(source?: string): Promise<ExternalUserMapping[]> {
    const q = source ? `?source=${encodeURIComponent(source)}` : "";
    const res = await this.http.get<{ mappings: ExternalUserMapping[] }>(this.route(`/external-users${q}`));
    return res.mappings;
  }

  async createExternalUser(data: { external_id: string; source: string; teamagentica_user_id: number; label?: string }): Promise<ExternalUserMapping> {
    return this.http.post(this.route("/external-users"), data);
  }

  async updateExternalUser(id: number, data: { teamagentica_user_id?: number; label?: string }): Promise<ExternalUserMapping> {
    return this.http.put(this.route(`/external-users/${id}`), data);
  }

  async deleteExternalUser(id: number): Promise<void> {
    await this.http.delete(this.route(`/external-users/${id}`));
  }

  async lookupExternalUser(source: string, externalId: string): Promise<{ mapping: ExternalUserMapping; user: UserDetails | null }> {
    return this.http.get(this.route(`/external-users/lookup?source=${encodeURIComponent(source)}&external_id=${encodeURIComponent(externalId)}`));
  }

  // --- Audit logs ---

  async listAuditLogs(params?: { action?: string; actor_id?: string; limit?: number; offset?: number }): Promise<{ logs: AuditLogEntry[]; total: number; limit: number; offset: number }> {
    const q = new URLSearchParams();
    if (params?.action) q.set("action", params.action);
    if (params?.actor_id) q.set("actor_id", params.actor_id);
    if (params?.limit) q.set("limit", String(params.limit));
    if (params?.offset) q.set("offset", String(params.offset));
    const qs = q.toString();
    return this.http.get(this.route(`/audit${qs ? `?${qs}` : ""}`));
  }
}
