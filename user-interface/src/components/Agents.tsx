import { useState, useEffect, useCallback, useMemo } from "react";
import { apiClient } from "../api/client";
import type { RegistryAlias, AgentType, Plugin, Persona } from "@teamagentica/api-client";
import PersonaForm from "./agents/PersonaForm";
import AgentForm from "./agents/AgentForm";
import ToolAgentForm from "./agents/ToolAgentForm";
import ToolForm from "./agents/ToolForm";

const CAPABILITY_MAP: Record<string, string[]> = {
  agent: ["agent:chat"],
  tool_agent: ["agent:tool"],
  tool: ["tool:", "storage:", "infra:"],
};

const DEFAULT_COORDINATOR = "default-coordinator";

interface SidebarSection {
  key: string;
  label: string;
  type?: AgentType; // undefined = personas
}

const SIDEBAR_SECTIONS: SidebarSection[] = [
  { key: "personas", label: "Personas" },
  { key: "agents", label: "Agents", type: "agent" },
  { key: "tool-agents", label: "Tool Agents", type: "tool_agent" },
  { key: "tools", label: "Tools", type: "tool" },
];

interface Props {
  subpath: string;
  onNavigate: (subpath: string) => void;
}

export default function Agents({ subpath, onNavigate }: Props) {
  const [aliases, setAliases] = useState<RegistryAlias[]>([]);
  const [personas, setPersonas] = useState<Persona[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [pluginsByType, setPluginsByType] = useState<Record<AgentType, Plugin[]>>({
    agent: [], tool_agent: [], tool: [],
  });

  const load = useCallback(async () => {
    try {
      const [aliasData, personaData] = await Promise.all([
        apiClient.agents.list(),
        apiClient.personas.list().catch(() => [] as Persona[]),
      ]);
      setAliases(aliasData);
      setPersonas(personaData);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load agents");
    } finally {
      setLoading(false);
    }
  }, []);

  const loadPlugins = useCallback(async () => {
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
    setPluginsByType(result);
  }, []);

  useEffect(() => { load(); loadPlugins(); }, [load, loadPlugins]);

  // Parse subpath to determine what to show in main area.
  const route = useMemo(() => {
    if (!subpath) return null;
    const parts = subpath.split("/");
    // e.g. "personas/create", "agents/edit/my-agent"
    const section = parts[0];
    const action = parts[1]; // "create" or "edit"
    const id = parts.slice(2).join("/"); // everything after action
    if (action === "create") return { section, action: "create" as const, id: "" };
    if (action === "edit" && id) return { section, action: "edit" as const, id };
    return null;
  }, [subpath]);

  const handleSave = useCallback(() => {
    load();
  }, [load]);

  const handleCancel = useCallback(() => {
    onNavigate("");
  }, [onNavigate]);

  // Chat-capable aliases for persona backend_alias dropdown.
  const agentPluginIds = useMemo(() => new Set(pluginsByType.agent.map((p) => p.id)), [pluginsByType]);
  const chatAliases = useMemo(
    () => aliases.filter((a) => agentPluginIds.has(a.plugin) || a.type === "agent"),
    [aliases, agentPluginIds],
  );

  const byType = useCallback(
    (type: AgentType) => aliases.filter((a) => a.type === type),
    [aliases],
  );

  // Resolve the data item for edit routes.
  const renderContent = () => {
    if (loading) {
      return <div className="agents-main-empty">Loading agents...</div>;
    }

    if (!route) {
      const hasCoordinator = personas.some((p) => p.alias === DEFAULT_COORDINATOR);
      return (
        <div className="agents-main-empty">
          {!hasCoordinator && (
            <div className="agents-error" style={{ marginBottom: 16 }}>
              No "{DEFAULT_COORDINATOR}" persona configured — task dispatch will not work.
            </div>
          )}
          <p>Select an item from the sidebar or create a new one.</p>
        </div>
      );
    }

    if (route.section === "personas") {
      const persona = route.action === "edit"
        ? personas.find((p) => p.alias === route.id)
        : undefined;
      if (route.action === "edit" && !persona) {
        return <div className="agents-main-empty">Persona "{route.id}" not found.</div>;
      }
      return (
        <PersonaForm
          key={route.action + (route.id || "new")}
          persona={persona}
          chatAliases={chatAliases}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      );
    }

    if (route.section === "agents") {
      const item = route.action === "edit"
        ? byType("agent").find((a) => a.name === route.id)
        : undefined;
      if (route.action === "edit" && !item) {
        return <div className="agents-main-empty">Agent "{route.id}" not found.</div>;
      }
      return (
        <AgentForm
          key={route.action + (route.id || "new")}
          alias={item}
          plugins={pluginsByType.agent}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      );
    }

    if (route.section === "tool-agents") {
      const item = route.action === "edit"
        ? byType("tool_agent").find((a) => a.name === route.id)
        : undefined;
      if (route.action === "edit" && !item) {
        return <div className="agents-main-empty">Tool Agent "{route.id}" not found.</div>;
      }
      return (
        <ToolAgentForm
          key={route.action + (route.id || "new")}
          alias={item}
          plugins={pluginsByType.tool_agent}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      );
    }

    if (route.section === "tools") {
      const item = route.action === "edit"
        ? byType("tool").find((a) => a.name === route.id)
        : undefined;
      if (route.action === "edit" && !item) {
        return <div className="agents-main-empty">Tool "{route.id}" not found.</div>;
      }
      return (
        <ToolForm
          key={route.action + (route.id || "new")}
          alias={item}
          plugins={pluginsByType.tool}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      );
    }

    return <div className="agents-main-empty">Unknown section.</div>;
  };

  // Determine active sidebar item from route.
  const isActive = (section: string, name?: string) => {
    if (!route) return false;
    if (route.section !== section) return false;
    if (!name) return route.action === "create";
    return route.action === "edit" && route.id === name;
  };

  return (
    <div className="agents-layout">
      <div className="agents-sidebar">
        {SIDEBAR_SECTIONS.map((sec) => {
          const items = sec.type ? byType(sec.type) : personas;
          const nameKey = sec.type ? "name" : "alias";
          return (
            <div className="agents-sidebar-section" key={sec.key}>
              <div className="agents-sidebar-section-header">{sec.label}</div>
              {items.map((item) => {
                const id = (item as any)[nameKey] as string;
                return (
                  <button
                    key={id}
                    className={`agents-sidebar-item${isActive(sec.key, id) ? " active" : ""}`}
                    onClick={() => onNavigate(`${sec.key}/edit/${id}`)}
                  >
                    @{id}
                  </button>
                );
              })}
              <button
                className={`agents-sidebar-add${isActive(sec.key) ? " active" : ""}`}
                onClick={() => onNavigate(`${sec.key}/create`)}
              >
                + Add {sec.label.replace(/s$/, "")}
              </button>
            </div>
          );
        })}
      </div>

      <div className="agents-main">
        {error && <div className="agents-error" style={{ marginBottom: 16 }}>{error}</div>}
        {renderContent()}
      </div>
    </div>
  );
}
