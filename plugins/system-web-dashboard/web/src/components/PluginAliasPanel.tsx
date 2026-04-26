import { useEffect, useState } from "react";
import { Loader2, Plus, X } from "lucide-react";
import { apiClient } from "../api/client";
import type { Plugin } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface AliasEntry {
  name: string;
  target: string;
}

function parseAliasEntries(v: string): AliasEntry[] {
  if (!v) return [];
  try {
    const parsed = JSON.parse(v);
    if (Array.isArray(parsed)) return parsed;
  } catch { /* empty */ }
  return [];
}

function targetToModel(target: string, pluginId: string): string {
  if (target.startsWith(pluginId + ":")) return target.slice(pluginId.length + 1);
  if (target === pluginId) return "";
  return target;
}

function modelToTarget(model: string, pluginId: string): string {
  if (!model) return pluginId;
  if (model.includes(":")) return model;
  return `${pluginId}:${model}`;
}

async function findModelFieldKey(plugin: Plugin): Promise<string | null> {
  const schema = await apiClient.plugins.getConfigSchema(plugin.id);
  for (const [key, field] of Object.entries(schema)) {
    if (field.type === "select" && field.dynamic) return key;
  }
  return null;
}

interface Props {
  plugin: Plugin;
  onSaved?: () => void;
}

export default function PluginAliasPanel({ plugin, onSaved }: Props) {
  const [entries, setEntries] = useState<AliasEntry[]>([]);
  const [modelFieldKey, setModelFieldKey] = useState<string | null>(null);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [dirty, setDirty] = useState(false);

  // Load current aliases from plugin config.
  useEffect(() => {
    async function load() {
      try {
        const configs = await apiClient.plugins.getConfig(plugin.id);
        const aliasEntry = configs.find((c) => c.key === "PLUGIN_ALIASES");
        setEntries(parseAliasEntries(aliasEntry?.value || ""));
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load aliases");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [plugin.id]);

  // Resolve model field key from live schema.
  useEffect(() => {
    findModelFieldKey(plugin).then(setModelFieldKey).catch(() => setModelFieldKey(null));
  }, [plugin.id, plugin.status]);

  const fetchModels = () => {
    if (!modelFieldKey || plugin.status !== "running") return;

    setModelsLoading(true);
    setModelsError("");
    apiClient.plugins.getFieldOptions(plugin.id, modelFieldKey)
      .then((res) => setModelOptions((res.options || []).map((o) => typeof o === "string" ? o : o.value)))
      .catch((err) => {
        setModelsError(err instanceof Error ? err.message : "Failed to fetch models");
      })
      .finally(() => setModelsLoading(false));
  };

  // Fetch available models from the plugin's dynamic model field.
  useEffect(() => {
    fetchModels();
  }, [plugin.id, plugin.status, modelFieldKey]);

  const updateEntry = (i: number, field: "name" | "model", val: string) => {
    setEntries((prev) =>
      prev.map((e, idx) => {
        if (idx !== i) return e;
        if (field === "name") return { ...e, name: val };
        return { ...e, target: modelToTarget(val, plugin.id) };
      })
    );
    setDirty(true);
  };

  const removeEntry = (i: number) => {
    setEntries((prev) => prev.filter((_, idx) => idx !== i));
    setDirty(true);
  };

  const addEntry = () => {
    setEntries((prev) => [...prev, { name: "", target: plugin.id }]);
    setDirty(true);
  };

  const handleSave = async () => {
    setSaving(true);
    setError("");
    setSaveSuccess(false);
    try {
      const cleaned = entries.filter((e) => e.name.trim() !== "");
      await apiClient.plugins.updateConfig(plugin.id, {
        PLUGIN_ALIASES: {
          value: JSON.stringify(cleaned),
          is_secret: false,
        },
      });
      setEntries(cleaned);
      setDirty(false);
      setSaveSuccess(true);
      onSaved?.();
      setTimeout(() => setSaveSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save aliases");
    } finally {
      setSaving(false);
    }
  };

  const supportsModels = modelFieldKey !== null;
  const hasModels = modelOptions.length > 0;

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" /> LOADING ALIASES...
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted-foreground">
        {supportsModels
          ? <>Map short @nicknames to models on this plugin. Use <code className="font-mono">@nickname</code> in chat to route to that model.</>
          : <>Map short @nicknames to this plugin. Use <code className="font-mono">@nickname</code> in chat to reference it.</>
        }
      </p>

      {supportsModels && plugin.status !== "running" && (
        <Alert variant="destructive">
          <AlertDescription>Plugin must be running to fetch available models.</AlertDescription>
        </Alert>
      )}
      {supportsModels && modelsLoading && (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="h-4 w-4 animate-spin" /> Fetching available models...
        </div>
      )}
      {supportsModels && modelsError && (
        <Alert variant="destructive">
          <AlertDescription className="flex items-center gap-2">
            {modelsError}
            <Button variant="link" size="sm" className="h-auto p-0" onClick={fetchModels}>
              retry
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {entries.length === 0 ? (
        <div className="rounded-md border bg-muted/30 p-6 text-center text-sm text-muted-foreground">
          No aliases configured.
        </div>
      ) : (
        <div className="rounded-md border">
          <div className={`grid gap-2 px-3 py-2 text-xs font-semibold tracking-wide text-muted-foreground border-b ${supportsModels ? "grid-cols-[1fr_1fr_auto]" : "grid-cols-[1fr_auto]"}`}>
            <span>NICKNAME</span>
            {supportsModels && <span>MODEL</span>}
            <span className="w-10" />
          </div>
          {entries.map((entry, i) => (
            <div
              key={i}
              className={`grid gap-2 px-3 py-2 items-center ${supportsModels ? "grid-cols-[1fr_1fr_auto]" : "grid-cols-[1fr_auto]"}`}
            >
              <Input
                value={entry.name}
                onChange={(e) => updateEntry(i, "name", e.target.value)}
                placeholder="e.g. files"
              />
              {supportsModels && (hasModels ? (
                <Select
                  value={targetToModel(entry.target, plugin.id) || "__default__"}
                  onValueChange={(v) => updateEntry(i, "model", v === "__default__" ? "" : v)}
                >
                  <SelectTrigger>
                    <SelectValue placeholder="-- default --" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__default__">-- default --</SelectItem>
                    {modelOptions.map((opt) => (
                      <SelectItem key={opt} value={opt}>{opt}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  value={targetToModel(entry.target, plugin.id)}
                  onChange={(e) => updateEntry(i, "model", e.target.value)}
                  placeholder={modelsLoading ? "loading models..." : "model name"}
                />
              ))}
              <Button
                variant="ghost"
                size="icon"
                onClick={() => removeEntry(i)}
                title="Remove alias"
              >
                <X className="h-4 w-4" />
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-col gap-2">
        <Button variant="outline" size="sm" className="w-fit" onClick={addEntry}>
          <Plus className="h-4 w-4" />
          Add Alias
        </Button>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        {saveSuccess && (
          <Alert>
            <AlertDescription>Aliases saved.</AlertDescription>
          </Alert>
        )}

        {dirty && (
          <Button
            onClick={handleSave}
            disabled={saving}
            className="w-fit"
          >
            {saving ? (
              <>
                <Loader2 className="h-4 w-4 animate-spin" />
                SAVING...
              </>
            ) : (
              "SAVE ALIASES"
            )}
          </Button>
        )}
      </div>
    </div>
  );
}
