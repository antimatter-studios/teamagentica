import { useState, useEffect, useCallback, useMemo, useRef, memo } from "react";
import { ProgressBar } from "./ProgressBar";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  closestCorners,
  MeasuringStrategy,
  type DragStartEvent,
  type DragOverEvent,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { useKanbanStore } from "../stores/kanbanStore";
import { useUserStore } from "../stores/userStore";
import { useAgentStore } from "../stores/agentStore";
import { apiClient } from "../api/client";
import type { Column, Epic, Card as CardType, Comment, UserDetails, RegistryAlias } from "@teamagentica/api-client";
import ConfirmDialog from "./ConfirmDialog";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Badge } from "@/components/ui/badge";
import { Separator } from "@/components/ui/separator";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { ChevronDown, Plus, Trash2, X } from "lucide-react";


// ── Position helpers ──────────────────────────────────────────────────────────

function positionAfter(items: { position: number }[]): number {
  if (items.length === 0) return 1000;
  return items[items.length - 1].position + 1000;
}

function positionBefore(items: { position: number }[], idx: number): number {
  if (items.length === 0) return 1000;
  if (idx === 0) return items[0].position - 1000;
  return (items[idx - 1].position + items[idx].position) / 2;
}

// ── Panel state ───────────────────────────────────────────────────────────────

type PanelState =
  | { type: "new-board" }
  | { type: "new-card"; columnId: string; columnName: string }
  | { type: "edit-card"; card: CardType; columnName: string };

interface CardFormData {
  title: string;
  description: string;
  cardType: string;
  priority: string;
  assigneeId: number;
  assigneeAgent: string;
  labels: string;
  dueDate: string; // "YYYY-MM-DD" or ""
  epicId: string;
}

// ── Priority helpers ──────────────────────────────────────────────────────────

const CARD_TYPE_LABEL: Record<string, string> = {
  task: "Task", bug: "Bug",
};

const CARD_TYPE_VARIANT: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  task: "secondary",
  bug: "destructive",
};

const PRIORITY_LABEL: Record<string, string> = {
  low: "Low", medium: "Medium", high: "High", urgent: "Urgent",
};

const PRIORITY_VARIANT: Record<string, "default" | "secondary" | "destructive" | "outline"> = {
  low: "outline",
  medium: "secondary",
  high: "default",
  urgent: "destructive",
};

const measuring = {
  droppable: { strategy: MeasuringStrategy.Always },
};

function formatDue(ms: number): string {
  const d = new Date(ms);
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}
function isOverdue(ms: number): boolean {
  return ms < Date.now();
}

// ── Card ──────────────────────────────────────────────────────────────────────

const KanbanCard = memo(function KanbanCard({
  card,
  isSelected,
  onEdit,
  boardPrefix,
}: {
  card: CardType;
  isSelected: boolean;
  onEdit: (card: CardType) => void;
  boardPrefix?: string;
}) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: card.id, data: { type: "card", card } });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.35 : 1,
    willChange: transform ? "transform" : undefined,
  };

  const labels = card.labels ? card.labels.split(",").map(l => l.trim()).filter(Boolean) : [];
  const overdue = card.due_date ? isOverdue(card.due_date) : false;

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
    >
      <Card
        className={cn(
          "flex cursor-grab flex-col gap-2 p-3 shadow-sm transition-shadow hover:shadow-md active:cursor-grabbing",
          isSelected && "ring-2 ring-primary"
        )}
      >
        {/* Top meta row: type + priority + assignee + card number */}
        {(card.card_type || card.priority || card.assignee_name || card.number > 0) && (
          <div className="flex flex-wrap items-center gap-1.5">
            {card.card_type && (
              <Badge variant={CARD_TYPE_VARIANT[card.card_type] ?? "outline"}>
                {CARD_TYPE_LABEL[card.card_type] ?? card.card_type}
              </Badge>
            )}
            {card.priority && (
              <Badge variant={PRIORITY_VARIANT[card.priority] ?? "outline"}>
                {PRIORITY_LABEL[card.priority]}
              </Badge>
            )}
            {card.assignee_name && (
              <span className="text-xs text-muted-foreground">{card.assignee_name}</span>
            )}
            {card.number > 0 && (
              <span className="ml-auto font-mono text-xs text-muted-foreground">
                {cardRef(boardPrefix, card.number)}
              </span>
            )}
          </div>
        )}

        <span className="text-sm font-medium leading-snug">
          {card.title}
        </span>

        {card.description && (
          <span className="line-clamp-2 text-xs text-muted-foreground">{card.description}</span>
        )}

        {/* Labels */}
        {labels.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {labels.map(l => (
              <Badge key={l} variant="outline" className="text-[10px]">{l}</Badge>
            ))}
          </div>
        )}

        {/* Due date */}
        {card.due_date && (
          <span className={cn(
            "text-xs",
            overdue ? "font-medium text-destructive" : "text-muted-foreground"
          )}>
            {overdue ? "⚠ " : ""}Due {formatDue(card.due_date)}
          </span>
        )}

        <div className="flex justify-end">
          <Button
            variant="ghost"
            size="sm"
            onPointerDown={(e) => e.stopPropagation()}
            onClick={(e) => { e.stopPropagation(); onEdit(card); }}
          >
            Open
          </Button>
        </div>
      </Card>
    </div>
  );
});

// ── Column ────────────────────────────────────────────────────────────────────

const KanbanColumn = memo(function KanbanColumn({
  column,
  cards,
  selectedCardId,
  onAddCard,
  onEditCard,
  boardPrefix,
}: {
  column: Column;
  cards: CardType[];
  selectedCardId: string | null;
  onAddCard: (column: Column) => void;
  onEditCard: (card: CardType, columnName: string) => void;
  boardPrefix?: string;
}) {
  const { setNodeRef } = useSortable({
    id: `col-${column.id}`,
    data: { type: "column", column },
  });

  return (
    <div ref={setNodeRef} className="flex flex-1 min-w-[14rem] flex-col gap-2 rounded-lg border bg-muted/30 p-2">
      <div className="flex items-center gap-2 px-2 pt-1">
        <span className="text-sm font-semibold">{column.name}</span>
        <span className="text-xs text-muted-foreground">({cards.length})</span>
      </div>

      <SortableContext items={cards.map((c) => c.id)} strategy={verticalListSortingStrategy}>
        <div className="flex flex-col gap-2">
          {cards.map((card) => (
            <KanbanCard
              key={card.id}
              card={card}
              isSelected={card.id === selectedCardId}
              onEdit={(c) => onEditCard(c, column.name)}
              boardPrefix={boardPrefix}
            />
          ))}
          {cards.length === 0 && (
            <div className="rounded-md border border-dashed py-6 text-center text-xs text-muted-foreground">
              No cards
            </div>
          )}
        </div>
      </SortableContext>

      <Button
        variant="ghost"
        size="sm"
        className="justify-start"
        onClick={() => onAddCard(column)}
      >
        <Plus className="h-4 w-4" /> Add card
      </Button>
    </div>
  );
});

// ── Board Manager (Columns + Epics) ──────────────────────────────────────────

function BoardSettingsForm({ boardId }: { boardId: string }) {
  const boards = useKanbanStore((s) => s.boards);
  const board = boards.find((b) => b.id === boardId);
  const [name, setName] = useState(board?.name ?? "");
  const [prefix, setPrefix] = useState(board?.prefix ?? "");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setName(board?.name ?? "");
    setPrefix(board?.prefix ?? "");
  }, [board?.name, board?.prefix]);

  const dirty = name !== (board?.name ?? "") || prefix !== (board?.prefix ?? "");

  async function save() {
    if (!dirty || !name.trim()) return;
    setSaving(true);
    setError(null);
    try {
      await useKanbanStore.getState().updateBoard(boardId, {
        name: name.trim(),
        prefix: prefix.trim().toUpperCase(),
      });
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      setError(String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <h4 className="text-sm font-semibold">Board Settings</h4>
      {error && (
        <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>
      )}
      <div className="flex flex-col gap-2">
        <Label>Board Name</Label>
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") save(); }}
          placeholder="Board name"
        />
      </div>
      <div className="flex flex-col gap-2">
        <Label>Prefix</Label>
        <Input
          value={prefix}
          onChange={(e) => setPrefix(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ""))}
          onKeyDown={(e) => { if (e.key === "Enter") save(); }}
          placeholder="e.g. INFRA"
          maxLength={10}
        />
        <span className="text-xs text-muted-foreground">
          {prefix ? `Cards will be referenced as ${prefix}-123` : "Used to reference cards like INFRA-123"}
        </span>
      </div>
      <div className="flex justify-end">
        <Button onClick={save} disabled={!dirty || saving}>
          {saving ? "Saving..." : saved ? "Saved" : "Save"}
        </Button>
      </div>
    </div>
  );
}

function BoardManager({
  columns,
  epics,
  cards,
  boardId,
  onBack,
  onColumnsChange,
  onCardsChange,
  onEpicsChange,
}: {
  columns: Column[];
  epics: Epic[];
  cards: CardType[];
  boardId: string;
  onBack: () => void;
  onColumnsChange: (cols: Column[]) => void;
  onCardsChange: (cards: CardType[]) => void;
  onEpicsChange: (epics: Epic[]) => void;
}) {
  // ── Column state ──
  const [newColName, setNewColName] = useState("");
  const [renamingCol, setRenamingCol] = useState<string | null>(null);
  const [renameColValue, setRenameColValue] = useState("");
  const [deletingCol, setDeletingCol] = useState<string | null>(null);
  const [reassignTarget, setReassignTarget] = useState<string>("");

  // ── Epic state ──
  const [newEpicName, setNewEpicName] = useState("");
  const [newEpicColor, setNewEpicColor] = useState("#4A90D9");
  const [editingEpic, setEditingEpic] = useState<string | null>(null);
  const [editEpicName, setEditEpicName] = useState("");
  const [editEpicDesc, setEditEpicDesc] = useState("");
  const [editEpicColor, setEditEpicColor] = useState("");
  const [deletingEpic, setDeletingEpic] = useState<string | null>(null);

  const [error, setError] = useState<string | null>(null);

  const sortedCols = useMemo(
    () => [...columns].sort((a, b) => a.position - b.position),
    [columns]
  );

  const sortedEpics = useMemo(
    () => [...epics].sort((a, b) => a.position - b.position),
    [epics]
  );

  function colCardCount(colId: string): number {
    return cards.filter((c) => c.column_id === colId).length;
  }

  function epicCardCount(epicId: string): number {
    return cards.filter((c) => c.epic_id === epicId).length;
  }

  // ── Column actions ──

  async function createColumn() {
    const name = newColName.trim();
    if (!name) return;
    try {
      const pos = positionAfter(sortedCols);
      const col = await useKanbanStore.getState().createColumn(boardId, { name, position: pos });
      onColumnsChange([...columns, col]);
      setNewColName("");
    } catch (e) {
      setError(String(e));
    }
  }

  async function renameColumn(colId: string) {
    const name = renameColValue.trim();
    if (!name) return;
    try {
      const updated = await useKanbanStore.getState().updateColumn(boardId, colId, { name });
      onColumnsChange(columns.map((c) => (c.id === updated.id ? updated : c)));
      setRenamingCol(null);
    } catch (e) {
      setError(String(e));
    }
  }

  async function deleteColumn(colId: string) {
    const count = colCardCount(colId);
    if (count > 0 && !reassignTarget) return;
    try {
      if (count > 0 && reassignTarget) {
        const store = useKanbanStore.getState();
        const colCards = cards.filter((c) => c.column_id === colId);
        for (const card of colCards) {
          await store.updateCard(boardId, card.id, { column_id: reassignTarget });
        }
        onCardsChange(
          cards.map((c) => c.column_id === colId ? { ...c, column_id: reassignTarget } : c)
        );
      }
      await useKanbanStore.getState().deleteColumn(boardId, colId);
      onColumnsChange(columns.filter((c) => c.id !== colId));
      setDeletingCol(null);
      setReassignTarget("");
    } catch (e) {
      setError(String(e));
    }
  }

  // ── Epic actions ──

  async function createEpic() {
    const name = newEpicName.trim();
    if (!name) return;
    try {
      const pos = positionAfter(sortedEpics);
      const epic = await useKanbanStore.getState().createEpic(boardId, { name, color: newEpicColor, position: pos });
      onEpicsChange([...epics, epic]);
      setNewEpicName("");
      setNewEpicColor("#4A90D9");
    } catch (e) {
      setError(String(e));
    }
  }

  async function saveEpic(epicId: string) {
    const name = editEpicName.trim();
    if (!name) return;
    try {
      const updated = await useKanbanStore.getState().updateEpic(boardId, epicId, {
        name, description: editEpicDesc.trim(), color: editEpicColor,
      });
      onEpicsChange(epics.map((e) => (e.id === updated.id ? updated : e)));
      setEditingEpic(null);
    } catch (e) {
      setError(String(e));
    }
  }

  async function deleteEpic(epicId: string) {
    try {
      await useKanbanStore.getState().deleteEpic(boardId, epicId);
      onEpicsChange(epics.filter((e) => e.id !== epicId));
      onCardsChange(cards.map((c) => c.epic_id === epicId ? { ...c, epic_id: "" } : c));
      setDeletingEpic(null);
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <div className="flex flex-col gap-4 p-4">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" onClick={onBack}>Back to board</Button>
        <h3 className="text-lg font-semibold">Manage Board</h3>
      </div>

      {error && (
        <Alert variant="destructive"><AlertDescription>{error}</AlertDescription></Alert>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <Card>
          <CardContent className="flex flex-col gap-3 pt-6">
            <h4 className="text-sm font-semibold">Columns</h4>

            <div className="flex flex-col gap-2">
              {sortedCols.map((col) => {
                const count = colCardCount(col.id);
                const isRenaming = renamingCol === col.id;
                const isDeleting = deletingCol === col.id;

                return (
                  <div key={col.id} className="rounded-md border p-2">
                    {isRenaming ? (
                      <div className="flex items-center gap-2">
                        <Input
                          value={renameColValue}
                          onChange={(e) => setRenameColValue(e.target.value)}
                          onKeyDown={(e) => { if (e.key === "Enter") renameColumn(col.id); if (e.key === "Escape") setRenamingCol(null); }}
                          autoFocus
                        />
                        <Button size="sm" onClick={() => renameColumn(col.id)}>Save</Button>
                        <Button size="sm" variant="ghost" onClick={() => setRenamingCol(null)}>Cancel</Button>
                      </div>
                    ) : isDeleting ? (
                      <div className="flex flex-col gap-2">
                        <span className="text-sm">
                          Delete "{col.name}"?
                          {count > 0 && ` Move ${count} card${count !== 1 ? "s" : ""} to:`}
                        </span>
                        {count > 0 && (
                          <Select value={reassignTarget} onValueChange={setReassignTarget}>
                            <SelectTrigger><SelectValue placeholder="Select column..." /></SelectTrigger>
                            <SelectContent>
                              {sortedCols.filter((c) => c.id !== col.id).map((c) => (
                                <SelectItem key={c.id} value={c.id}>{c.name}</SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}
                        <div className="flex gap-2">
                          <Button
                            size="sm"
                            variant="destructive"
                            disabled={count > 0 && !reassignTarget}
                            onClick={() => deleteColumn(col.id)}
                          >
                            Confirm delete
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => { setDeletingCol(null); setReassignTarget(""); }}>
                            Cancel
                          </Button>
                        </div>
                      </div>
                    ) : (
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">{col.name}</span>
                        <span className="text-xs text-muted-foreground">({count})</span>
                        <div className="ml-auto flex gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => { setRenamingCol(col.id); setRenameColValue(col.name); setDeletingCol(null); }}
                          >
                            Rename
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            className="text-destructive hover:text-destructive"
                            onClick={() => { setDeletingCol(col.id); setRenamingCol(null); setReassignTarget(""); }}
                          >
                            Delete
                          </Button>
                        </div>
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            <div className="flex gap-2">
              <Input
                placeholder="New column name"
                value={newColName}
                onChange={(e) => setNewColName(e.target.value)}
                onKeyDown={(e) => { if (e.key === "Enter") createColumn(); }}
              />
              <Button onClick={createColumn}>Create</Button>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardContent className="pt-6">
            <BoardSettingsForm boardId={boardId} />
          </CardContent>
        </Card>
      </div>

      {/* ── Epics Section ── */}
      <Card>
        <CardContent className="flex flex-col gap-3 pt-6">
          <h4 className="text-sm font-semibold">Epics</h4>

          <div className="flex flex-col gap-2">
            {sortedEpics.length === 0 && (
              <div className="rounded-md border p-2 text-sm text-muted-foreground">No epics yet</div>
            )}
            {sortedEpics.map((ep) => {
              const count = epicCardCount(ep.id);
              const isEditing = editingEpic === ep.id;
              const isDeleting = deletingEpic === ep.id;

              return (
                <div key={ep.id} className="rounded-md border p-2">
                  {isEditing ? (
                    <div className="flex flex-col gap-2">
                      <div className="flex gap-2">
                        <input
                          type="color"
                          className="h-10 w-12 cursor-pointer rounded border"
                          value={editEpicColor}
                          onChange={(e) => setEditEpicColor(e.target.value)}
                        />
                        <Input
                          value={editEpicName}
                          onChange={(e) => setEditEpicName(e.target.value)}
                          onKeyDown={(e) => { if (e.key === "Enter") saveEpic(ep.id); if (e.key === "Escape") setEditingEpic(null); }}
                          placeholder="Epic name"
                          autoFocus
                        />
                      </div>
                      <Input
                        value={editEpicDesc}
                        onChange={(e) => setEditEpicDesc(e.target.value)}
                        placeholder="Description (optional)"
                      />
                      <div className="flex gap-2">
                        <Button size="sm" onClick={() => saveEpic(ep.id)}>Save</Button>
                        <Button size="sm" variant="ghost" onClick={() => setEditingEpic(null)}>Cancel</Button>
                      </div>
                    </div>
                  ) : isDeleting ? (
                    <div className="flex flex-col gap-2">
                      <span className="text-sm">
                        Delete "{ep.name}"?
                        {count > 0 && ` ${count} card${count !== 1 ? "s" : ""} will be ungrouped.`}
                      </span>
                      <div className="flex gap-2">
                        <Button size="sm" variant="destructive" onClick={() => deleteEpic(ep.id)}>
                          Confirm delete
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => setDeletingEpic(null)}>
                          Cancel
                        </Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex items-center gap-2">
                      {ep.color && <span className="inline-block h-3 w-3 rounded-full" style={{ backgroundColor: ep.color }} />}
                      <span className="text-sm font-medium">{ep.name}</span>
                      <span className="text-xs text-muted-foreground">({count})</span>
                      {ep.description && <span className="truncate text-xs text-muted-foreground">{ep.description}</span>}
                      <div className="ml-auto flex gap-1">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => {
                            setEditingEpic(ep.id);
                            setEditEpicName(ep.name);
                            setEditEpicDesc(ep.description ?? "");
                            setEditEpicColor(ep.color || "#4A90D9");
                            setDeletingEpic(null);
                          }}
                        >
                          Edit
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          className="text-destructive hover:text-destructive"
                          onClick={() => { setDeletingEpic(ep.id); setEditingEpic(null); }}
                        >
                          Delete
                        </Button>
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>

          <div className="flex gap-2">
            <input
              type="color"
              className="h-10 w-12 cursor-pointer rounded border"
              value={newEpicColor}
              onChange={(e) => setNewEpicColor(e.target.value)}
            />
            <Input
              placeholder="New epic name"
              value={newEpicName}
              onChange={(e) => setNewEpicName(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter") createEpic(); }}
            />
            <Button onClick={createEpic}>Create</Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

// ── Assignee autocomplete ─────────────────────────────────────────────────────

interface AssigneeOption {
  key: string;
  label: string;
  detail: string;
  kind: "user" | "agent";
  userId?: number;
  agentName?: string;
}

interface AssigneeValue {
  userId: number;
  agentName: string;
  display: string;
}

function AssigneeAutocomplete({
  value,
  onChange,
  users,
  agents,
}: {
  value: AssigneeValue;
  onChange: (val: AssigneeValue) => void;
  users: UserDetails[];
  agents: RegistryAlias[];
}) {
  const [query, setQuery] = useState(value.display);
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);

  // Sync external value changes (e.g. panel open with existing assignee)
  useEffect(() => { setQuery(value.display); }, [value.display]);

  const options = useMemo((): AssigneeOption[] => {
    const items: AssigneeOption[] = [];
    for (const u of users) {
      items.push({
        key: `user-${u.id}`,
        label: u.display_name || u.email.split("@")[0],
        detail: u.email,
        kind: "user",
        userId: Number(u.id),
      });
    }
    for (const a of agents) {
      items.push({
        key: `agent-${a.name}`,
        label: `@${a.name}`,
        detail: `${a.type} · ${a.plugin}`,
        kind: "agent",
        agentName: a.name,
      });
    }
    return items;
  }, [users, agents]);

  const filtered = useMemo(() => {
    if (!query.trim()) return options;
    const q = query.toLowerCase();
    return options.filter(
      (o) => o.label.toLowerCase().includes(q) || o.detail.toLowerCase().includes(q)
    );
  }, [query, options]);

  // Close dropdown on outside click
  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (wrapperRef.current && !wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
        if (query !== value.display) setQuery(value.display);
      }
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, [query, value.display]);

  function selectOption(o: AssigneeOption) {
    setQuery(o.label);
    onChange({
      userId: o.userId ?? 0,
      agentName: o.agentName ?? "",
      display: o.label,
    });
    setOpen(false);
  }

  function clearAssignee() {
    setQuery("");
    onChange({ userId: 0, agentName: "", display: "" });
    setOpen(false);
  }

  return (
    <div ref={wrapperRef} className="relative">
      <div className="relative flex items-center">
        <Input
          placeholder="Search users or agents..."
          value={query}
          onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
          onFocus={() => setOpen(true)}
          className={value.display ? "pr-9" : undefined}
        />
        {value.display && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="absolute right-0 h-8 w-8"
            onClick={clearAssignee}
            title="Clear assignee"
          >
            <X className="h-4 w-4" />
          </Button>
        )}
      </div>
      {open && (
        <div className="absolute z-50 mt-1 max-h-64 w-full overflow-y-auto rounded-md border bg-popover p-1 shadow-md">
          {filtered.length === 0 ? (
            <div className="px-3 py-2 text-sm text-muted-foreground">No matches</div>
          ) : (
            filtered.map((o) => (
              <button
                key={o.key}
                className="flex w-full flex-col gap-0.5 rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                onClick={() => selectOption(o)}
                type="button"
              >
                <div className="flex items-center gap-2">
                  <Badge
                    variant={o.kind === "agent" ? "default" : "secondary"}
                    className="text-[10px]"
                  >
                    {o.kind === "agent" ? "AGENT" : "USER"}
                  </Badge>
                  <span className="font-medium">{o.label}</span>
                </div>
                <span className="text-xs text-muted-foreground">{o.detail}</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}

// ── Side panel ────────────────────────────────────────────────────────────────

function formatCommentTime(ms: number): string {
  const d = new Date(ms);
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function SidePanel({
  panel,
  onClose,
  onSubmit,
  onDelete,
  boardPrefix,
}: {
  panel: PanelState;
  onClose: () => void;
  onSubmit: (data: CardFormData) => void;
  onDelete?: (card: CardType) => void;
  boardPrefix?: string;
}) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [cardType, setCardType] = useState("");
  const [priority, setPriority] = useState("");
  const [assignee, setAssignee] = useState<AssigneeValue>({ userId: 0, agentName: "", display: "" });
  const [labels, setLabels] = useState("");
  const [dueDate, setDueDate] = useState("");  // "YYYY-MM-DD" or ""
  const [epicId, setEpicId] = useState("");
  const [comments, setComments] = useState<Comment[]>([]);
  const [newComment, setNewComment] = useState("");
  const [submittingComment, setSubmittingComment] = useState(false);
  const [confirmDeleteComment, setConfirmDeleteComment] = useState<Comment | null>(null);
  const [deletingComment, setDeletingComment] = useState(false);
  const allUsers = useUserStore((s) => s.users);
  const allAgents = useAgentStore((s) => s.aliases);
  const epics = useKanbanStore((s) => s.epics);
  const [savedCountdown, setSavedCountdown] = useState(0);
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    return () => { if (countdownRef.current) clearInterval(countdownRef.current); };
  }, []);

  useEffect(() => {
    if (panel.type === "edit-card") {
      useKanbanStore.getState().listComments(panel.card.id)
        .then(setComments)
        .catch(() => setComments([]));
    } else {
      setComments([]);
    }
  }, [panel]);

  useEffect(() => {
    if (panel.type === "edit-card") {
      const c = panel.card;
      setTitle(c.title);
      setDescription(c.description ?? "");
      setCardType(c.card_type ?? "");
      setPriority(c.priority ?? "");
      setAssignee({
        userId: c.assignee_id ?? 0,
        agentName: c.assignee_agent ?? "",
        display: c.assignee_name ?? "",
      });
      setLabels(c.labels ?? "");
      setDueDate(c.due_date ? new Date(c.due_date).toISOString().slice(0, 10) : "");
      setEpicId(c.epic_id ?? "");
    } else {
      setTitle(""); setDescription(""); setCardType(""); setPriority(""); setAssignee({ userId: 0, agentName: "", display: "" }); setLabels(""); setDueDate(""); setEpicId("");
    }
  }, [panel]);

  const isCard = panel.type === "new-card" || panel.type === "edit-card";
  const isSimple = panel.type === "new-board";

  const heading =
    panel.type === "new-board" ? "New Board" :
    panel.type === "new-card" ? `New Card` :
    panel.card.number > 0 ? `${cardRef(boardPrefix, panel.card.number)} ${panel.card.title}` : panel.card.title;

  function handleSubmit() {
    onSubmit({ title, description, cardType, priority, assigneeId: assignee.userId, assigneeAgent: assignee.agentName, labels, dueDate, epicId });
    if (panel.type === "edit-card") {
      if (countdownRef.current) clearInterval(countdownRef.current);
      setSavedCountdown(5);
      countdownRef.current = setInterval(() => {
        setSavedCountdown((prev) => {
          if (prev <= 1) {
            clearInterval(countdownRef.current!);
            countdownRef.current = null;
            return 0;
          }
          return prev - 1;
        });
      }, 1000);
    }
  }

  async function handleAddComment() {
    if (!newComment.trim() || panel.type !== "edit-card") return;
    setSubmittingComment(true);
    try {
      const created = await useKanbanStore.getState().createComment(panel.card.id, newComment.trim());
      setComments((prev) => [...prev, created]);
      setNewComment("");
    } catch (err) {
      console.error("Failed to post comment:", err);
    } finally {
      setSubmittingComment(false);
    }
  }

  async function handleDeleteComment() {
    if (!confirmDeleteComment || panel.type !== "edit-card") return;
    setDeletingComment(true);
    try {
      await useKanbanStore.getState().deleteComment(panel.card.id, confirmDeleteComment.id);
      setComments((prev) => prev.filter((c) => c.id !== confirmDeleteComment.id));
      setConfirmDeleteComment(null);
    } catch (err) {
      console.error("Failed to delete comment:", err);
    } finally {
      setDeletingComment(false);
    }
  }

  return (
    <aside className="flex w-[440px] shrink-0 flex-col border-l bg-background">
      <div className="flex items-center gap-2 border-b px-4 py-3">
        <span className="flex-1 truncate text-sm font-semibold">{heading}</span>
        <Button variant="ghost" size="icon" onClick={onClose}>
          <X className="h-4 w-4" />
        </Button>
      </div>

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4">

        {/* Title / name */}
        <div className="flex flex-col gap-2">
          <Label>{panel.type === "new-board" ? "Board name" : "Title"}</Label>
          <Input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && isSimple) handleSubmit(); }}
            autoFocus
          />
        </div>

        {/* Description — boards and cards */}
        {(isCard || panel.type === "new-board") && (
          <div className="flex flex-col gap-2">
            <Label>Description</Label>
            <Textarea
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
        )}

        {/* Card-specific fields */}
        {isCard && (<>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-2">
              <Label>Type</Label>
              <Select value={cardType || "__none__"} onValueChange={(v) => setCardType(v === "__none__" ? "" : v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">None</SelectItem>
                  <SelectItem value="task">Task</SelectItem>
                  <SelectItem value="bug">Bug</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Priority</Label>
              <Select value={priority || "__none__"} onValueChange={(v) => setPriority(v === "__none__" ? "" : v)}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">None</SelectItem>
                  <SelectItem value="low">Low</SelectItem>
                  <SelectItem value="medium">Medium</SelectItem>
                  <SelectItem value="high">High</SelectItem>
                  <SelectItem value="urgent">Urgent</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label>Due date</Label>
              <Input
                type="date"
                value={dueDate}
                onChange={(e) => setDueDate(e.target.value)}
              />
            </div>
            {panel.type === "edit-card" && (
              <div className="flex flex-col gap-2">
                <Label>Status</Label>
                <Input
                  value={panel.columnName}
                  readOnly
                  tabIndex={-1}
                  className="opacity-70"
                />
              </div>
            )}
          </div>

          <div className="flex flex-col gap-2">
            <Label>Assignee</Label>
            <AssigneeAutocomplete
              value={assignee}
              onChange={setAssignee}
              users={allUsers}
              agents={allAgents}
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label>Labels</Label>
            <Input
              placeholder="bug, frontend, v2 (comma-separated)"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <Label>Epic</Label>
            <Select value={epicId || "__none__"} onValueChange={(v) => setEpicId(v === "__none__" ? "" : v)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="__none__">No epic</SelectItem>
                {epics.map((ep) => (
                  <SelectItem key={ep.id} value={ep.id}>{ep.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </>)}

        {/* Comments — only shown when editing an existing card */}
        {panel.type === "edit-card" && (
          <div className="flex flex-col gap-3">
            <Separator />
            <div className="text-sm font-semibold">Comments</div>
            {comments.length === 0 ? (
              <div className="text-xs text-muted-foreground">No comments yet.</div>
            ) : (
              <div className="flex flex-col gap-2">
                {comments.map((c) => (
                  <div key={c.id} className="rounded-md border p-2">
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span className="font-medium text-foreground">{c.author_name || "unknown"}</span>
                      <span>{formatCommentTime(c.created_at)}</span>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="ml-auto h-6 w-6"
                        title="Delete comment"
                        onClick={() => setConfirmDeleteComment(c)}
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    </div>
                    <div className="mt-1 whitespace-pre-wrap text-sm">{c.body}</div>
                  </div>
                ))}
              </div>
            )}

            <div className="flex flex-col gap-2">
              <Textarea
                placeholder="Write a comment..."
                rows={3}
                value={newComment}
                onChange={(e) => setNewComment(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && newComment.trim()) {
                    e.preventDefault();
                    handleAddComment();
                  }
                }}
              />
              <div className="flex items-center justify-between">
                <span className="text-xs text-muted-foreground">Ctrl+Enter to submit</span>
                <Button
                  size="sm"
                  disabled={!newComment.trim() || submittingComment}
                  onClick={handleAddComment}
                >
                  {submittingComment ? "Posting..." : "Add Comment"}
                </Button>
              </div>
            </div>
          </div>
        )}

        {confirmDeleteComment && (
          <ConfirmDialog
            title="Delete comment?"
            confirmLabel={deletingComment ? "Deleting..." : "Confirm"}
            cancelLabel="Cancel"
            onConfirm={handleDeleteComment}
            onCancel={() => setConfirmDeleteComment(null)}
            disabled={deletingComment}
          >
            This comment may have been written by someone else. Are you sure you want to delete it?
          </ConfirmDialog>
        )}
      </div>

      <div className="flex items-center gap-2 border-t p-4">
        {panel.type === "edit-card" && onDelete && (
          <Button
            variant="ghost"
            className="text-destructive hover:text-destructive"
            onClick={() => onDelete(panel.card)}
          >
            Delete card
          </Button>
        )}
        <div className="ml-auto flex gap-2">
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSubmit} disabled={savedCountdown > 0}>
            {savedCountdown > 0 ? `Saved (${savedCountdown})` : panel.type === "edit-card" ? "Save" : "Create"}
          </Button>
        </div>
      </div>
    </aside>
  );
}

// ── Main ──────────────────────────────────────────────────────────────────────

function slugify(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
}

/** Format card ref for display: "ARCH-123" or "#123" */
function cardRef(prefix: string | undefined, num: number): string {
  return prefix ? `${prefix}-${num}` : `#${num}`;
}

/** Format card slug for URLs: "ARCH-123" or "123" */
function cardSlug(prefix: string | undefined, num: number): string {
  return prefix ? `${prefix}-${num}` : `${num}`;
}

/** Parse a URL suffix like "ARCH-123" or "123" into a card number, or null */
function parseCardNumber(suffix: string): number | null {
  if (!suffix) return null;
  // Pure number
  if (/^\d+$/.test(suffix)) return parseInt(suffix, 10);
  // PREFIX-123
  const m = suffix.match(/^[A-Za-z]+-(\d+)$/);
  return m ? parseInt(m[1], 10) : null;
}

export default function KanbanBoard({ initialSlug, onBoardChange }: {
  initialSlug?: string;
  onBoardChange?: (slug: string) => void;
}) {
  // Parse slug: "board-slug", "board-slug/manage", or "board-slug/42" (card number)
  const slugParts = initialSlug?.split("/") ?? [];
  const parsedSlug = slugParts[0] || "";
  const slugSuffix = slugParts[1] || "";
  const isManageView = slugSuffix === "manage";
  const initialCardNumber = parseCardNumber(slugSuffix);

  const {
    boards, columns, epics, cards, activeBoardId, loading, error,
    fetchBoards, fetchBoard, setActiveBoard,
    createBoard: storeCreateBoard,
    createCard: storeCreateCard, updateCard: storeUpdateCard, deleteCard: storeDeleteCard,
    setCards, setColumns,
  } = useKanbanStore();
  const userFetch = useUserStore((s) => s.fetch);
  const agentFetch = useAgentStore((s) => s.fetch);

  const [panel, setPanel] = useState<PanelState | null>(null);
  const [activeCard, setActiveCard] = useState<CardType | null>(null);
  const [_panelError, setError] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [countdown, setCountdown] = useState(60);
  const countdownRef = useRef(60);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } })
  );

  const selectedCardId =
    panel?.type === "edit-card" ? panel.card.id : null;

  // ── Load ────────────────────────────────────────────────────────────────────

  useEffect(() => {
    userFetch(); agentFetch();
    fetchBoards()
      .then(async (bs) => {
        if (bs.length === 0) return;
        const match = parsedSlug
          ? bs.find((b) => slugify(b.name) === parsedSlug)
          : null;
        const target = match ?? bs[0];
        setActiveBoard(target.id);
        const suffix = isManageView ? "/manage" : initialCardNumber ? `/${cardSlug(target.prefix, initialCardNumber)}` : "";
        onBoardChange?.(slugify(target.name) + suffix);
        await fetchBoard(target.id);
        // Deep-link: open card by number
        if (initialCardNumber) {
          try {
            const card = await apiClient.tasks.getCardByNumber(target.id, initialCardNumber);
            const col = useKanbanStore.getState().columns.find((c) => c.id === card.column_id);
            setPanel({ type: "edit-card", card, columnName: col?.name ?? "" });
          } catch { /* card not found, ignore */ }
        }
      })
      .catch(() => { useKanbanStore.setState({ loading: false }); });
  }, []);

  useEffect(() => {
    if (activeBoardId) fetchBoard(activeBoardId).catch(() => {});
  }, [activeBoardId]);

  // Auto-refresh columns & cards with countdown
  const refreshBoard = useCallback(() => {
    fetchBoards().catch(() => {});
    if (activeBoardId) {
      fetchBoard(activeBoardId).catch(() => {});
    }
    countdownRef.current = 60;
    setCountdown(60);
  }, [activeBoardId, fetchBoard, fetchBoards]);

  useEffect(() => {
    if (!activeBoardId) return;
    countdownRef.current = 60;
    setCountdown(60);
    const id = setInterval(() => {
      countdownRef.current -= 1;
      setCountdown(countdownRef.current);
      if (countdownRef.current <= 0) {
        fetchBoard(activeBoardId).catch(() => {});
        countdownRef.current = 60;
        setCountdown(60);
      }
    }, 1000);
    return () => clearInterval(id);
  }, [activeBoardId, fetchBoard]);

  // ── Navigation helpers ──────────────────────────────────────────────────────

  const closePanel = useCallback(() => {
    setPanel(null);
    const board = boards.find((b) => b.id === activeBoardId);
    if (board) onBoardChange?.(slugify(board.name));
  }, [boards, activeBoardId, onBoardChange]);

  const navigateToManage = useCallback(() => {
    const board = boards.find((b) => b.id === activeBoardId);
    if (board) onBoardChange?.(slugify(board.name) + "/manage");
  }, [activeBoardId, boards, onBoardChange]);

  const navigateToBoard = useCallback(() => {
    const board = boards.find((b) => b.id === activeBoardId);
    if (board) onBoardChange?.(slugify(board.name));
  }, [activeBoardId, boards, onBoardChange]);

  // ── Drag ────────────────────────────────────────────────────────────────────

  const onDragStart = useCallback((e: DragStartEvent) => {
    if (e.active.data.current?.type === "card") setActiveCard(e.active.data.current.card);
  }, []);

  const onDragOver = useCallback((e: DragOverEvent) => {
    const { active, over } = e;
    if (!over) return;
    const data = active.data.current;
    if (data?.type !== "card") return;

    const draggedCard = data.card as CardType;
    const overId = String(over.id);
    const targetColId: string | null = overId.startsWith("col-")
      ? overId.replace("col-", "")
      : cards.find((c) => c.id === overId)?.column_id ?? null;

    if (targetColId && targetColId !== draggedCard.column_id) {
      setCards((prev) =>
        prev.map((c) => c.id === draggedCard.id ? { ...c, column_id: targetColId } : c)
      );
    }
  }, [cards]);

  const onDragEnd = useCallback(async (e: DragEndEvent) => {
    setActiveCard(null);
    const { active, over } = e;
    if (!over || !activeBoardId) return;
    const data = active.data.current;
    if (data?.type !== "card") return;

    const draggedCard = data.card as CardType;
    const overId = String(over.id);

    let targetColId = draggedCard.column_id;
    if (overId.startsWith("col-")) targetColId = overId.replace("col-", "");
    else {
      const overCard = cards.find((c) => c.id === overId);
      if (overCard) targetColId = overCard.column_id;
    }

    const colCards = cards
      .filter((c) => c.column_id === targetColId && c.id !== draggedCard.id)
      .sort((a, b) => a.position - b.position);

    const overCard = cards.find((c) => c.id === overId);
    const newPosition = overCard && overCard.column_id === targetColId
      ? positionBefore(colCards, colCards.findIndex((c) => c.id === overId))
      : positionAfter(colCards);

    setCards((prev) =>
      prev.map((c) =>
        c.id === draggedCard.id ? { ...c, column_id: targetColId, position: newPosition } : c
      )
    );

    try {
      await storeUpdateCard(activeBoardId, draggedCard.id, {
        column_id: targetColId,
        position: newPosition,
      });
    } catch {
      fetchBoard(activeBoardId);
    }
  }, [cards, activeBoardId, fetchBoard, storeUpdateCard]);

  // ── Panel submit ─────────────────────────────────────────────────────────────

  async function submitPanel(data: CardFormData) {
    if (!panel) return;
    const trimTitle = data.title.trim();
    if (!trimTitle) return;

    const dueDateMs = data.dueDate ? new Date(data.dueDate).getTime() : null;

    try {
      if (panel.type === "new-board") {
        const b = await storeCreateBoard({ name: trimTitle, description: data.description.trim() });
        setActiveBoard(b.id);
        onBoardChange?.(slugify(b.name));
      }

      if (panel.type === "new-card" && activeBoardId) {
        const colCards = cards
          .filter((c) => c.column_id === panel.columnId)
          .sort((a, b) => a.position - b.position);
        await storeCreateCard(activeBoardId, {
          column_id: panel.columnId,
          epic_id: data.epicId || undefined,
          title: trimTitle,
          description: data.description.trim(),
          card_type: data.cardType || undefined,
          priority: data.priority,
          assignee_id: data.assigneeId || undefined,
          assignee_agent: data.assigneeAgent || undefined,
          labels: data.labels.trim(),
          due_date: dueDateMs,
          position: positionAfter(colCards),
        });
      }

      if (panel.type === "edit-card" && activeBoardId) {
        await storeUpdateCard(activeBoardId, panel.card.id, {
          title: trimTitle,
          description: data.description.trim(),
          card_type: data.cardType,
          priority: data.priority,
          assignee_id: data.assigneeId || undefined,
          assignee_agent: data.assigneeAgent || undefined,
          clear_assignee: !data.assigneeId && !data.assigneeAgent,
          labels: data.labels.trim(),
          due_date: dueDateMs ?? undefined,
          clear_due: !data.dueDate,
          epic_id: data.epicId,
          clear_epic: !data.epicId,
        });
      }
    } catch (e) {
      setError(String(e));
      return;
    }

    if (panel.type !== "edit-card") {
      setPanel(null);
    }
  }

  // ── CRUD ────────────────────────────────────────────────────────────────────

  const deleteCard = useCallback(async (card: CardType) => {
    if (!activeBoardId) return;
    await storeDeleteCard(activeBoardId, card.id);
    closePanel();
  }, [activeBoardId, storeDeleteCard, closePanel]);

  const onAddCard = useCallback((c: Column) =>
    setPanel({ type: "new-card", columnId: c.id, columnName: c.name }), []);

  const onEditCard = useCallback((card: CardType, columnName: string) => {
    setPanel({ type: "edit-card", card, columnName });
    const board = boards.find((b) => b.id === activeBoardId);
    if (board && card.number) {
      onBoardChange?.(slugify(board.name) + "/" + cardSlug(board.prefix, card.number));
    }
  }, [boards, activeBoardId, onBoardChange]);

  // ── Render ───────────────────────────────────────────────────────────────────

  const activeBoardPrefix = useMemo(
    () => boards.find((b) => b.id === activeBoardId)?.prefix,
    [boards, activeBoardId]
  );

  // Memoize derived data so KanbanColumn only re-renders when its own cards change
  const sortedColumns = useMemo(
    () => [...columns].sort((a, b) => a.position - b.position),
    [columns]
  );

  const sortedEpics = useMemo(
    () => [...epics].sort((a, b) => a.position - b.position),
    [epics]
  );

  const hasEpics = sortedEpics.length > 0;

  const filteredCards = useMemo(() => {
    if (!searchQuery) return cards;
    const q = searchQuery.toLowerCase();
    return cards.filter(
      (c) => c.title.toLowerCase().includes(q) || c.description.toLowerCase().includes(q)
    );
  }, [cards, searchQuery]);

  // Build swimlane structure: epics + ungrouped
  type Swimlane = { epic: Epic | null; cards: CardType[]; totalCards: number };
  const swimlanes = useMemo((): Swimlane[] => {
    if (!hasEpics) return [{ epic: null, cards: filteredCards, totalCards: cards.length }];
    const lanes: Swimlane[] = sortedEpics.map((ep) => ({
      epic: ep,
      cards: filteredCards.filter((c) => c.epic_id === ep.id),
      totalCards: cards.filter((c) => c.epic_id === ep.id).length,
    }));
    const ungrouped = filteredCards.filter((c) => !c.epic_id);
    const totalUngrouped = cards.filter((c) => !c.epic_id).length;
    if (ungrouped.length > 0 || totalUngrouped > 0 || lanes.length === 0) {
      lanes.push({ epic: null, cards: ungrouped, totalCards: totalUngrouped });
    }
    return lanes;
  }, [filteredCards, cards, sortedEpics, hasEpics]);

  const [collapsedEpics, setCollapsedEpics] = useState<Set<string>>(new Set());

  const toggleEpicCollapse = useCallback((epicId: string) => {
    setCollapsedEpics((prev) => {
      const next = new Set(prev);
      if (next.has(epicId)) next.delete(epicId); else next.add(epicId);
      return next;
    });
  }, []);

  const allEpicsCollapsed = hasEpics && swimlanes.every((l) => {
    const key = l.epic?.id ?? "__ungrouped";
    return collapsedEpics.has(key);
  });

  const toggleAllEpics = useCallback(() => {
    setCollapsedEpics((prev) => {
      const allKeys = swimlanes.map((l) => l.epic?.id ?? "__ungrouped");
      const allCollapsed = allKeys.every((k) => prev.has(k));
      return allCollapsed ? new Set() : new Set(allKeys);
    });
  }, [swimlanes]);

  const cardsByColumn = useMemo(() => {
    const map = new Map<string, CardType[]>();
    for (const card of filteredCards) {
      const list = map.get(card.column_id) ?? [];
      list.push(card);
      map.set(card.column_id, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.position - b.position);
    return map;
  }, [filteredCards]);

  // Per-swimlane cards grouped by column
  const cardsByColumnForLane = useCallback((laneCards: CardType[]) => {
    const map = new Map<string, CardType[]>();
    for (const card of laneCards) {
      const list = map.get(card.column_id) ?? [];
      list.push(card);
      map.set(card.column_id, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.position - b.position);
    return map;
  }, []);

  const columnIds = useMemo(() => sortedColumns.map((c) => `col-${c.id}`), [sortedColumns]);

  const activeBoard = boards.find((b) => b.id === activeBoardId);

  if (loading) return <div className="p-6 text-sm text-muted-foreground">Loading...</div>;
  if (error) return <div className="p-6 text-sm text-destructive">{error}</div>;

  return (
    <div className="flex h-full">

      {/* ── Left sidebar: board list ── */}
      <aside className="flex w-56 shrink-0 flex-col border-r bg-muted/20">
        <div className="px-3 py-2 text-xs font-semibold uppercase text-muted-foreground">BOARDS</div>

        <div className="flex flex-1 flex-col gap-1 overflow-y-auto px-2">
          {boards.map((b) => {
            const isActive = b.id === activeBoardId;
            return (
              <button
                key={b.id}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors hover:bg-accent",
                  isActive && "bg-accent font-medium"
                )}
                onClick={() => { setActiveBoard(b.id); onBoardChange?.(slugify(b.name)); setPanel(null); }}
              >
                <span className="flex-1 truncate">{b.name}</span>
                <span className="text-xs text-muted-foreground">
                  {cards.filter(() => b.id === activeBoardId).length > 0
                    ? cards.length
                    : ""}
                </span>
              </button>
            );
          })}
        </div>

        <div className="border-t p-2">
          <Button
            variant={panel?.type === "new-board" ? "secondary" : "ghost"}
            size="sm"
            className="w-full justify-start"
            onClick={() => setPanel(panel?.type === "new-board" ? null : { type: "new-board" })}
          >
            <Plus className="h-4 w-4" /> New board
          </Button>
        </div>
      </aside>

      {/* ── Main board area ── */}
      <div className="flex flex-1 flex-col overflow-hidden">

        {/* Board toolbar */}
        {activeBoard && (
          <div className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
            <div className="flex min-w-0 flex-col">
              <h2 className="truncate text-lg font-semibold">{activeBoard.name}</h2>
              {activeBoard.description && (
                <span className="truncate text-xs text-muted-foreground">{activeBoard.description}</span>
              )}
            </div>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              {searchQuery && (
                <span className="text-xs text-muted-foreground">
                  {filteredCards.length} result{filteredCards.length !== 1 ? "s" : ""} found
                </span>
              )}
              <Input
                type="text"
                className="w-48"
                placeholder="Search cards..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
              <span className="text-xs text-muted-foreground">
                {sortedColumns.length} columns · {searchQuery ? `${filteredCards.length}/` : ""}{cards.length} cards
              </span>
              {hasEpics && (
                <Button variant="outline" size="sm" onClick={toggleAllEpics}>
                  {allEpicsCollapsed ? "Expand All" : "Collapse All"}
                </Button>
              )}
              <Button variant="outline" size="sm" onClick={refreshBoard}>
                <span className="text-muted-foreground">({countdown}s)</span> Refresh
              </Button>
              <Button
                variant={isManageView ? "secondary" : "outline"}
                size="sm"
                onClick={() => isManageView ? navigateToBoard() : navigateToManage()}
              >
                Manage
              </Button>
            </div>
          </div>
        )}

        {/* Manage view */}
        <div className="flex-1 overflow-auto">
        {isManageView && activeBoard && activeBoardId ? (
          <BoardManager
            columns={columns}
            epics={epics}
            cards={cards}
            boardId={activeBoardId}
            onBack={navigateToBoard}
            onColumnsChange={(cols) => setColumns(() => cols)}
            onCardsChange={(crds) => setCards(() => crds)}
            onEpicsChange={(eps) => useKanbanStore.setState({ epics: eps })}
          />
        ) : !activeBoard ? (
          <div className="flex flex-col items-center justify-center gap-3 p-12 text-center">
            <p className="text-sm text-muted-foreground">No boards yet.</p>
            <Button onClick={() => setPanel({ type: "new-board" })}>
              Create your first board
            </Button>
          </div>
        ) : (
          <DndContext
            sensors={sensors}
            collisionDetection={closestCorners}
            measuring={measuring}
            onDragStart={onDragStart}
            onDragOver={onDragOver}
            onDragEnd={onDragEnd}
          >
            {hasEpics ? (
              <div className="flex flex-col gap-3 p-4">
                {swimlanes.map((lane) => {
                  const laneKey = lane.epic?.id ?? "__ungrouped";
                  const collapsed = collapsedEpics.has(laneKey);
                  const laneCardsByCol = cardsByColumnForLane(lane.cards);
                  const doneColIds = new Set(columns.filter((c) => c.name.toLowerCase() === "done").map((c) => c.id));
                  const allLaneCards = cards.filter((c) => lane.epic ? c.epic_id === lane.epic.id : !c.epic_id);
                  const doneCount = allLaneCards.filter((c) => doneColIds.has(c.column_id)).length;
                  const totalCount = lane.totalCards;
                  return (
                    <div key={laneKey} className="flex flex-col gap-2">
                      <div
                        className="flex cursor-pointer items-center gap-2 rounded-md border-l-4 border-l-transparent bg-muted/40 px-3 py-2"
                        style={lane.epic?.color ? { borderLeftColor: lane.epic.color } : undefined}
                        onClick={() => toggleEpicCollapse(laneKey)}
                      >
                        <ChevronDown className={cn("h-4 w-4 transition-transform", collapsed && "-rotate-90")} />
                        {lane.epic?.color && (
                          <span className="inline-block h-3 w-3 rounded-full" style={{ backgroundColor: lane.epic.color }} />
                        )}
                        <span className="text-sm font-semibold">
                          {lane.epic?.name ?? "Ungrouped"}
                        </span>
                        <span className="text-xs text-muted-foreground">({doneCount}/{totalCount})</span>
                        {lane.epic?.description && (
                          <span className="truncate text-xs text-muted-foreground">{lane.epic.description}</span>
                        )}
                        <div className="ml-auto w-32">
                          <ProgressBar done={doneCount} total={totalCount} />
                        </div>
                      </div>
                      {!collapsed && lane.totalCards > 0 && lane.cards.length > 0 && (
                        <SortableContext items={columnIds} strategy={verticalListSortingStrategy}>
                          <div className="flex items-start gap-3 overflow-x-auto pb-2">
                            {sortedColumns.map((col) => (
                              <KanbanColumn
                                key={col.id}
                                column={col}
                                cards={laneCardsByCol.get(col.id) ?? []}
                                selectedCardId={selectedCardId}
                                onAddCard={onAddCard}
                                onEditCard={onEditCard}
                                boardPrefix={activeBoardPrefix}
                              />
                            ))}
                          </div>
                        </SortableContext>
                      )}
                    </div>
                  );
                })}
              </div>
            ) : (
              <SortableContext items={columnIds} strategy={verticalListSortingStrategy}>
                <div className="flex items-start gap-3 overflow-x-auto p-4">
                  {sortedColumns.map((col) => (
                    <KanbanColumn
                      key={col.id}
                      column={col}
                      cards={cardsByColumn.get(col.id) ?? []}
                      selectedCardId={selectedCardId}
                      onAddCard={onAddCard}
                      onEditCard={onEditCard}
                      boardPrefix={activeBoardPrefix}
                    />
                  ))}
                  {sortedColumns.length === 0 && (
                    <div className="flex w-full items-center justify-center p-8 text-sm text-muted-foreground">
                      No columns yet — add one to get started.
                    </div>
                  )}
                </div>
              </SortableContext>
            )}

            <DragOverlay>
              {activeCard && (
                <Card className="flex w-72 cursor-grabbing flex-col gap-2 p-3 opacity-90 shadow-lg">
                  <span className="text-sm font-medium">{activeCard.title}</span>
                </Card>
              )}
            </DragOverlay>
          </DndContext>
        )}
        </div>
      </div>

      {/* ── Right panel: forms ── */}
      {panel && (
        <SidePanel
          panel={panel}
          onClose={closePanel}
          onSubmit={submitPanel}
          onDelete={deleteCard}
          boardPrefix={activeBoardPrefix}
        />
      )}
    </div>
  );
}
