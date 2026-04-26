import { useEffect, useMemo, useCallback } from "react";
import { useAgentStore } from "../stores/agentStore";
import AgentEntryForm from "./agents/PersonaForm";
import AgentForm from "./agents/AgentForm";
import ToolForm from "./agents/ToolForm";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Separator } from "@/components/ui/separator";
import { cn } from "@/lib/utils";
import { Plus } from "lucide-react";

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
      return (
        <div className="flex items-center justify-center h-full text-muted-foreground">
          Loading agents...
        </div>
      );
    }

    if (!route) {
      return (
        <div className="flex items-center justify-center h-full text-muted-foreground">
          <p>Select an item from the sidebar or create a new one.</p>
        </div>
      );
    }

    if (route.section === "agents") {
      const agent = route.action === "edit"
        ? agents.find((a) => a.alias === route.id)
        : undefined;
      if (route.action === "edit" && !agent) {
        return (
          <div className="flex items-center justify-center h-full text-muted-foreground">
            Agent "{route.id}" not found.
          </div>
        );
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
        return (
          <div className="flex items-center justify-center h-full text-muted-foreground">
            Alias "{route.id}" not found.
          </div>
        );
      }
      const aliasType = item?.type;
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

    return (
      <div className="flex items-center justify-center h-full text-muted-foreground">
        Unknown section.
      </div>
    );
  };

  const isActive = (section: string, name?: string) => {
    if (!route) return false;
    if (route.section !== section) return false;
    if (!name) return route.action === "create";
    return route.action === "edit" && route.id === name;
  };

  return (
    <div className="flex h-full gap-4 p-4">
      <Card className="w-64 shrink-0 overflow-hidden">
        <ScrollArea className="h-full">
          <div className="flex flex-col gap-4 p-3">
            {SIDEBAR_SECTIONS.map((sec, idx) => {
              const items = sec.source === "agents" ? agents : aliases;
              const nameKey = sec.source === "agents" ? "alias" : "name";
              return (
                <div key={sec.key} className="flex flex-col gap-1">
                  {idx > 0 && <Separator className="mb-2" />}
                  <div className="px-2 py-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {sec.label}
                  </div>
                  {items.map((item) => {
                    const id = (item as any)[nameKey] as string;
                    const active = isActive(sec.key, id);
                    return (
                      <Button
                        key={id}
                        variant={active ? "secondary" : "ghost"}
                        size="sm"
                        className={cn("justify-start", active && "font-medium")}
                        onClick={() => onNavigate(`${sec.key}/edit/${id}`)}
                      >
                        @{id}
                      </Button>
                    );
                  })}
                  <Button
                    variant={isActive(sec.key) ? "secondary" : "ghost"}
                    size="sm"
                    className="justify-start text-muted-foreground"
                    onClick={() => onNavigate(`${sec.key}/create`)}
                  >
                    <Plus className="mr-2 h-4 w-4" />
                    Add {sec.label.replace(/s$/, "")}
                  </Button>
                </div>
              );
            })}
          </div>
        </ScrollArea>
      </Card>

      <div className="flex-1 flex flex-col min-w-0 gap-4 overflow-auto">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {renderContent()}
      </div>
    </div>
  );
}
