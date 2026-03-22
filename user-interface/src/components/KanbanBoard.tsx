import { useState, useEffect, useCallback, useMemo, useRef, memo } from "react";
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
import { apiClient } from "../api/client";
import type { Board, Column, Card, Comment, UserDetails, RegistryAlias } from "@teamagentica/api-client";


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
  priority: string;
  assigneeId: number;
  assigneeAgent: string;
  labels: string;
  dueDate: string; // "YYYY-MM-DD" or ""
}

// ── Priority helpers ──────────────────────────────────────────────────────────

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
}: {
  card: Card;
  isSelected: boolean;
  onEdit: (card: Card) => void;
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
      {/* Top meta row: priority + assignee */}
      {(card.priority || card.assignee_name) && (
        <div className="kn-card-meta">
          {card.priority && (
            <span className={`kn-priority ${PRIORITY_CLASS[card.priority] ?? ""}`}>
              {PRIORITY_LABEL[card.priority]}
            </span>
          )}
          {card.assignee_name && (
            <span className="kn-card-assignee">{card.assignee_name}</span>
          )}
        </div>
      )}

      <span className="kn-card-title">{card.title}</span>

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
}: {
  column: Column;
  cards: Card[];
  selectedCardId: string | null;
  onAddCard: (column: Column) => void;
  onEditCard: (card: Card, columnName: string) => void;
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

// ── Column Manager ────────────────────────────────────────────────────────────

function ColumnManager({
  columns,
  cards,
  boardId,
  onBack,
  onColumnsChange,
  onCardsChange,
}: {
  columns: Column[];
  cards: Card[];
  boardId: string;
  onBack: () => void;
  onColumnsChange: (cols: Column[]) => void;
  onCardsChange: (cards: Card[]) => void;
}) {
  const [newName, setNewName] = useState("");
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [deleting, setDeleting] = useState<string | null>(null);
  const [reassignTarget, setReassignTarget] = useState<string>("");
  const [error, setError] = useState<string | null>(null);

  const sorted = useMemo(
    () => [...columns].sort((a, b) => a.position - b.position),
    [columns]
  );

  function cardCount(colId: string): number {
    return cards.filter((c) => c.column_id === colId).length;
  }

  async function createColumn() {
    const name = newName.trim();
    if (!name) return;
    try {
      const pos = positionAfter(sorted);
      const col = await apiClient.tasks.createColumn(boardId, { name, position: pos });
      onColumnsChange([...columns, col]);
      setNewName("");
    } catch (e) {
      setError(String(e));
    }
  }

  async function renameColumn(colId: string) {
    const name = renameValue.trim();
    if (!name) return;
    try {
      const updated = await apiClient.tasks.updateColumn(boardId, colId, { name });
      onColumnsChange(columns.map((c) => (c.id === updated.id ? updated : c)));
      setRenaming(null);
    } catch (e) {
      setError(String(e));
    }
  }

  async function deleteColumn(colId: string) {
    const count = cardCount(colId);
    if (count > 0 && !reassignTarget) return;

    try {
      // Reassign cards first if needed
      if (count > 0 && reassignTarget) {
        const colCards = cards.filter((c) => c.column_id === colId);
        for (const card of colCards) {
          await apiClient.tasks.updateCard(boardId, card.id, { column_id: reassignTarget });
        }
        onCardsChange(
          cards.map((c) => c.column_id === colId ? { ...c, column_id: reassignTarget } : c)
        );
      }

      await apiClient.tasks.deleteColumn(boardId, colId);
      onColumnsChange(columns.filter((c) => c.id !== colId));
      setDeleting(null);
      setReassignTarget("");
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <div className="kn-col-manager">
      <div className="kn-col-manager-header">
        <button className="kn-btn kn-btn--ghost" onClick={onBack}>Back to board</button>
        <h3 className="kn-col-manager-title">Manage Columns</h3>
      </div>

      {error && <div className="kn-col-manager-error">{error}</div>}

      <div className="kn-col-manager-list">
        {sorted.map((col) => {
          const count = cardCount(col.id);
          const isRenaming = renaming === col.id;
          const isDeleting = deleting === col.id;

          return (
            <div key={col.id} className="kn-col-manager-row">
              {isRenaming ? (
                <div className="kn-col-manager-rename">
                  <input
                    className="kn-input"
                    value={renameValue}
                    onChange={(e) => setRenameValue(e.target.value)}
                    onKeyDown={(e) => { if (e.key === "Enter") renameColumn(col.id); if (e.key === "Escape") setRenaming(null); }}
                    autoFocus
                  />
                  <button className="kn-btn kn-btn--primary" onClick={() => renameColumn(col.id)}>Save</button>
                  <button className="kn-btn kn-btn--ghost" onClick={() => setRenaming(null)}>Cancel</button>
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
                      {sorted.filter((c) => c.id !== col.id).map((c) => (
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
                    <button className="kn-btn kn-btn--ghost" onClick={() => { setDeleting(null); setReassignTarget(""); }}>
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
                      onClick={() => { setRenaming(col.id); setRenameValue(col.name); setDeleting(null); }}
                    >
                      Rename
                    </button>
                    <button
                      className="kn-btn kn-btn--ghost kn-btn--danger"
                      onClick={() => { setDeleting(col.id); setRenaming(null); setReassignTarget(""); }}
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
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") createColumn(); }}
        />
        <button className="kn-btn kn-btn--primary" onClick={createColumn}>Create</button>
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
}: {
  panel: PanelState;
  onClose: () => void;
  onSubmit: (data: CardFormData) => void;
  onDelete?: (card: Card) => void;
}) {
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [priority, setPriority] = useState("");
  const [assignee, setAssignee] = useState<AssigneeValue>({ userId: 0, agentName: "", display: "" });
  const [labels, setLabels] = useState("");
  const [dueDate, setDueDate] = useState("");  // "YYYY-MM-DD" or ""
  const [comments, setComments] = useState<Comment[]>([]);
  const [newComment, setNewComment] = useState("");
  const [submittingComment, setSubmittingComment] = useState(false);
  const [confirmDeleteComment, setConfirmDeleteComment] = useState<Comment | null>(null);
  const [deletingComment, setDeletingComment] = useState(false);
  const [allUsers, setAllUsers] = useState<UserDetails[]>([]);
  const [allAgents, setAllAgents] = useState<RegistryAlias[]>([]);
  const [savedCountdown, setSavedCountdown] = useState(0);
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    return () => { if (countdownRef.current) clearInterval(countdownRef.current); };
  }, []);

  useEffect(() => {
    if (panel.type === "edit-card") {
      apiClient.tasks.listComments(panel.card.id)
        .then(setComments)
        .catch(() => setComments([]));
    } else {
      setComments([]);
    }
  }, [panel]);

  // Fetch users and agents for assignee autocomplete
  useEffect(() => {
    apiClient.users.listUsers()
      .then(setAllUsers)
      .catch(() => setAllUsers([]));
    apiClient.agents.list()
      .then(setAllAgents)
      .catch(() => setAllAgents([]));
  }, []);

  useEffect(() => {
    if (panel.type === "edit-card") {
      const c = panel.card;
      setTitle(c.title);
      setDescription(c.description ?? "");
      setPriority(c.priority ?? "");
      setAssignee({
        userId: c.assignee_id ?? 0,
        agentName: c.assignee_agent ?? "",
        display: c.assignee_name ?? "",
      });
      setLabels(c.labels ?? "");
      setDueDate(c.due_date ? new Date(c.due_date).toISOString().slice(0, 10) : "");
    } else {
      setTitle(""); setDescription(""); setPriority(""); setAssignee({ userId: 0, agentName: "", display: "" }); setLabels(""); setDueDate("");
    }
  }, [panel]);

  const isCard = panel.type === "new-card" || panel.type === "edit-card";
  const isSimple = panel.type === "new-board";

  const heading =
    panel.type === "new-board" ? "New Board" :
    panel.type === "new-card" ? `New Card` :
    panel.card.title;

  function handleSubmit() {
    onSubmit({ title, description, priority, assigneeId: assignee.userId, assigneeAgent: assignee.agentName, labels, dueDate });
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
      const created = await apiClient.tasks.createComment(panel.card.id, newComment.trim());
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
      await apiClient.tasks.deleteComment(panel.card.id, confirmDeleteComment.id);
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
          <div className="kn-modal-overlay" onClick={() => setConfirmDeleteComment(null)}>
            <div className="kn-modal" onClick={(e) => e.stopPropagation()}>
              <div className="kn-modal-title">Delete comment?</div>
              <div className="kn-modal-body">
                This comment may have been written by someone else. Are you sure you want to delete it?
              </div>
              <div className="kn-modal-actions">
                <button
                  className="kn-btn kn-btn--ghost"
                  onClick={() => setConfirmDeleteComment(null)}
                  disabled={deletingComment}
                >Cancel</button>
                <button
                  className="kn-btn kn-btn--danger"
                  onClick={handleDeleteComment}
                  disabled={deletingComment}
                >{deletingComment ? "Deleting..." : "Confirm"}</button>
              </div>
            </div>
          </div>
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

export default function KanbanBoard({ initialSlug, onBoardChange }: {
  initialSlug?: string;
  onBoardChange?: (slug: string) => void;
}) {
  // Parse manage view from slug: "board-slug/manage"
  const parsedSlug = initialSlug?.replace(/\/manage$/, "") || "";
  const isManageView = initialSlug?.endsWith("/manage") ?? false;

  const [boards, setBoards] = useState<Board[]>([]);
  const [activeBoardId, setActiveBoardId] = useState<string | null>(null);
  const [columns, setColumns] = useState<Column[]>([]);
  const [cards, setCards] = useState<Card[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [panel, setPanel] = useState<PanelState | null>(null);
  const [activeCard, setActiveCard] = useState<Card | null>(null);
  const [countdown, setCountdown] = useState(60);
  const countdownRef = useRef(60);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } })
  );

  const selectedCardId =
    panel?.type === "edit-card" ? panel.card.id : null;

  // ── Load ────────────────────────────────────────────────────────────────────

  const loadBoards = useCallback(async () => {
    const bs = await apiClient.tasks.listBoards();
    setBoards(bs);
    return bs;
  }, []);

  const loadBoard = useCallback(async (boardId: string) => {
    const [cols, crds] = await Promise.all([
      apiClient.tasks.listColumns(boardId),
      apiClient.tasks.listCards(boardId),
    ]);
    setColumns(cols);
    setCards(crds);
  }, []);

  useEffect(() => {
    setLoading(true);
    loadBoards()
      .then((bs) => {
        if (bs.length === 0) return;
        // Match URL slug to a board name, fallback to first board
        const match = parsedSlug
          ? bs.find((b) => slugify(b.name) === parsedSlug)
          : null;
        const target = match ?? bs[0];
        setActiveBoardId(target.id);
        const suffix = isManageView ? "/manage" : "";
        onBoardChange?.(slugify(target.name) + suffix);
        return loadBoard(target.id);
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (activeBoardId) loadBoard(activeBoardId).catch((e) => setError(String(e)));
  }, [activeBoardId]);

  // Auto-refresh columns & cards with countdown
  const refreshBoard = useCallback(() => {
    loadBoards().catch(() => {});
    if (activeBoardId) {
      loadBoard(activeBoardId).catch(() => {});
    }
    countdownRef.current = 60;
    setCountdown(60);
  }, [activeBoardId, loadBoard, loadBoards]);

  useEffect(() => {
    if (!activeBoardId) return;
    countdownRef.current = 60;
    setCountdown(60);
    const id = setInterval(() => {
      countdownRef.current -= 1;
      setCountdown(countdownRef.current);
      if (countdownRef.current <= 0) {
        loadBoard(activeBoardId).catch(() => {});
        countdownRef.current = 60;
        setCountdown(60);
      }
    }, 1000);
    return () => clearInterval(id);
  }, [activeBoardId, loadBoard]);

  // ── Navigation helpers ──────────────────────────────────────────────────────

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
      await apiClient.tasks.updateCard(activeBoardId, draggedCard.id, {
        column_id: targetColId,
        position: newPosition,
      });
    } catch {
      loadBoard(activeBoardId);
    }
  }, [cards, activeBoardId, loadBoard]);

  // ── Panel submit ─────────────────────────────────────────────────────────────

  async function submitPanel(data: CardFormData) {
    if (!panel) return;
    const trimTitle = data.title.trim();
    if (!trimTitle) return;

    const dueDateMs = data.dueDate ? new Date(data.dueDate).getTime() : null;

    try {
      if (panel.type === "new-board") {
        const b = await apiClient.tasks.createBoard({ name: trimTitle, description: data.description.trim() });
        setBoards((prev) => [...prev, b]);
        setActiveBoardId(b.id);
        onBoardChange?.(slugify(b.name));
      }

      if (panel.type === "new-card" && activeBoardId) {
        const colCards = cards
          .filter((c) => c.column_id === panel.columnId)
          .sort((a, b) => a.position - b.position);
        const card = await apiClient.tasks.createCard(activeBoardId, {
          column_id: panel.columnId,
          title: trimTitle,
          description: data.description.trim(),
          priority: data.priority,
          assignee_id: data.assigneeId || undefined,
          assignee_agent: data.assigneeAgent || undefined,
          labels: data.labels.trim(),
          due_date: dueDateMs,
          position: positionAfter(colCards),
        });
        setCards((prev) => [...prev, card]);
      }

      if (panel.type === "edit-card" && activeBoardId) {
        const updated = await apiClient.tasks.updateCard(activeBoardId, panel.card.id, {
          title: trimTitle,
          description: data.description.trim(),
          priority: data.priority,
          assignee_id: data.assigneeId || undefined,
          assignee_agent: data.assigneeAgent || undefined,
          clear_assignee: !data.assigneeId && !data.assigneeAgent,
          labels: data.labels.trim(),
          due_date: dueDateMs ?? undefined,
          clear_due: !data.dueDate,
        });
        setCards((prev) => prev.map((c) => (c.id === updated.id ? updated : c)));
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
    await apiClient.tasks.deleteCard(activeBoardId, card.id);
    setCards((prev) => prev.filter((c) => c.id !== card.id));
    setPanel(null);
  }, [activeBoardId]);

  const onAddCard = useCallback((c: Column) =>
    setPanel({ type: "new-card", columnId: c.id, columnName: c.name }), []);

  const onEditCard = useCallback((card: Card, columnName: string) =>
    setPanel({ type: "edit-card", card, columnName }), []);

  // ── Render ───────────────────────────────────────────────────────────────────

  // Memoize derived data so KanbanColumn only re-renders when its own cards change
  const sortedColumns = useMemo(
    () => [...columns].sort((a, b) => a.position - b.position),
    [columns]
  );

  const cardsByColumn = useMemo(() => {
    const map = new Map<string, Card[]>();
    for (const card of cards) {
      const list = map.get(card.column_id) ?? [];
      list.push(card);
      map.set(card.column_id, list);
    }
    for (const list of map.values()) list.sort((a, b) => a.position - b.position);
    return map;
  }, [cards]);

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
              onClick={() => { setActiveBoardId(b.id); onBoardChange?.(slugify(b.name)); setPanel(null); }}
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
              <span className="kn-board-meta">{sortedColumns.length} columns · {cards.length} cards</span>
              <button className="kn-btn kn-btn--outline kn-refresh-btn" onClick={refreshBoard}>
                <span className="kn-refresh-countdown">({countdown}s)</span> Refresh
              </button>
              <button
                className={`kn-btn kn-btn--outline ${isManageView ? "kn-btn--active" : ""}`}
                onClick={() => isManageView ? navigateToBoard() : navigateToManage()}
              >
                Manage columns
              </button>
            </div>
          </div>
        )}

        {/* Manage columns view */}
        {isManageView && activeBoard && activeBoardId ? (
          <ColumnManager
            columns={columns}
            cards={cards}
            boardId={activeBoardId}
            onBack={navigateToBoard}
            onColumnsChange={setColumns}
            onCardsChange={setCards}
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
                  />
                ))}
                {sortedColumns.length === 0 && (
                  <div className="kn-board-empty">
                    No columns yet — add one to get started.
                  </div>
                )}
              </div>
            </SortableContext>

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
          onClose={() => setPanel(null)}
          onSubmit={submitPanel}
          onDelete={deleteCard}
        />
      )}
    </div>
  );
}
