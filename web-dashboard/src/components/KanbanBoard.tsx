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
import type { Column, Epic, Card, Comment, UserDetails, RegistryAlias } from "@teamagentica/api-client";
import ConfirmDialog from "./ConfirmDialog";


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
  | { type: "edit-card"; card: Card; columnName: string };

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
const CARD_TYPE_CLASS: Record<string, string> = {
  task: "kn-type--task", bug: "kn-type--bug",
};

const PRIORITY_LABEL: Record<string, string> = {
  low: "Low", medium: "Medium", high: "High", urgent: "Urgent",
};
const PRIORITY_CLASS: Record<string, string> = {
  low: "kn-priority--low", medium: "kn-priority--medium",
  high: "kn-priority--high", urgent: "kn-priority--urgent",
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
  card: Card;
  isSelected: boolean;
  onEdit: (card: Card) => void;
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
      className={`kn-card ${isSelected ? "kn-card--selected" : ""}`}
      {...attributes}
      {...listeners}
    >
      {/* Top meta row: type + priority + assignee + card number */}
      {(card.card_type || card.priority || card.assignee_name || card.number > 0) && (
        <div className="kn-card-meta">
          {card.card_type && (
            <span className={`kn-card-type ${CARD_TYPE_CLASS[card.card_type] ?? ""}`}>
              {CARD_TYPE_LABEL[card.card_type] ?? card.card_type}
            </span>
          )}
          {card.priority && (
            <span className={`kn-priority ${PRIORITY_CLASS[card.priority] ?? ""}`}>
              {PRIORITY_LABEL[card.priority]}
            </span>
          )}
          {card.assignee_name && (
            <span className="kn-card-assignee">{card.assignee_name}</span>
          )}
          {card.number > 0 && (
            <span className="kn-card-ref">{cardRef(boardPrefix, card.number)}</span>
          )}
        </div>
      )}

      <span className="kn-card-title">
        {card.title}
      </span>

      {card.description && (
        <span className="kn-card-desc">{card.description}</span>
      )}

      {/* Labels */}
      {labels.length > 0 && (
        <div className="kn-card-labels">
          {labels.map(l => <span key={l} className="kn-label-pill">{l}</span>)}
        </div>
      )}

      {/* Due date */}
      {card.due_date && (
        <span className={`kn-card-due ${overdue ? "kn-card-due--overdue" : ""}`}>
          {overdue ? "⚠ " : ""}Due {formatDue(card.due_date)}
        </span>
      )}

      <div className="kn-card-actions">
        <button
          className="kn-btn kn-btn--ghost"
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => { e.stopPropagation(); onEdit(card); }}
        >
          Open
        </button>
      </div>
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
  cards: Card[];
  selectedCardId: string | null;
  onAddCard: (column: Column) => void;
  onEditCard: (card: Card, columnName: string) => void;
  boardPrefix?: string;
}) {
  const { setNodeRef } = useSortable({
    id: `col-${column.id}`,
    data: { type: "column", column },
  });

  return (
    <div ref={setNodeRef} className="kn-column">
      <div className="kn-column-header">
        <span className="kn-column-name">{column.name}</span>
        <span className="kn-column-count">({cards.length})</span>
      </div>

      <SortableContext items={cards.map((c) => c.id)} strategy={verticalListSortingStrategy}>
        <div className="kn-cards">
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
            <div className="kn-column-empty">No cards</div>
          )}
        </div>
      </SortableContext>

      <button className="kn-add-card-btn" onClick={() => onAddCard(column)}>
        + Add card
      </button>
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
    <div className="kn-board-settings">
      <h4 className="kn-manager-section-title" style={{ borderTop: "none", marginTop: 0, paddingTop: 0 }}>Board Settings</h4>
      {error && <div className="kn-col-manager-error">{error}</div>}
      <div className="kn-board-settings-field">
        <label className="kn-label">Board Name</label>
        <input
          className="kn-input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") save(); }}
          placeholder="Board name"
        />
      </div>
      <div className="kn-board-settings-field">
        <label className="kn-label">Prefix</label>
        <input
          className="kn-input"
          value={prefix}
          onChange={(e) => setPrefix(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ""))}
          onKeyDown={(e) => { if (e.key === "Enter") save(); }}
          placeholder="e.g. INFRA"
          maxLength={10}
        />
        <span className="kn-board-settings-hint">
          {prefix ? `Cards will be referenced as ${prefix}-123` : "Used to reference cards like INFRA-123"}
        </span>
      </div>
      <div className="kn-board-settings-actions">
        <button className="kn-btn kn-btn--primary" onClick={save} disabled={!dirty || saving}>
          {saving ? "Saving..." : saved ? "Saved" : "Save"}
        </button>
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
  cards: Card[];
  boardId: string;
  onBack: () => void;
  onColumnsChange: (cols: Column[]) => void;
  onCardsChange: (cards: Card[]) => void;
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
    <div className="kn-col-manager">
      <div className="kn-col-manager-header">
        <button className="kn-btn kn-btn--ghost" onClick={onBack}>Back to board</button>
        <h3 className="kn-col-manager-title">Manage Board</h3>
      </div>

      {error && <div className="kn-col-manager-error">{error}</div>}

      <div className="kn-manager-panels">
      <div className="kn-manager-panel-left">

      {/* ── Columns Section ── */}
      <h4 className="kn-manager-section-title" style={{ borderTop: "none", marginTop: 0, paddingTop: 0 }}>Columns</h4>

      <div className="kn-col-manager-list">
        {sortedCols.map((col) => {
          const count = colCardCount(col.id);
          const isRenaming = renamingCol === col.id;
          const isDeleting = deletingCol === col.id;

          return (
            <div key={col.id} className="kn-col-manager-row">
              {isRenaming ? (
                <div className="kn-col-manager-rename">
                  <input
                    className="kn-input"
                    value={renameColValue}
                    onChange={(e) => setRenameColValue(e.target.value)}
                    onKeyDown={(e) => { if (e.key === "Enter") renameColumn(col.id); if (e.key === "Escape") setRenamingCol(null); }}
                    autoFocus
                  />
                  <button className="kn-btn kn-btn--primary" onClick={() => renameColumn(col.id)}>Save</button>
                  <button className="kn-btn kn-btn--ghost" onClick={() => setRenamingCol(null)}>Cancel</button>
                </div>
              ) : isDeleting ? (
                <div className="kn-col-manager-delete">
                  <span className="kn-col-manager-delete-label">
                    Delete "{col.name}"?
                    {count > 0 && ` Move ${count} card${count !== 1 ? "s" : ""} to:`}
                  </span>
                  {count > 0 && (
                    <select
                      className="kn-input kn-select"
                      value={reassignTarget}
                      onChange={(e) => setReassignTarget(e.target.value)}
                    >
                      <option value="">Select column...</option>
                      {sortedCols.filter((c) => c.id !== col.id).map((c) => (
                        <option key={c.id} value={c.id}>{c.name}</option>
                      ))}
                    </select>
                  )}
                  <div className="kn-col-manager-delete-actions">
                    <button
                      className="kn-btn kn-btn--danger"
                      disabled={count > 0 && !reassignTarget}
                      onClick={() => deleteColumn(col.id)}
                    >
                      Confirm delete
                    </button>
                    <button className="kn-btn kn-btn--ghost" onClick={() => { setDeletingCol(null); setReassignTarget(""); }}>
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <>
                  <span className="kn-col-manager-name">{col.name}</span>
                  <span className="kn-col-manager-count">({count})</span>
                  <div className="kn-col-manager-actions">
                    <button
                      className="kn-btn kn-btn--ghost"
                      onClick={() => { setRenamingCol(col.id); setRenameColValue(col.name); setDeletingCol(null); }}
                    >
                      Rename
                    </button>
                    <button
                      className="kn-btn kn-btn--ghost kn-btn--danger"
                      onClick={() => { setDeletingCol(col.id); setRenamingCol(null); setReassignTarget(""); }}
                    >
                      Delete
                    </button>
                  </div>
                </>
              )}
            </div>
          );
        })}
      </div>

      <div className="kn-col-manager-create">
        <input
          className="kn-input"
          placeholder="New column name"
          value={newColName}
          onChange={(e) => setNewColName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") createColumn(); }}
        />
        <button className="kn-btn kn-btn--primary" onClick={createColumn}>Create</button>
      </div>
      </div>{/* end panel-left */}

      <div className="kn-manager-panel-right">
        <BoardSettingsForm boardId={boardId} />
      </div>
      </div>{/* end panels */}

      {/* ── Epics Section ── */}
      <h4 className="kn-manager-section-title">Epics</h4>

      <div className="kn-col-manager-list">
        {sortedEpics.length === 0 && (
          <div className="kn-col-manager-row">
            <span className="kn-col-manager-name" style={{ color: "var(--text-muted)" }}>No epics yet</span>
          </div>
        )}
        {sortedEpics.map((ep) => {
          const count = epicCardCount(ep.id);
          const isEditing = editingEpic === ep.id;
          const isDeleting = deletingEpic === ep.id;

          return (
            <div key={ep.id} className="kn-col-manager-row">
              {isEditing ? (
                <div className="kn-epic-edit">
                  <div className="kn-epic-edit-row">
                    <input
                      type="color"
                      className="kn-epic-color-input"
                      value={editEpicColor}
                      onChange={(e) => setEditEpicColor(e.target.value)}
                    />
                    <input
                      className="kn-input"
                      value={editEpicName}
                      onChange={(e) => setEditEpicName(e.target.value)}
                      onKeyDown={(e) => { if (e.key === "Enter") saveEpic(ep.id); if (e.key === "Escape") setEditingEpic(null); }}
                      placeholder="Epic name"
                      autoFocus
                    />
                  </div>
                  <input
                    className="kn-input"
                    value={editEpicDesc}
                    onChange={(e) => setEditEpicDesc(e.target.value)}
                    placeholder="Description (optional)"
                  />
                  <div className="kn-col-manager-delete-actions">
                    <button className="kn-btn kn-btn--primary" onClick={() => saveEpic(ep.id)}>Save</button>
                    <button className="kn-btn kn-btn--ghost" onClick={() => setEditingEpic(null)}>Cancel</button>
                  </div>
                </div>
              ) : isDeleting ? (
                <div className="kn-col-manager-delete">
                  <span className="kn-col-manager-delete-label">
                    Delete "{ep.name}"?
                    {count > 0 && ` ${count} card${count !== 1 ? "s" : ""} will be ungrouped.`}
                  </span>
                  <div className="kn-col-manager-delete-actions">
                    <button className="kn-btn kn-btn--danger" onClick={() => deleteEpic(ep.id)}>
                      Confirm delete
                    </button>
                    <button className="kn-btn kn-btn--ghost" onClick={() => setDeletingEpic(null)}>
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <>
                  {ep.color && <span className="kn-swimlane-dot" style={{ backgroundColor: ep.color }} />}
                  <span className="kn-col-manager-name">{ep.name}</span>
                  <span className="kn-col-manager-count">({count})</span>
                  {ep.description && <span className="kn-epic-desc-preview">{ep.description}</span>}
                  <div className="kn-col-manager-actions">
                    <button
                      className="kn-btn kn-btn--ghost"
                      onClick={() => {
                        setEditingEpic(ep.id);
                        setEditEpicName(ep.name);
                        setEditEpicDesc(ep.description ?? "");
                        setEditEpicColor(ep.color || "#4A90D9");
                        setDeletingEpic(null);
                      }}
                    >
                      Edit
                    </button>
                    <button
                      className="kn-btn kn-btn--ghost kn-btn--danger"
                      onClick={() => { setDeletingEpic(ep.id); setEditingEpic(null); }}
                    >
                      Delete
                    </button>
                  </div>
                </>
              )}
            </div>
          );
        })}
      </div>

      <div className="kn-col-manager-create">
        <input
          type="color"
          className="kn-epic-color-input"
          value={newEpicColor}
          onChange={(e) => setNewEpicColor(e.target.value)}
        />
        <input
          className="kn-input"
          placeholder="New epic name"
          value={newEpicName}
          onChange={(e) => setNewEpicName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") createEpic(); }}
        />
        <button className="kn-btn kn-btn--primary" onClick={createEpic}>Create</button>
      </div>
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
    <div ref={wrapperRef} className="kn-assignee-autocomplete">
      <div className="kn-assignee-input-wrap">
        <input
          className="kn-input"
          placeholder="Search users or agents..."
          value={query}
          onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
          onFocus={() => setOpen(true)}
        />
        {value.display && (
          <button
            className="kn-assignee-clear"
            onClick={clearAssignee}
            type="button"
            title="Clear assignee"
          >
            ✕
          </button>
        )}
      </div>
      {open && (
        <div className="kn-assignee-dropdown">
          {filtered.length === 0 ? (
            <div className="kn-assignee-dropdown-empty">No matches</div>
          ) : (
            filtered.map((o) => (
              <button
                key={o.key}
                className="kn-assignee-option"
                onClick={() => selectOption(o)}
                type="button"
              >
                <div className="kn-assignee-option-row">
                  <span className={`kn-assignee-kind kn-assignee-kind--${o.kind}`}>
                    {o.kind === "agent" ? "AGENT" : "USER"}
                  </span>
                  <span className="kn-assignee-option-name">{o.label}</span>
                </div>
                <span className="kn-assignee-option-email">{o.detail}</span>
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
  onDelete?: (card: Card) => void;
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
    <aside className="kn-panel">
      <div className="kn-panel-header">
        <span className="kn-panel-title">{heading}</span>
        <button className="kn-panel-close" onClick={onClose}>✕</button>
      </div>

      <div className="kn-panel-body">

        {/* Title / name */}
        <div className="kn-field">
          <label className="kn-label">
            {panel.type === "new-board" ? "Board name" : "Title"}
          </label>
          <input
            className="kn-input"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => { if (e.key === "Enter" && isSimple) handleSubmit(); }}
            autoFocus
          />
        </div>

        {/* Description — boards and cards */}
        {(isCard || panel.type === "new-board") && (
          <div className="kn-field kn-field--grow">
            <label className="kn-label">Description</label>
            <textarea
              className="kn-input kn-textarea"
              rows={3}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
            />
          </div>
        )}

        {/* Card-specific fields */}
        {isCard && (<>
          <div className="kn-field-row">
            <div className="kn-field">
              <label className="kn-label">Type</label>
              <select className="kn-input kn-select" value={cardType} onChange={(e) => setCardType(e.target.value)}>
                <option value="">None</option>
                <option value="task">Task</option>
                <option value="bug">Bug</option>
              </select>
            </div>
            <div className="kn-field">
              <label className="kn-label">Priority</label>
              <select className="kn-input kn-select" value={priority} onChange={(e) => setPriority(e.target.value)}>
                <option value="">None</option>
                <option value="low">Low</option>
                <option value="medium">Medium</option>
                <option value="high">High</option>
                <option value="urgent">Urgent</option>
              </select>
            </div>
            <div className="kn-field">
              <label className="kn-label">Due date</label>
              <input
                type="date"
                className="kn-input"
                value={dueDate}
                onChange={(e) => setDueDate(e.target.value)}
              />
            </div>
            {panel.type === "edit-card" && (
              <div className="kn-field">
                <label className="kn-label">Status</label>
                <input
                  className="kn-input"
                  value={panel.columnName}
                  readOnly
                  tabIndex={-1}
                  style={{ opacity: 0.7, cursor: "default" }}
                />
              </div>
            )}
          </div>

          <div className="kn-field">
            <label className="kn-label">Assignee</label>
            <AssigneeAutocomplete
              value={assignee}
              onChange={setAssignee}
              users={allUsers}
              agents={allAgents}
            />
          </div>

          <div className="kn-field">
            <label className="kn-label">Labels</label>
            <input
              className="kn-input"
              placeholder="bug, frontend, v2 (comma-separated)"
              value={labels}
              onChange={(e) => setLabels(e.target.value)}
            />
          </div>

          <div className="kn-field">
            <label className="kn-label">Epic</label>
            <select className="kn-input kn-select" value={epicId} onChange={(e) => setEpicId(e.target.value)}>
              <option value="">No epic</option>
              {epics.map((ep) => (
                <option key={ep.id} value={ep.id}>
                  {ep.name}
                </option>
              ))}
            </select>
          </div>
        </>)}

        {/* Comments — only shown when editing an existing card */}
        {panel.type === "edit-card" && (
          <div className="kn-comments">
            <div className="kn-comments-heading">Comments</div>
            {comments.length === 0 ? (
              <div className="kn-comments-empty">No comments yet.</div>
            ) : (
              <div className="kn-comments-list">
                {comments.map((c) => (
                  <div key={c.id} className="kn-comment">
                    <div className="kn-comment-meta">
                      <span className="kn-comment-author">{c.author_name || "unknown"}</span>
                      <span className="kn-comment-time">{formatCommentTime(c.created_at)}</span>
                      <button
                        className="kn-comment-delete"
                        title="Delete comment"
                        onClick={() => setConfirmDeleteComment(c)}
                      >🗑</button>
                    </div>
                    <div className="kn-comment-body">{c.body}</div>
                  </div>
                ))}
              </div>
            )}

            <div className="kn-comment-editor">
              <textarea
                className="kn-input kn-comment-textarea"
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
              <div className="kn-comment-editor-actions">
                <span className="kn-comment-hint">Ctrl+Enter to submit</span>
                <button
                  className="kn-btn kn-btn--primary kn-btn--sm"
                  disabled={!newComment.trim() || submittingComment}
                  onClick={handleAddComment}
                >
                  {submittingComment ? "Posting..." : "Add Comment"}
                </button>
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

        <div className="kn-panel-footer">
          {panel.type === "edit-card" && onDelete && (
            <button
              className="kn-btn kn-btn--ghost kn-btn--danger"
              onClick={() => onDelete(panel.card)}
            >
              Delete card
            </button>
          )}
          <div className="kn-panel-actions">
            <button className="kn-btn kn-btn--ghost" onClick={onClose}>Cancel</button>
            <button className="kn-btn kn-btn--primary" onClick={handleSubmit} disabled={savedCountdown > 0}>
              {savedCountdown > 0 ? `✅ Saved (${savedCountdown})` : panel.type === "edit-card" ? "Save" : "Create"}
            </button>
          </div>
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
  const [activeCard, setActiveCard] = useState<Card | null>(null);
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

    const draggedCard = data.card as Card;
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

    const draggedCard = data.card as Card;
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

  const deleteCard = useCallback(async (card: Card) => {
    if (!activeBoardId) return;
    await storeDeleteCard(activeBoardId, card.id);
    closePanel();
  }, [activeBoardId, storeDeleteCard, closePanel]);

  const onAddCard = useCallback((c: Column) =>
    setPanel({ type: "new-card", columnId: c.id, columnName: c.name }), []);

  const onEditCard = useCallback((card: Card, columnName: string) => {
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
  type Swimlane = { epic: Epic | null; cards: Card[]; totalCards: number };
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
    const map = new Map<string, Card[]>();
    for (const card of filteredCards) {
      const list = map.get(card.column_id) ?? [];
      list.push(card);
      map.set(card.column_id, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.position - b.position);
    return map;
  }, [filteredCards]);

  // Per-swimlane cards grouped by column
  const cardsByColumnForLane = useCallback((laneCards: Card[]) => {
    const map = new Map<string, Card[]>();
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

  if (loading) return <div className="kn-loading">Loading...</div>;
  if (error) return <div className="kn-loading kn-error">{error}</div>;

  return (
    <div className="kn-root">

      {/* ── Left sidebar: board list ── */}
      <aside className="kn-sidebar">
        <div className="kn-sidebar-header">BOARDS</div>

        <div className="kn-sidebar-list">
          {boards.map((b) => (
            <button
              key={b.id}
              className={`kn-sidebar-item ${b.id === activeBoardId ? "kn-sidebar-item--active" : ""}`}
              onClick={() => { setActiveBoard(b.id); onBoardChange?.(slugify(b.name)); setPanel(null); }}
            >
              <span className="kn-sidebar-item-name">{b.name}</span>
              <span className="kn-sidebar-item-count">
                {cards.filter(() => b.id === activeBoardId).length > 0
                  ? cards.length
                  : ""}
              </span>
            </button>
          ))}
        </div>

        <div className="kn-sidebar-footer">
          <button
            className={`kn-sidebar-new ${panel?.type === "new-board" ? "kn-sidebar-new--active" : ""}`}
            onClick={() => setPanel(panel?.type === "new-board" ? null : { type: "new-board" })}
          >
            + New board
          </button>
        </div>
      </aside>

      {/* ── Main board area ── */}
      <div className="kn-main">

        {/* Board toolbar */}
        {activeBoard && (
          <div className="kn-board-toolbar">
            <div className="kn-board-title-row">
              <h2 className="kn-board-name">{activeBoard.name}</h2>
              {activeBoard.description && (
                <span className="kn-board-desc">{activeBoard.description}</span>
              )}
            </div>
            <div className="kn-board-actions">
              {searchQuery && (
                <span className="kn-search-results">{filteredCards.length} result{filteredCards.length !== 1 ? "s" : ""} found</span>
              )}
              <input
                type="text"
                className="kn-search-input"
                placeholder="Search cards..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
              />
              <span className="kn-board-meta">
                {sortedColumns.length} columns · {searchQuery ? `${filteredCards.length}/` : ""}{cards.length} cards
              </span>
              {hasEpics && (
                <button className="kn-btn kn-btn--outline" onClick={toggleAllEpics}>
                  {allEpicsCollapsed ? "Expand All" : "Collapse All"}
                </button>
              )}
              <button className="kn-btn kn-btn--outline kn-refresh-btn" onClick={refreshBoard}>
                <span className="kn-refresh-countdown">({countdown}s)</span> Refresh
              </button>
              <button
                className={`kn-btn kn-btn--outline ${isManageView ? "kn-btn--active" : ""}`}
                onClick={() => isManageView ? navigateToBoard() : navigateToManage()}
              >
                Manage
              </button>
            </div>
          </div>
        )}

        {/* Manage view */}
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
          <div className="kn-empty">
            <p>No boards yet.</p>
            <button
              className="kn-btn kn-btn--primary"
              onClick={() => setPanel({ type: "new-board" })}
            >
              Create your first board
            </button>
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
              <div className="kn-swimlanes">
                {swimlanes.map((lane) => {
                  const laneKey = lane.epic?.id ?? "__ungrouped";
                  const collapsed = collapsedEpics.has(laneKey);
                  const laneCardsByCol = cardsByColumnForLane(lane.cards);
                  const doneColIds = new Set(columns.filter((c) => c.name.toLowerCase() === "done").map((c) => c.id));
                  const allLaneCards = cards.filter((c) => lane.epic ? c.epic_id === lane.epic.id : !c.epic_id);
                  const doneCount = allLaneCards.filter((c) => doneColIds.has(c.column_id)).length;
                  const totalCount = lane.totalCards;
                  return (
                    <div key={laneKey} className="kn-swimlane">
                      <div
                        className="kn-swimlane-header"
                        style={lane.epic?.color ? { borderLeftColor: lane.epic.color } : undefined}
                        onClick={() => toggleEpicCollapse(laneKey)}
                      >
                        <span className={`kn-swimlane-toggle ${collapsed ? "kn-swimlane-toggle--collapsed" : ""}`}>
                          ▾
                        </span>
                        {lane.epic?.color && (
                          <span className="kn-swimlane-dot" style={{ backgroundColor: lane.epic.color }} />
                        )}
                        <span className="kn-swimlane-name">
                          {lane.epic?.name ?? "Ungrouped"}
                        </span>
                        <span className="kn-swimlane-count">({doneCount}/{totalCount})</span>
                        {lane.epic?.description && (
                          <span className="kn-swimlane-desc">{lane.epic.description}</span>
                        )}
                        <div className="kn-swimlane-progress-wrap">
                          <ProgressBar done={doneCount} total={totalCount} />
                        </div>
                      </div>
                      {!collapsed && lane.totalCards > 0 && lane.cards.length > 0 && (
                        <SortableContext items={columnIds} strategy={verticalListSortingStrategy}>
                          <div className="kn-board kn-board--swimlane">
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
                <div className="kn-board">
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
                    <div className="kn-board-empty">
                      No columns yet — add one to get started.
                    </div>
                  )}
                </div>
              </SortableContext>
            )}

            <DragOverlay>
              {activeCard && (
                <div className="kn-card kn-card--dragging">
                  <span className="kn-card-title">{activeCard.title}</span>
                </div>
              )}
            </DragOverlay>
          </DndContext>
        )}
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
