import { useState, useCallback } from "react";
import { useAgentStore } from "../../stores/agentStore";
import type { PersonaRole } from "@teamagentica/api-client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
  role?: PersonaRole;
  onSave: (createdId?: string) => void;
  onCancel: () => void;
}

export default function RoleForm({ role, onSave, onCancel }: Props) {
  const { createRole, updateRole, deleteRole } = useAgentStore();
  const isEdit = !!role;
  const [id, setId] = useState(role?.id ?? "");
  const [label, setLabel] = useState(role?.label ?? "");
  const [systemPrompt, setSystemPrompt] = useState(role?.system_prompt ?? "");
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = useCallback(async () => {
    if (!id.trim() || !label.trim()) return;
    setSaving(true);
    setError(null);
    try {
      if (isEdit) {
        await updateRole(role!.id, {
          label: label || undefined,
          system_prompt: systemPrompt || undefined,
        });
      } else {
        await createRole({
          id: id.trim(),
          label: label.trim(),
          system_prompt: systemPrompt || undefined,
        });
      }
      onSave(isEdit ? undefined : id.trim());
    } catch (e) {
      setError(e instanceof Error ? e.message : "Save failed");
    } finally {
      setSaving(false);
    }
  }, [id, label, systemPrompt, isEdit, role, onSave, createRole, updateRole]);

  const remove = useCallback(async () => {
    if (!role) return;
    setDeleting(true);
    setError(null);
    try {
      await deleteRole(role.id);
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setDeleting(false);
    }
  }, [role, onSave, deleteRole]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onCancel();
  };

  return (
    <Card onKeyDown={handleKeyDown} className="flex flex-col">
      <CardHeader>
        <CardTitle>
          {isEdit ? <>Edit Role: <span className="text-primary">{id}</span></> : "Create Role"}
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <div className="flex flex-col gap-2">
          <Label htmlFor="role-id">ID</Label>
          <Input
            id="role-id"
            value={id}
            onChange={(e) => setId(e.target.value)}
            placeholder="role-id"
            autoFocus={!isEdit}
          />
        </div>

        <div className="flex flex-col gap-2">
          <Label htmlFor="role-label">Label</Label>
          <Input
            id="role-label"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="Display name"
          />
        </div>

        <div className="flex flex-col gap-2 flex-1">
          <Label htmlFor="role-system-prompt">System Prompt</Label>
          <Textarea
            id="role-system-prompt"
            value={systemPrompt}
            onChange={(e) => setSystemPrompt(e.target.value)}
            placeholder="Default system prompt for personas with this role..."
            className="resize-none font-sans min-h-[160px]"
          />
        </div>

        <div className="flex items-center gap-2 pt-2">
          <Button onClick={save} disabled={saving || !id.trim() || !label.trim()}>
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
            <AlertDialogTitle>Delete Role</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete role <strong>{id}</strong>?
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
