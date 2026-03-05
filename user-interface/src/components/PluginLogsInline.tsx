import { useEffect, useState, useRef } from "react";
import { getPluginLogs } from "../api/plugins";

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
      const text = await getPluginLogs(pluginId, 200);
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
    <div className="logs-inline">
      <div className="logs-inline-toolbar">
        <button
          className="plugin-action-btn"
          onClick={fetchLogs}
          disabled={loading}
        >
          REFRESH
        </button>
      </div>

      <div className="log-viewer">
        {loading && !logs && (
          <div className="log-loading">
            <span className="spinner" />
            LOADING LOGS...
          </div>
        )}
        {error && <div className="log-error">{error}</div>}
        {!loading && !error && !logs && (
          <div className="log-empty">No logs available.</div>
        )}
        <pre className="log-content">{logs}</pre>
        <div ref={logsEndRef} />
      </div>
    </div>
  );
}
