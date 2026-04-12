import { useEffect, useState } from "react";
import { apiClient } from "../api/client";

interface Props {
  pluginId: string;
}

interface AliasPreview {
  alias: string;
  agent_alias: string;
  model: string;
  is_default: boolean;
  rendered_prompt: string;
}

interface SystemPromptResponse {
  // New format from agent plugins.
  default_prompt?: string;
  aliases?: AliasPreview[];
  // Legacy format from tool plugins.
  system_prompt?: string;
}

export default function PluginSystemPrompt({ pluginId }: Props) {
  const [data, setData] = useState<SystemPromptResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [selectedAlias, setSelectedAlias] = useState<string | null>(null);

  const isAgent = pluginId.startsWith("agent-");

  useEffect(() => {
    loadPrompt();
  }, [pluginId]);

  async function loadPrompt() {
    setLoading(true);
    setError("");
    try {
      const resp = (await apiClient.plugins.getSystemPrompt(
        pluginId
      )) as SystemPromptResponse;
      setData(resp);
      // Auto-select default alias or first alias.
      if (resp.aliases && resp.aliases.length > 0) {
        const def = resp.aliases.find((a) => a.is_default);
        setSelectedAlias(def ? def.alias : resp.aliases[0].alias);
      } else {
        setSelectedAlias(null);
      }
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

  const aliases = data?.aliases ?? [];
  const selected = aliases.find((a) => a.alias === selectedAlias);

  // Determine what prompt to show.
  let prompt: string | undefined;
  let promptLabel = "";
  if (isAgent && selected) {
    prompt = selected.rendered_prompt;
    promptLabel = `Rendered system prompt for @${selected.agent_alias}${selected.model ? ` (${selected.model})` : ""}${selected.is_default ? " — default" : ""}`;
  } else if (isAgent && data?.default_prompt) {
    prompt = data.default_prompt;
    promptLabel = "Default system prompt (no agents assigned to this agent).";
  } else {
    prompt = data?.system_prompt || data?.default_prompt;
    promptLabel = "System prompt this tool plugin uses when processing requests.";
  }

  return (
    <div className="plugin-pricing">
      <div className="pricing-header-row">
        <h3 className="pricing-section-title">SYSTEM PROMPT</h3>
        <button className="plugin-action-btn" onClick={loadPrompt}>
          REFRESH
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      {isAgent && aliases.length > 0 && (
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginBottom: 12 }}>
          <button
            className={`plugin-action-btn${selectedAlias === "__default" ? " btn-active" : ""}`}
            onClick={() => setSelectedAlias("__default")}
            title="Raw embedded system prompt before agent template rendering"
          >
            RAW TEMPLATE
          </button>
          {aliases.map((a) => (
            <button
              key={a.alias}
              className={`plugin-action-btn${selectedAlias === a.alias ? " btn-active" : ""}`}
              onClick={() => setSelectedAlias(a.alias)}
              title={`Agent: ${a.agent_alias}${a.model ? ` | Model: ${a.model}` : ""}`}
            >
              @{a.agent_alias}
              {a.is_default && " *"}
            </button>
          ))}
        </div>
      )}

      <p className="pricing-hint">
        {selectedAlias === "__default"
          ? "Raw embedded system prompt template (before agent rendering with agents/tools context)."
          : promptLabel}
      </p>

      {(() => {
        const displayPrompt =
          selectedAlias === "__default" ? data?.default_prompt : prompt;
        return displayPrompt ? (
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
            {displayPrompt}
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
        );
      })()}
    </div>
  );
}
