import { create } from "zustand";
import { apiClient } from "../api/client";
import type { RegistryAlias, AgentType, Plugin, Persona, PersonaRole } from "@teamagentica/api-client";

const CAPABILITY_MAP: Record<string, string[]> = {
  agent: ["agent:chat"],
  tool_agent: ["agent:tool"],
  tool: ["tool:", "storage:", "infra:"],
};

interface AgentStore {
  // Data
  aliases: RegistryAlias[];
  personas: Persona[];
  roles: PersonaRole[];
  pluginsByType: Record<AgentType, Plugin[]>;
  loading: boolean;
  error: string | null;

  // Derived helpers
  byType: (type: AgentType) => RegistryAlias[];
  chatAliases: () => RegistryAlias[];

  // Actions
  fetch: () => Promise<void>;
  fetchPlugins: () => Promise<void>;

  // Persona CRUD
  createPersona: (req: Parameters<typeof apiClient.personas.create>[0]) => Promise<Persona>;
  updatePersona: (alias: string, req: Parameters<typeof apiClient.personas.update>[1]) => Promise<Persona>;
  deletePersona: (alias: string) => Promise<void>;

  // Role CRUD
  createRole: (req: Parameters<typeof apiClient.personas.createRole>[0]) => Promise<PersonaRole>;
  updateRole: (id: string, req: Parameters<typeof apiClient.personas.updateRole>[1]) => Promise<PersonaRole>;
  deleteRole: (id: string) => Promise<void>;

  // Alias CRUD (agents, tool agents, tools)
  createAlias: (req: Parameters<typeof apiClient.agents.create>[0]) => Promise<RegistryAlias>;
  updateAlias: (name: string, req: Parameters<typeof apiClient.agents.update>[1]) => Promise<RegistryAlias>;
  deleteAlias: (name: string) => Promise<void>;
}

export const useAgentStore = create<AgentStore>((set, get) => ({
  aliases: [],
  personas: [],
  roles: [],
  pluginsByType: { agent: [], tool_agent: [], tool: [] },
  loading: true,
  error: null,

  byType: (type) => get().aliases.filter((a) => a.type === type),

  chatAliases: () => {
    const { aliases, pluginsByType } = get();
    const agentPluginIds = new Set(pluginsByType.agent.map((p) => p.id));
    return aliases.filter((a) => agentPluginIds.has(a.plugin) || a.type === "agent");
  },

  fetch: async () => {
    if (get().aliases.length === 0 && get().personas.length === 0) {
      set({ loading: true });
    }
    try {
      const [aliases, personas, roles] = await Promise.all([
        apiClient.agents.list(),
        apiClient.personas.list().catch(() => [] as Persona[]),
        apiClient.personas.listRoles().catch(() => [] as PersonaRole[]),
      ]);
      set({ aliases, personas, roles, loading: false, error: null });
    } catch (err) {
      set({ loading: false, error: err instanceof Error ? err.message : "Failed to load agents" });
    }
  },

  fetchPlugins: async () => {
    const result: Record<AgentType, Plugin[]> = { agent: [], tool_agent: [], tool: [] };
    for (const [type, caps] of Object.entries(CAPABILITY_MAP)) {
      const seen = new Set<string>();
      for (const cap of caps) {
        try {
          const plugins = await apiClient.plugins.search(cap);
          for (const p of plugins) {
            if (!seen.has(p.id)) { seen.add(p.id); result[type as AgentType].push(p); }
          }
        } catch { /* ignore */ }
      }
    }
    set({ pluginsByType: result });
  },

  // Persona CRUD
  createPersona: async (req) => {
    const p = await apiClient.personas.create(req);
    await get().fetch();
    return p;
  },
  updatePersona: async (alias, req) => {
    const p = await apiClient.personas.update(alias, req);
    await get().fetch();
    return p;
  },
  deletePersona: async (alias) => {
    await apiClient.personas.delete(alias);
    await get().fetch();
  },

  // Role CRUD
  createRole: async (req) => {
    const r = await apiClient.personas.createRole(req);
    await get().fetch();
    return r;
  },
  updateRole: async (id, req) => {
    const r = await apiClient.personas.updateRole(id, req);
    await get().fetch();
    return r;
  },
  deleteRole: async (id) => {
    await apiClient.personas.deleteRole(id);
    await get().fetch();
  },

  // Alias CRUD
  createAlias: async (req) => {
    const a = await apiClient.agents.create(req);
    await get().fetch();
    return a;
  },
  updateAlias: async (name, req) => {
    const a = await apiClient.agents.update(name, req);
    await get().fetch();
    return a;
  },
  deleteAlias: async (name) => {
    await apiClient.agents.delete(name);
    await get().fetch();
  },
}));
