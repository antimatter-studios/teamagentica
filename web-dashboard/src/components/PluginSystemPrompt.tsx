import { useEffect, useState } from "react";
import { apiClient } from "../api/client";

interface Props {
  pluginId: string;
}

interface SystemPromptResponse {
  system_prompt?: string;
  system_prompt_coordinator?: string;
  system_prompt_direct?: string;
}

export default function PluginSystemPrompt({ pluginId }: Props) {
  const [data, setData] = useState<SystemPromptResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [view, setView] = useState<"coordinator" | "direct">("coordinator");

  const isAgent = pluginId.startsWith("agent-");

  useEffect(() => {
    loadPrompt();
  }, [pluginId]);

  async function loadPrompt() {
    setLoading(true);
    setError("");
    try {
      const resp = await apiClient.plugins.getSystemPrompt(pluginId) as SystemPromptResponse;
      setData(resp);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  if (loading) {
    return (
      <div className="plugin-pricing">
        <div className="spinner" /> Loading system prompt...
      </div>
    );
  }

  const prompt = isAgent
    ? view === "coordinator"
      ? data?.system_prompt_coordinator
      : data?.system_prompt_direct
    : data?.system_prompt;

  return (
    <div className="plugin-pricing">
      <div className="pricing-header-row">
        <h3 className="pricing-section-title">SYSTEM PROMPT</h3>
        <div style={{ display: "flex", gap: 6 }}>
          {isAgent && (
            <>
              <button
                className={`plugin-action-btn${view === "coordinator" ? " btn-active" : ""}`}
                onClick={() => setView("coordinator")}
              >
                COORDINATOR
              </button>
              <button
                className={`plugin-action-btn${view === "direct" ? " btn-active" : ""}`}
                onClick={() => setView("direct")}
              >
                DIRECT
              </button>
            </>
          )}
          <button className="plugin-action-btn" onClick={loadPrompt}>
            REFRESH
          </button>
        </div>
      </div>

      <p className="pricing-hint">
        {isAgent
          ? view === "coordinator"
            ? "System prompt used when this agent acts as the coordinator (receives all unaddressed messages)."
            : "System prompt used when this agent is called directly via an @alias."
          : "System prompt this tool plugin uses when processing requests."}
      </p>

      {error && <div className="form-error">{error}</div>}

      {prompt ? (
        <pre
          style={{
            padding: "16px 20px",
            background: "var(--bg-secondary, #1a1a2e)",
            borderRadius: 8,
            fontSize: "0.82rem",
            lineHeight: 1.7,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            color: "var(--text-primary, #e0e0e0)",
            border: "1px solid var(--border-color, #2a2a4a)",
            maxHeight: "60vh",
            overflow: "auto",
          }}
        >
          {prompt}
        </pre>
      ) : (
        <div
          style={{
            padding: "20px 24px",
            background: "var(--bg-secondary, #1a1a2e)",
            borderRadius: 8,
            textAlign: "center",
            color: "var(--text-muted, #888)",
            fontSize: "0.85rem",
          }}
        >
          No system prompt available.
        </div>
      )}
    </div>
  );
}
