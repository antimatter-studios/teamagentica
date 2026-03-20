import { useEffect, useState, useRef } from "react";
import { apiClient } from "../api/client";
import { parseConfigSchema } from "@teamagentica/api-client";
import type { Plugin, PluginConfigEntry, ConfigSchemaField, OAuthStatus, OAuthDeviceCode } from "@teamagentica/api-client";

export interface ConfigField {
  key: string;
  value: string;
  is_secret: boolean;
  hasStoredValue: boolean;
  schema: ConfigSchemaField | null;
}

export interface OAuthFieldState {
  status: OAuthStatus | null;
  loading: boolean;
  deviceCode: OAuthDeviceCode | null;
  polling: boolean;
  error?: string;
}

export interface DynamicOptionState {
  options: string[];
  loading: boolean;
  error?: string;
  fallback?: boolean;
}

export interface SchemaSection {
  name: string;
  fields: { key: string; value: string }[];
}

export function usePluginConfig(plugin: Plugin, onSaved: () => void) {
  const [fields, setFields] = useState<ConfigField[]>([]);
  const [dirty, setDirty] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [extraSections, setExtraSections] = useState<SchemaSection[]>([]);
  const [dynamicOptions, setDynamicOptions] = useState<
    Record<string, DynamicOptionState>
  >({});

  const [oauthStates, setOauthStates] = useState<Record<string, OAuthFieldState>>({});
  const pollTimersRef = useRef<Record<string, ReturnType<typeof setInterval>>>({});

  useEffect(() => {
    return () => {
      for (const timer of Object.values(pollTimersRef.current)) {
        clearInterval(timer);
      }
    };
  }, []);

  // Fetch OAuth status for visible oauth fields when plugin is running.
  useEffect(() => {
    if (fields.length === 0) return;

    const schema = parseConfigSchema(plugin);
    const oauthFields = Object.entries(schema).filter(([, f]) => f.type === "oauth");
    if (oauthFields.length === 0) return;

    const newStates: Record<string, OAuthFieldState> = {};
    for (const [key, field] of oauthFields) {
      if (field.visible_when) {
        const dep = fields.find((f) => f.key === field.visible_when!.field);
        if (dep && dep.value !== field.visible_when.value) continue;
      }

      if (plugin.status !== "running") {
        newStates[key] = { status: null, loading: false, deviceCode: null, polling: false, error: "Plugin must be running to authenticate" };
        continue;
      }

      // Skip if we already have a valid state (authenticated or actively polling).
      // Re-fetch if the previous state was an error (e.g. "Plugin must be running").
      const existing = oauthStates[key];
      if (existing && !existing.error) continue;
      newStates[key] = { status: null, loading: true, deviceCode: null, polling: false };
    }

    if (Object.keys(newStates).length === 0) return;
    setOauthStates((prev) => ({ ...prev, ...newStates }));

    for (const key of Object.keys(newStates)) {
      if (!newStates[key].loading) continue;
      apiClient.plugins.getOAuthStatus(plugin.id)
        .then((status) => {
          setOauthStates((prev) => ({
            ...prev,
            [key]: { ...prev[key], status, loading: false },
          }));
        })
        .catch(() => {
          setOauthStates((prev) => ({
            ...prev,
            [key]: { ...prev[key], status: null, loading: false },
          }));
        });
    }
  }, [plugin.id, plugin.status, fields]);

  async function handleOAuthLogin(fieldKey: string) {
    setOauthStates((prev) => ({
      ...prev,
      [fieldKey]: { ...prev[fieldKey], error: undefined },
    }));

    try {
      const dcr = await apiClient.plugins.startOAuthFlow(plugin.id);
      setOauthStates((prev) => ({
        ...prev,
        [fieldKey]: { ...prev[fieldKey], deviceCode: dcr, polling: true },
      }));

      pollTimersRef.current[fieldKey] = setInterval(async () => {
        try {
          const res = await apiClient.plugins.pollOAuthFlow(plugin.id);
          if (res.authenticated) {
            clearInterval(pollTimersRef.current[fieldKey]);
            delete pollTimersRef.current[fieldKey];
            const status = await apiClient.plugins.getOAuthStatus(plugin.id);
            setOauthStates((prev) => ({
              ...prev,
              [fieldKey]: { status, loading: false, deviceCode: null, polling: false },
            }));
          }
        } catch {
          // Keep polling — transient errors expected.
        }
      }, 5 * 1000);
    } catch (err) {
      setOauthStates((prev) => ({
        ...prev,
        [fieldKey]: {
          ...prev[fieldKey],
          error: err instanceof Error ? err.message : "Failed to start login",
        },
      }));
    }
  }

  async function handleOAuthLogout(fieldKey: string) {
    setOauthStates((prev) => ({
      ...prev,
      [fieldKey]: { ...prev[fieldKey], error: undefined },
    }));

    try {
      await apiClient.plugins.oauthLogout(plugin.id);
      setOauthStates((prev) => ({
        ...prev,
        [fieldKey]: { status: { authenticated: false }, loading: false, deviceCode: null, polling: false },
      }));
    } catch (err) {
      setOauthStates((prev) => ({
        ...prev,
        [fieldKey]: {
          ...prev[fieldKey],
          error: err instanceof Error ? err.message : "Failed to logout",
        },
      }));
    }
  }

  useEffect(() => {
    async function load() {
      try {
        const entries: PluginConfigEntry[] = await apiClient.plugins.getConfig(plugin.id);
        // Fetch live schema from running plugin; fall back to DB-cached schema.
        const schema = plugin.status === "running"
          ? await apiClient.plugins.getConfigSchema(plugin.id)
          : parseConfigSchema(plugin);
        const entryMap = new Map(entries.map((e) => [e.key, e]));

        const configFields: ConfigField[] = Object.entries(schema).map(
          ([key, field]) => {
            const existing = entryMap.get(key);
            const isSecret = field.secret || false;
            const hasStored = existing != null && existing.is_secret && existing.value === "********";
            return {
              key,
              value: existing && !existing.is_secret ? existing.value : field.default || "",
              is_secret: isSecret,
              hasStoredValue: hasStored,
              schema: field,
            };
          }
        );

        setFields(configFields);

        // Fetch full schema to extract non-config readonly sections.
        if (plugin.status === "running") {
          try {
            const fullSchema = await apiClient.plugins.getSchema(plugin.id);
            const sections: SchemaSection[] = [];
            for (const [sectionName, raw] of Object.entries(fullSchema)) {
              if (sectionName === "config") continue;
              if (typeof raw !== "object" || raw === null || Array.isArray(raw)) continue;
              const kvMap = raw as Record<string, unknown>;
              const sectionFields = Object.entries(kvMap)
                .map(([k, v]) => ({
                  key: k,
                  value: typeof v === "string" ? v : v == null ? "" : JSON.stringify(v),
                }))
                .sort((a, b) => a.key.localeCompare(b.key));
              sections.push({ name: sectionName, fields: sectionFields });
            }
            sections.sort((a, b) => a.name.localeCompare(b.name));
            setExtraSections(sections);
          } catch {
            // Non-critical — just skip extra sections.
          }
        } else {
          setExtraSections([]);
        }

        const dynamicFields = Object.entries(schema).filter(
          ([, f]) => f.dynamic && f.type === "select"
        );
        if (dynamicFields.length > 0) {
          const initial: Record<string, DynamicOptionState> = {};
          for (const [key] of dynamicFields) {
            if (plugin.status === "running") {
              initial[key] = { options: [], loading: true };
            } else {
              initial[key] = { options: [], loading: false, error: "Plugin is not running" };
            }
          }
          setDynamicOptions(initial);

          if (plugin.status === "running") {
            for (const [key] of dynamicFields) {
              apiClient.plugins.getFieldOptions(plugin.id, key)
                .then((res) => {
                  setDynamicOptions((prev) => ({
                    ...prev,
                    [key]: { options: res.options || [], loading: false, error: res.error, fallback: res.fallback },
                  }));
                })
                .catch((err) => {
                  setDynamicOptions((prev) => ({
                    ...prev,
                    [key]: {
                      options: [],
                      loading: false,
                      error: err instanceof Error ? err.message : "Failed to fetch options",
                    },
                  }));
                });
            }
          }
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "Failed to load config");
      } finally {
        setLoading(false);
      }
    }
    load();
  }, [plugin.id, plugin.status]);

  function updateField(index: number, value: string) {
    setDirty(true);
    setFields((prev) =>
      prev.map((f, i) => (i === index ? { ...f, value } : f))
    );
  }

  async function handleSave() {
    setSaving(true);
    setError("");
    setSaveSuccess(false);
    try {
      const config: Record<string, { value: string; is_secret: boolean }> = {};
      for (const field of fields) {
        if (field.schema?.type === "oauth") continue;
        if (field.schema?.readonly) continue;
        if (field.is_secret && field.value === "") continue;
        config[field.key] = {
          value: field.value,
          is_secret: field.is_secret,
        };
      }
      await apiClient.plugins.updateConfig(plugin.id, config);
      setDirty(false);
      setSaveSuccess(true);
      onSaved();
      setTimeout(() => setSaveSuccess(false), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save config");
    } finally {
      setSaving(false);
    }
  }

  return {
    fields,
    dirty,
    loading,
    saving,
    error,
    saveSuccess,
    extraSections,
    dynamicOptions,
    oauthStates,
    updateField,
    handleSave,
    handleOAuthLogin,
    handleOAuthLogout,
  };
}
