import { useEffect, useMemo, useCallback } from "react";
import { useAgentStore } from "../stores/agentStore";
import type { AgentType, PersonaRole } from "@teamagentica/api-client";
import PersonaForm from "./agents/PersonaForm";
import RoleForm from "./agents/RoleForm";
import AgentForm from "./agents/AgentForm";
import ToolAgentForm from "./agents/ToolAgentForm";
import ToolForm from "./agents/ToolForm";

interface SidebarSection {
  key: string;
  label: string;
  type?: AgentType;
}

const SIDEBAR_SECTIONS: SidebarSection[] = [
  { key: "roles", label: "Roles" },
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
  const {
    personas, roles, pluginsByType,
    loading, error,
    byType,
    fetch, fetchPlugins,
  } = useAgentStore();

  useEffect(() => { fetch(); fetchPlugins(); }, [fetch, fetchPlugins]);

  const route = useMemo(() => {
    if (!subpath) return null;
    const parts = subpath.split("/");
    const section = parts[0];
    const action = parts[1];
    const id = parts.slice(2).join("/");
    if (action === "create") return { section, action: "create" as const, id: "" };
    if (action === "edit" && id) return { section, action: "edit" as const, id };
    return null;
  }, [subpath]);

  const handleSave = useCallback((createdId?: string) => {
    if (createdId && route) {
      onNavigate(`${route.section}/edit/${createdId}`);
    }
  }, [route, onNavigate]);

  const handleCancel = useCallback(() => {
    onNavigate("");
  }, [onNavigate]);

  const renderContent = () => {
    if (loading) {
      return <div className="agents-main-empty">Loading agents...</div>;
    }

    if (!route) {
      return (
        <div className="agents-main-empty">
          <p>Select an item from the sidebar or create a new one.</p>
        </div>
      );
    }

    if (route.section === "roles") {
      const role = route.action === "edit"
        ? roles.find((r) => r.id === route.id)
        : undefined;
      if (route.action === "edit" && !role) {
        return <div className="agents-main-empty">Role "{route.id}" not found.</div>;
      }
      return (
        <RoleForm
          key={route.action + (route.id || "new")}
          role={role}
          onSave={handleSave}
          onCancel={handleCancel}
        />
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
          const items = sec.key === "roles"
            ? roles
            : sec.type ? byType(sec.type) : personas;
          const nameKey = sec.key === "roles" ? "id" : sec.type ? "name" : "alias";
          return (
            <div className="agents-sidebar-section" key={sec.key}>
              <div className="agents-sidebar-section-header">{sec.label}</div>
              {items.map((item) => {
                const id = (item as any)[nameKey] as string;
                const displayName = sec.key === "roles"
                  ? (item as PersonaRole).label
                  : `@${id}`;
                return (
                  <button
                    key={id}
                    className={`agents-sidebar-item${isActive(sec.key, id) ? " active" : ""}`}
                    onClick={() => onNavigate(`${sec.key}/edit/${id}`)}
                  >
                    {displayName}
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
