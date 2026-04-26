import { useEffect, useState } from "react";
import { Loader2, RefreshCw } from "lucide-react";
import { apiClient } from "../api/client";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ScrollArea } from "@/components/ui/scroll-area";
import { cn } from "@/lib/utils";

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
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading system prompt...
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

  const displayPrompt =
    selectedAlias === "__default" ? data?.default_prompt : prompt;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold tracking-wide">SYSTEM PROMPT</h3>
        <Button variant="outline" size="sm" onClick={loadPrompt}>
          <RefreshCw className="h-4 w-4" />
          REFRESH
        </Button>
      </div>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {isAgent && aliases.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          <Button
            variant={selectedAlias === "__default" ? "default" : "outline"}
            size="sm"
            onClick={() => setSelectedAlias("__default")}
            title="Raw embedded system prompt before agent template rendering"
          >
            RAW TEMPLATE
          </Button>
          {aliases.map((a) => (
            <Button
              key={a.alias}
              variant={selectedAlias === a.alias ? "default" : "outline"}
              size="sm"
              onClick={() => setSelectedAlias(a.alias)}
              title={`Agent: ${a.agent_alias}${a.model ? ` | Model: ${a.model}` : ""}`}
            >
              @{a.agent_alias}
              {a.is_default && " *"}
            </Button>
          ))}
        </div>
      )}

      <p className="text-sm text-muted-foreground">
        {selectedAlias === "__default"
          ? "Raw embedded system prompt template (before agent rendering with agents/tools context)."
          : promptLabel}
      </p>

      {displayPrompt ? (
        <ScrollArea className={cn("max-h-[60vh] rounded-md border bg-muted/30")}>
          <pre className="p-5 text-xs leading-relaxed whitespace-pre-wrap break-words font-mono">
            {displayPrompt}
          </pre>
        </ScrollArea>
      ) : (
        <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
          No system prompt available.
        </div>
      )}
    </div>
  );
}
