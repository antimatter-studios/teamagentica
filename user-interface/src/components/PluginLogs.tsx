import { useEffect, useState, useRef } from "react";
import { getPluginLogs } from "../api/plugins";

interface Props {
  pluginId: string;
  pluginName: string;
  onClose: () => void;
}

export default function PluginLogs({ pluginId, pluginName, onClose }: Props) {
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pluginId]);

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [logs]);

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-card modal-large" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2 className="modal-title">
            <span className="section-icon">&gt;_</span>
            LOGS: {pluginName}
          </h2>
          <div className="modal-header-actions">
            <button
              className="plugin-action-btn"
              onClick={fetchLogs}
              disabled={loading}
            >
              REFRESH
            </button>
            <button className="plugin-action-btn btn-danger" onClick={onClose}>
              CLOSE
            </button>
          </div>
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
    </div>
  );
}
