import { useEffect, useState, useRef } from "react";
import { Loader2, RefreshCw } from "lucide-react";
import { apiClient } from "../api/client";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Alert, AlertDescription } from "@/components/ui/alert";

interface Props {
  pluginId: string;
}

export default function PluginLogsInline({ pluginId }: Props) {
  const [logs, setLogs] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const logsEndRef = useRef<HTMLDivElement>(null);

  async function fetchLogs() {
    setLoading(true);
    setError("");
    try {
      const text = await apiClient.plugins.getLogs(pluginId, 200);
      setLogs(text);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load logs");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    fetchLogs();
  }, [pluginId]);

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-end">
        <Button
          variant="outline"
          size="sm"
          onClick={fetchLogs}
          disabled={loading}
        >
          <RefreshCw className="h-4 w-4" />
          REFRESH
        </Button>
      </div>

      <ScrollArea className="h-[60vh] rounded-md border bg-muted/30">
        <div className="p-4">
          {loading && !logs && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              LOADING LOGS...
            </div>
          )}
          {error && (
            <Alert variant="destructive">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          {!loading && !error && !logs && (
            <div className="text-sm text-muted-foreground">No logs available.</div>
          )}
          <pre className="font-mono text-xs whitespace-pre-wrap break-all">{logs}</pre>
          <div ref={logsEndRef} />
        </div>
      </ScrollArea>
    </div>
  );
}
