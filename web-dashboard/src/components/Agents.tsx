import { useEffect, useMemo, useCallback } from "react";
import { useAgentStore } from "../stores/agentStore";
import AgentEntryForm from "./agents/PersonaForm";
import AgentForm from "./agents/AgentForm";
import ToolAgentForm from "./agents/ToolAgentForm";
import ToolForm from "./agents/ToolForm";

interface SidebarSection {
  key: string;
  label: string;
  source: "agents" | "aliases";
  aliasType?: string;
}

const SIDEBAR_SECTIONS: SidebarSection[] = [
  { key: "agents", label: "Agents", source: "agents" },
  { key: "aliases", label: "Aliases", source: "aliases" },
];

interface Props {
  subpath: string;
  onNavigate: (subpath: string) => void;
}

export default function Agents({ subpath, onNavigate }: Props) {
  const {
    agents, aliases, pluginsByType,
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

    if (route.section === "agents") {
      const agent = route.action === "edit"
        ? agents.find((a) => a.alias === route.id)
        : undefined;
      if (route.action === "edit" && !agent) {
        return <div className="agents-main-empty">Agent "{route.id}" not found.</div>;
      }
      return (
        <AgentEntryForm
          key={route.action + (route.id || "new")}
          agent={agent}
          onSave={handleSave}
          onCancel={handleCancel}
        />
      );
    }

    if (route.section === "aliases") {
      const item = route.action === "edit"
        ? aliases.find((a) => a.name === route.id)
        : undefined;
      if (route.action === "edit" && !item) {
        return <div className="agents-main-empty">Alias "{route.id}" not found.</div>;
      }
      // Use the appropriate form based on alias type.
      const aliasType = item?.type;
      if (aliasType === "tool_agent") {
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
      if (aliasType === "tool") {
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
          const items = sec.source === "agents" ? agents : aliases;
          const nameKey = sec.source === "agents" ? "alias" : "name";
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
