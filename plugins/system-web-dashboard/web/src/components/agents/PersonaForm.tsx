import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { AgentEntry, CreateAgentEntryRequest } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Switch } from "@/components/ui/switch";
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
  agent?: AgentEntry;
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function AgentEntryForm({ agent, onSave, onCancel }: Props) {
  const { chatAliases, createAgent, updateAgent, deleteAgent } = useAgentStore();
  const isEdit = !!agent;
  const [alias, setAlias] = useState(agent?.alias ?? "");
  const [plugin, setPlugin] = useState(agent?.plugin ?? "");
  const [model, setModel] = useState(agent?.model ?? "");
  const [systemPrompt, setSystemPrompt] = useState(agent?.system_prompt ?? "");
  const [isDefault, setIsDefault] = useState(!!agent?.is_default);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!alias.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateAgent(agent!.alias, {
          alias: alias.trim() !== agent!.alias ? alias.trim() : undefined,
          system_prompt: systemPrompt || undefined,
          plugin: plugin || undefined,
          is_default: isDefault || undefined,
        });
      } else {
        const req: CreateAgentEntryRequest = {
          alias: alias.trim(),
          system_prompt: systemPrompt,
          plugin: plugin || undefined,
          is_default: isDefault || undefined,
        };
        await createAgent(req);
      }
      onSave(isEdit ? undefined : alias.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [alias, plugin, systemPrompt, isDefault, isEdit, agent, onSave, createAgent, updateAgent]);

  const remove = useCallback(async () => {
    if (!agent) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteAgent(agent.alias);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [agent, onSave, deleteAgent]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  const agents = chatAliases();
  const pluginOptions = Array.from(new Set(agents.map((a) => a.plugin))).sort();

  const NONE = "__none__";

  return (
    <Card onKeyDown={handleKeyDown} className="flex flex-col">
      <CardHeader>
        <CardTitle>
          {isEdit ? <>Edit Agent: <span className="text-primary">@{alias}</span></> : "Create Agent"}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="persona-name">Name</Label>
          <Input
            id="persona-name"
            value={alias}
            onChange={(e) => setAlias(e.target.value.toLowerCase())}
            placeholder="agent name"
            autoFocus={!isEdit}
          />
          {alias.trim() && (
            <span className="text-xs text-muted-foreground">
              use <strong>@{alias.trim()}</strong> in your prompts
            </span>
          )}
        </div>

        <div className="flex items-center gap-3">
          <Switch
            id="persona-default"
            checked={isDefault}
            onCheckedChange={setIsDefault}
          />
          <div className="flex flex-col">
            <Label htmlFor="persona-default">Default Agent</Label>
            <span className="text-xs text-muted-foreground">handles unaddressed messages</span>
          </div>
        </div>

        <div className="flex flex-col gap-2">
          <Label>Plugin</Label>
          <Select
            value={plugin || NONE}
            onValueChange={(v) => setPlugin(v === NONE ? "" : v)}
          >
            <SelectTrigger>
              <SelectValue placeholder="Select plugin..." />
            </SelectTrigger>
            <SelectContent>
              {pluginOptions.map((p) => (
                <SelectItem key={p} value={p}>{p}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="persona-model">Model</Label>
          <Input
            id="persona-model"
            value={model}
            onChange={(e) => setModel(e.target.value)}
            placeholder="model override (optional)"
          />
        </div>

        <div className="flex flex-col gap-2 flex-1">
          <Label htmlFor="persona-system-prompt">System Prompt</Label>
          <Textarea
            id="persona-system-prompt"
            value={systemPrompt}
            onChange={(e) => setSystemPrompt(e.target.value)}
            placeholder="System prompt (leave empty for default)..."
            className="resize-none font-sans min-h-[160px]"
          />
        </div>

        <div className="flex items-center gap-2 pt-2">
          <Button onClick={save} disabled={saving || !alias.trim()}>
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
              Are you sure you want to delete <strong>@{alias}</strong>?
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
