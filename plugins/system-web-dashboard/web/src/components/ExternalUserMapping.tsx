import { useEffect, useState } from "react";
import { apiClient } from "../api/client";
import type { ExternalUserMapping as Mapping } from "@teamagentica/api-client";
import { useCostStore } from "../stores/costStore";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

interface Props {
  onClose: () => void;
}

export default function ExternalUserMapping({ onClose }: Props) {
  const users = useCostStore((s) => s.users);
  const [mappings, setMappings] = useState<Mapping[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // New mapping form.
  const [newExternalID, setNewExternalID] = useState("");
  const [newSource, setNewSource] = useState("messaging-telegram");
  const [newTeamagenticaUserID, setNewTeamagenticaUserID] = useState("");
  const [newLabel, setNewLabel] = useState("");
  const [saving, setSaving] = useState(false);

  const loadMappings = async () => {
    try {
      const resp = await apiClient.costs.fetchExternalUsers();
      setMappings(resp.mappings || []);
      setLoading(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load mappings");
      setLoading(false);
    }
  };

  useEffect(() => {
    loadMappings();
  }, []);

  // Find unmapped external user IDs (in cost data but not in mappings).
  const mappedIDs = new Set(mappings.map((m) => `${m.source}:${m.external_id}`));
  const unmapped = users.filter((u) => {
    // External IDs have format "source:id" (e.g. "telegram:123456").
    if (!u.user_id.includes(":")) return false;
    const [source, extID] = u.user_id.split(":", 2);
    return !mappedIDs.has(`${source}:${extID}`);
  });

  const handleCreate = async () => {
    if (!newExternalID || !newTeamagenticaUserID) return;
    setSaving(true);
    try {
      await apiClient.costs.createExternalUser({
        external_id: newExternalID,
        source: newSource,
        teamagentica_user_id: parseInt(newTeamagenticaUserID, 10),
        label: newLabel || undefined,
      });
      setNewExternalID("");
      setNewTeamagenticaUserID("");
      setNewLabel("");
      await loadMappings();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create mapping");
    }
    setSaving(false);
  };

  const handleDelete = async (id: number) => {
    try {
      await apiClient.costs.deleteExternalUser(id);
      await loadMappings();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to delete mapping");
    }
  };

  const handleQuickMap = (userID: string) => {
    const [source, extID] = userID.split(":", 2);
    setNewSource(source);
    setNewExternalID(extID);
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>External User Mapping</DialogTitle>
        </DialogHeader>

        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        {/* Unmapped users */}
        {unmapped.length > 0 && (
          <div className="space-y-2">
            <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              Unmapped External Users
            </h4>
            <div className="flex flex-wrap gap-2">
              {unmapped.map((u) => (
                <Button
                  key={u.user_id}
                  variant="outline"
                  size="sm"
                  onClick={() => handleQuickMap(u.user_id)}
                >
                  {u.user_id} <Badge variant="secondary" className="ml-2">{u.count}</Badge>
                </Button>
              ))}
            </div>
          </div>
        )}

        {/* Add new mapping */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-5 sm:items-end">
          <div className="space-y-1">
            <Label>Source</Label>
            <Select value={newSource} onValueChange={setNewSource}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="messaging-telegram">messaging-telegram</SelectItem>
                <SelectItem value="messaging-discord">messaging-discord</SelectItem>
                <SelectItem value="messaging-whatsapp">messaging-whatsapp</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="external-id">External ID</Label>
            <Input
              id="external-id"
              placeholder="123456789"
              value={newExternalID}
              onChange={(e) => setNewExternalID(e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="ta-user-id">Teamagentica User ID</Label>
            <Input
              id="ta-user-id"
              placeholder="7"
              value={newTeamagenticaUserID}
              onChange={(e) => setNewTeamagenticaUserID(e.target.value)}
            />
          </div>
          <div className="space-y-1">
            <Label htmlFor="mapping-label">Label</Label>
            <Input
              id="mapping-label"
              placeholder="optional"
              value={newLabel}
              onChange={(e) => setNewLabel(e.target.value)}
            />
          </div>
          <Button
            onClick={handleCreate}
            disabled={saving || !newExternalID || !newTeamagenticaUserID}
          >
            {saving ? "..." : "Add"}
          </Button>
        </div>

        {/* Existing mappings */}
        {loading ? (
          <div className="text-sm text-muted-foreground">Loading...</div>
        ) : mappings.length === 0 ? (
          <div className="text-sm text-muted-foreground">No mappings configured yet.</div>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Source</TableHead>
                <TableHead>External ID</TableHead>
                <TableHead>Teamagentica User</TableHead>
                <TableHead>Label</TableHead>
                <TableHead className="w-12" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {mappings.map((m) => (
                <TableRow key={m.id}>
                  <TableCell>{m.source}</TableCell>
                  <TableCell>{m.external_id}</TableCell>
                  <TableCell>{m.teamagentica_user_id}</TableCell>
                  <TableCell>{m.label}</TableCell>
                  <TableCell>
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-8 w-8 text-destructive hover:text-destructive"
                      onClick={() => handleDelete(m.id)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </DialogContent>
    </Dialog>
  );
}
