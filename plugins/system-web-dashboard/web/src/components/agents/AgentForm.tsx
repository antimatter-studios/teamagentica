import { useState, useEffect, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import { apiClient } from "../../api/client";
import type { RegistryAlias, CreateAliasRequest, Plugin } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";

interface Props {
  alias?: RegistryAlias;
  plugins: Plugin[];
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function AgentForm({ alias, plugins, onSave, onCancel }: Props) {
  const { createAlias, updateAlias, deleteAlias } = useAgentStore();
  const isEdit = !!alias;
  const [name, setName] = useState(alias?.name ?? "");
  const [plugin, setPlugin] = useState(alias?.plugin ?? "");
  const [model, setModel] = useState(alias?.model ?? "");
  const [provider, setProvider] = useState(alias?.provider ?? "");
  const [systemPrompt, setSystemPrompt] = useState(alias?.system_prompt ?? "");
  const [models, setModels] = useState<string[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!plugin) { setModels([]); return; }
    let cancelled = false;
    setModelsLoading(true);
    apiClient.agents.pluginModels(plugin)
      .then((res) => { if (!cancelled) setModels(res.models || []); })
      .catch(() => { if (!cancelled) setModels([]); })
      .finally(() => { if (!cancelled) setModelsLoading(false); });
    return () => { cancelled = true; };
  }, [plugin]);

  const handlePluginChange = (pluginId: string) => {
    setPlugin(pluginId);
    setModel("");
    setProvider("");
  };

  const handleModelChange = (m: string) => {
    setModel(m);
    const slash = m.indexOf("/");
    setProvider(slash > 0 ? m.slice(0, slash) : "");
  };

  const save = useCallback(async () => {
    if (!name.trim() || !plugin.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateAlias(alias!.name, {
          name: name.trim() !== alias!.name ? name.trim() : undefined,
          type: "agent",
          plugin: plugin.trim(),
          provider: provider || undefined,
          model: model || undefined,
          system_prompt: systemPrompt || undefined,
        });
      } else {
        const req: CreateAliasRequest = {
          name: name.trim(), type: "agent", plugin: plugin.trim(),
          provider: provider || undefined,
          model: model || undefined,
          system_prompt: systemPrompt || undefined,
        };
        await createAlias(req);
      }
      onSave(isEdit ? undefined : name.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [name, plugin, provider, model, systemPrompt, isEdit, alias, onSave, createAlias, updateAlias]);

  const remove = useCallback(async () => {
    if (!alias) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteAlias(alias.name);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [alias, onSave, deleteAlias]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  const NONE = "__none__";

  return (
    <Card onKeyDown={handleKeyDown} className="flex flex-col">
      <CardHeader>
        <CardTitle>
          {isEdit ? <>Edit Agent: <span className="text-primary">@{name}</span></> : "Create Agent"}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="agent-name">Name</Label>
          <Input
            id="agent-name"
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            placeholder="alias name"
            autoFocus={!isEdit}
          />
          {name.trim() && (
            <span className="text-xs text-muted-foreground">
              use <strong>@{name.trim()}</strong> in your prompts
            </span>
          )}
        </div>

        <div className="flex flex-col gap-2">
          <Label>Plugin</Label>
          <Select
            value={plugin || NONE}
            onValueChange={(v) => handlePluginChange(v === NONE ? "" : v)}
          >
            <SelectTrigger>
              <SelectValue placeholder="Select plugin..." />
            </SelectTrigger>
            <SelectContent>
              {plugins.map((p) => (
                <SelectItem key={p.id} value={p.id}>{p.name} ({p.id})</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-2">
          <Label>Model</Label>
          {modelsLoading ? (
            <span className="text-sm text-muted-foreground">loading...</span>
          ) : models.length > 0 ? (
            <Select
              value={model || NONE}
              onValueChange={(v) => handleModelChange(v === NONE ? "" : v)}
            >
              <SelectTrigger>
                <SelectValue placeholder="Default" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Default</SelectItem>
                {models.map((m) => (
                  <SelectItem key={m} value={m}>{m}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
            <Input
              value={model}
              onChange={(e) => handleModelChange(e.target.value)}
              placeholder={plugin ? "no models found" : "select plugin first"}
              disabled={!plugin}
            />
          )}
        </div>

        {provider && (
          <div className="flex flex-col gap-2">
            <Label>Provider</Label>
            <Input value={provider} readOnly className="opacity-70" />
          </div>
        )}

        <div className="flex flex-col gap-2 flex-1">
          <Label htmlFor="agent-system-prompt">System Prompt</Label>
          <Textarea
            id="agent-system-prompt"
            value={systemPrompt}
            onChange={(e) => setSystemPrompt(e.target.value)}
            placeholder="System prompt..."
            className="resize-none font-sans min-h-[160px]"
          />
        </div>

        <div className="flex items-center gap-2 pt-2">
          <Button onClick={save} disabled={saving || !name.trim() || !plugin.trim()}>
            {saving ? "Saving..." : "Save"}
          </Button>
          <Button variant="outline" onClick={onCancel}>Cancel</Button>
          {isEdit && (
            <Button
              variant="destructive"
              onClick={() => setConfirmDelete(true)}
              disabled={deleting}
              className="ml-auto"
            >
              {deleting ? "..." : "Delete"}
            </Button>
          )}
        </div>
      </CardContent>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Agent</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete <strong>@{name}</strong>?
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => { setConfirmDelete(false); remove(); }}
              disabled={deleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleting ? "..." : "Yes"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Card>
  );
}
