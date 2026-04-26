import { create } from "zustand";
import { apiClient } from "../api/client";
import type { UserDetails, ServiceToken, AuditLogEntry } from "@teamagentica/api-client";

interface UserStore {
  users: UserDetails[];
  tokens: ServiceToken[];
  auditLogs: AuditLogEntry[];
  auditTotal: number;
  loading: boolean;
  error: string | null;

  fetch: () => Promise<void>;
  fetchUsers: () => Promise<void>;
  fetchTokens: () => Promise<void>;
  fetchAudit: () => Promise<void>;

  updateUser: (id: number, req: { display_name?: string; role?: string }) => Promise<void>;
  banUser: (id: number, ban: boolean, reason: string) => Promise<void>;
  deleteUser: (id: number) => Promise<void>;
  createUser: (email: string, password: string, displayName: string) => Promise<void>;

  createToken: (name: string, caps: string[], days: number) => Promise<string>;
  revokeToken: (id: number) => Promise<void>;
}

export const useUserStore = create<UserStore>((set, get) => ({
  users: [],
  tokens: [],
  auditLogs: [],
  auditTotal: 0,
  loading: true,
  error: null,

  fetch: async () => {
    if (get().users.length === 0) set({ loading: true });
    try {
      const errors: string[] = [];
      const [users, tokens, audit] = await Promise.all([
        apiClient.users.listUsers().catch((e: Error) => { errors.push(`users: ${e.message}`); return [] as any[]; }),
        apiClient.users.listServiceTokens().catch((e: Error) => { errors.push(`tokens: ${e.message}`); return [] as any[]; }),
        apiClient.users.listAuditLogs({ limit: 100 }).catch((e: Error) => { errors.push(`audit: ${e.message}`); return { logs: [] as any[], total: 0 }; }),
      ]);
      set({ users: users as any, tokens: tokens as any, auditLogs: audit.logs, auditTotal: audit.total, loading: false, error: errors.length ? errors.join("; ") : null });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Failed to load users" });
    }
  },

  fetchUsers: async () => {
    try {
      const users = await apiClient.users.listUsers();
      set({ users, error: null });
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to load users" });
    }
  },

  fetchTokens: async () => {
    try {
      const tokens = await apiClient.users.listServiceTokens();
      set({ tokens, error: null });
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to load tokens" });
    }
  },

  fetchAudit: async () => {
    try {
      const res = await apiClient.users.listAuditLogs({ limit: 100 });
      set({ auditLogs: res.logs, auditTotal: res.total, error: null });
    } catch (err) {
      set({ error: err instanceof Error ? err.message : "Failed to load audit logs" });
    }
  },

  updateUser: async (id, req) => {
    await apiClient.users.updateUser(id, req);
    await get().fetchUsers();
  },

  banUser: async (id, ban, reason) => {
    await apiClient.users.banUser(id, ban, reason);
    await get().fetchUsers();
  },

  deleteUser: async (id) => {
    await apiClient.users.deleteUser(id);
    await get().fetchUsers();
  },

  createUser: async (email, password, displayName) => {
    await apiClient.auth.register(email, password, displayName);
    await get().fetchUsers();
  },

  createToken: async (name, caps, days) => {
    const res = await apiClient.users.createServiceToken(name, caps, days);
    await get().fetchTokens();
    return res.token;
  },

  revokeToken: async (id) => {
    await apiClient.users.revokeServiceToken(id);
    await get().fetchTokens();
  },
}));
