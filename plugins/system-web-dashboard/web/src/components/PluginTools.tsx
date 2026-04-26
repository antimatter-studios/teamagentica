import { useEffect, useState } from "react";
import { Loader2, RefreshCw, Settings } from "lucide-react";
import { apiClient } from "../api/client";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface ToolEntry {
  name: string;
  full_name?: string;
  description: string;
  endpoint: string;
  parameters?: unknown;
  plugin_id?: string;
  alias_name?: string;
  alias_model?: string;
}

interface Props {
  pluginId: string;
}

export default function PluginTools({ pluginId }: Props) {
  const [tools, setTools] = useState<ToolEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const isMCP = pluginId.startsWith("infra-mcp");
  const isAgent = pluginId.startsWith("agent-");

  useEffect(() => {
    loadTools();
  }, [pluginId]);

  async function loadTools() {
    setLoading(true);
    setError("");
    try {
      const data = await apiClient.plugins.getTools(pluginId) as { tools: ToolEntry[] };
      setTools(data.tools || []);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      // 404 or "not found" means the plugin simply doesn't expose tools
      if (msg.includes("404") || msg.toLowerCase().includes("not found")) {
        setTools([]);
      } else {
        setError(msg);
      }
    } finally {
      setLoading(false);
    }
  }

  function toggleExpand(idx: number) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(idx)) next.delete(idx);
      else next.add(idx);
      return next;
    });
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading tools...
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold tracking-wide">
          {isMCP ? "AGGREGATED MCP TOOLS" : isAgent ? "DISCOVERED TOOLS" : "EXPOSED TOOLS"}
        </h3>
        <Button variant="outline" size="sm" onClick={loadTools}>
          <RefreshCw className="h-4 w-4" />
          REFRESH
        </Button>
      </div>

      <p className="text-sm text-muted-foreground">
        {isMCP
          ? "Tools aggregated from all tool:* and storage:* plugins via alias discovery. Shows the full MCP tool set exposed to agents."
          : isAgent
          ? "Tools discovered from tool:* plugins that this agent will send to the LLM during chat requests."
          : "Tools this plugin exposes to the MCP server for agent use."}
      </p>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {tools.length === 0 ? (
        <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
          <Settings className="mx-auto mb-2 h-6 w-6 opacity-50" />
          No tools available
          <br />
          <span className="text-xs opacity-70">
            This plugin does not expose any tools, or it may not be running.
          </span>
        </div>
      ) : (
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                {(isMCP || isAgent) && <TableHead>Source Plugin</TableHead>}
                {isMCP && <TableHead>Alias</TableHead>}
                <TableHead>Description</TableHead>
                <TableHead>Endpoint</TableHead>
                <TableHead>Params</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {tools.map((t, idx) => (
                <TableRow key={idx}>
                  <TableCell>
                    <code className="font-mono text-xs">{isMCP ? t.full_name || t.name : t.name}</code>
                  </TableCell>
                  {(isMCP || isAgent) && <TableCell>{t.plugin_id || "—"}</TableCell>}
                  {isMCP && (
                    <TableCell>
                      {t.alias_name ? (
                        <>
                          @{t.alias_name}
                          {t.alias_model && (
                            <span className="block text-xs text-muted-foreground">
                              {t.alias_model}
                            </span>
                          )}
                        </>
                      ) : (
                        "—"
                      )}
                    </TableCell>
                  )}
                  <TableCell className="max-w-[300px]">{t.description}</TableCell>
                  <TableCell>
                    <code className="font-mono text-xs">{t.endpoint}</code>
                  </TableCell>
                  <TableCell>
                    {t.parameters ? (
                      <Button
                        variant="outline"
                        size="sm"
                        className="h-7 text-xs"
                        onClick={() => toggleExpand(idx)}
                      >
                        {expanded.has(idx) ? "HIDE" : "SHOW"}
                      </Button>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          {/* Expanded parameter details */}
          {Array.from(expanded).map((idx) => {
            const t = tools[idx];
            if (!t?.parameters) return null;
            return (
              <div
                key={`params-${idx}`}
                className="my-2 mx-2 rounded-md border bg-muted/30 p-3 text-xs"
              >
                <strong>{isMCP ? t.full_name || t.name : t.name}</strong> parameters:
                <pre className="mt-1 whitespace-pre-wrap break-all font-mono">
                  {JSON.stringify(t.parameters, null, 2)}
                </pre>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
