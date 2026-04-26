import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { RegistryAlias, CreateAliasRequest, Plugin } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
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

export default function ToolForm({ alias, plugins, onSave, onCancel }: Props) {
  const { createAlias, updateAlias, deleteAlias } = useAgentStore();
  const isEdit = !!alias;
  const [name, setName] = useState(alias?.name ?? "");
  const [plugin, setPlugin] = useState(alias?.plugin ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!name.trim() || !plugin.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateAlias(alias!.name, {
          name: name.trim() !== alias!.name ? name.trim() : undefined,
          type: "tool",
          plugin: plugin.trim(),
        });
      } else {
        const req: CreateAliasRequest = {
          name: name.trim(), type: "tool", plugin: plugin.trim(),
        };
        await createAlias(req);
      }
      onSave(isEdit ? undefined : name.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [name, plugin, isEdit, alias, onSave, createAlias, updateAlias]);

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
          {isEdit ? <>Edit Tool: <span className="text-primary">@{name}</span></> : "Create Tool"}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="tool-name">Name</Label>
          <Input
            id="tool-name"
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
            onValueChange={(v) => setPlugin(v === NONE ? "" : v)}
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
            <AlertDialogTitle>Delete Tool</AlertDialogTitle>
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
