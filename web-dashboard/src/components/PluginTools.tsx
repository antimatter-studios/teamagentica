import { useEffect, useState } from "react";
import { apiClient } from "../api/client";

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
      <div className="plugin-pricing">
        <div className="spinner" /> Loading tools...
      </div>
    );
  }

  return (
    <div className="plugin-pricing">
      <div className="pricing-header-row">
        <h3 className="pricing-section-title">
          {isMCP ? "AGGREGATED MCP TOOLS" : isAgent ? "DISCOVERED TOOLS" : "EXPOSED TOOLS"}
        </h3>
        <button className="plugin-action-btn" onClick={loadTools}>
          REFRESH
        </button>
      </div>

      <p className="pricing-hint">
        {isMCP
          ? "Tools aggregated from all tool:* and storage:* plugins via alias discovery. Shows the full MCP tool set exposed to agents."
          : isAgent
          ? "Tools discovered from tool:* plugins that this agent will send to the LLM during chat requests."
          : "Tools this plugin exposes to the MCP server for agent use."}
      </p>

      {error && <div className="form-error">{error}</div>}

      {tools.length === 0 ? (
        <div style={{
          padding: "20px 24px",
          background: "var(--bg-secondary, #1a1a2e)",
          borderRadius: 8,
          textAlign: "center",
          color: "var(--text-muted, #888)",
          fontSize: "0.85rem",
          lineHeight: 1.6,
        }}>
          <span style={{ fontSize: "1.5rem", display: "block", marginBottom: 8, opacity: 0.5 }}>
            ⚙️
          </span>
          No tools available
          <br />
          <span style={{ fontSize: "0.75rem", opacity: 0.7 }}>
            This plugin does not expose any tools, or it may not be running.
          </span>
        </div>
      ) : (
        <div className="pricing-table-wrapper">
          <table className="cost-table pricing-edit-table">
            <thead>
              <tr>
                <th>Name</th>
                {(isMCP || isAgent) && <th>Source Plugin</th>}
                {isMCP && <th>Alias</th>}
                <th>Description</th>
                <th>Endpoint</th>
                <th>Params</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((t, idx) => (
                <tr key={idx}>
                  <td>
                    <code>{isMCP ? t.full_name || t.name : t.name}</code>
                  </td>
                  {(isMCP || isAgent) && <td>{t.plugin_id || "—"}</td>}
                  {isMCP && (
                    <td>
                      {t.alias_name ? (
                        <>
                          @{t.alias_name}
                          {t.alias_model && (
                            <span className="pricing-hint" style={{ display: "block", fontSize: "0.75rem" }}>
                              {t.alias_model}
                            </span>
                          )}
                        </>
                      ) : (
                        "—"
                      )}
                    </td>
                  )}
                  <td style={{ maxWidth: 300 }}>{t.description}</td>
                  <td>
                    <code>{t.endpoint}</code>
                  </td>
                  <td>
                    {t.parameters ? (
                      <button
                        className="plugin-action-btn"
                        style={{ fontSize: "0.7rem", padding: "2px 6px" }}
                        onClick={() => toggleExpand(idx)}
                      >
                        {expanded.has(idx) ? "HIDE" : "SHOW"}
                      </button>
                    ) : (
                      "—"
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          {/* Expanded parameter details */}
          {Array.from(expanded).map((idx) => {
            const t = tools[idx];
            if (!t?.parameters) return null;
            return (
              <div key={`params-${idx}`} style={{ margin: "8px 0 16px", padding: "8px 12px", background: "var(--bg-secondary, #1a1a2e)", borderRadius: 4, fontSize: "0.8rem" }}>
                <strong>{isMCP ? t.full_name || t.name : t.name}</strong> parameters:
                <pre style={{ margin: "4px 0 0", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
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
