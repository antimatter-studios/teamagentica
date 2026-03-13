import { useEffect, useState } from "react";
import { apiGet } from "../api/client";

interface DiscordCommandEntry {
  key: string;
  plugin_id: string;
  endpoint: string;
}

interface Props {
  pluginId: string;
}

export default function PluginDiscordCommands({ pluginId }: Props) {
  const [commands, setCommands] = useState<DiscordCommandEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    load();
  }, [pluginId]);

  async function load() {
    setLoading(true);
    setError("");
    try {
      const data = await apiGet<{ commands: DiscordCommandEntry[] }>(
        `/api/route/${pluginId}/discord-commands`
      );
      const sorted = (data.commands || []).slice().sort((a, b) => a.key.localeCompare(b.key));
      setCommands(sorted);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }

  if (loading) {
    return (
      <div className="plugin-pricing">
        <div className="spinner" /> Loading commands...
      </div>
    );
  }

  return (
    <div className="plugin-pricing">
      <div className="pricing-header-row">
        <h3 className="pricing-section-title">REGISTERED SLASH COMMANDS</h3>
        <button className="plugin-action-btn" onClick={load}>REFRESH</button>
      </div>

      <p className="pricing-hint">
        Slash commands currently registered with Discord. Empty means discovery hasn't run yet or no plugins expose <code>discord:command</code>.
      </p>

      {error && <div className="form-error">{error}</div>}

      {commands.length === 0 ? (
        <div style={{
          padding: "20px 24px",
          background: "var(--bg-secondary, #1a1a2e)",
          borderRadius: 8,
          textAlign: "center",
          color: "var(--text-muted, #888)",
          fontSize: "0.85rem",
          lineHeight: 1.6,
        }}>
          No slash commands registered yet.
        </div>
      ) : (
        <div className="pricing-table-wrapper">
          <table className="cost-table pricing-edit-table">
            <thead>
              <tr>
                <th>Command</th>
                <th>Owner Plugin</th>
                <th>Endpoint</th>
              </tr>
            </thead>
            <tbody>
              {commands.map((cmd) => (
                <tr key={cmd.key}>
                  <td><code>/{cmd.key.replace("/", " ")}</code></td>
                  <td>{cmd.plugin_id}</td>
                  <td><code>{cmd.endpoint}</code></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
