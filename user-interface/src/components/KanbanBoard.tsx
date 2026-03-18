import { useState, useEffect, useCallback, useMemo, memo } from "react";
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  closestCorners,
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
import type { Board, Column, Card, Comment } from "@teamagentica/api-client";

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
  | { type: "new-column" }
  | { type: "new-card"; columnId: string; columnName: string }
  | { type: "edit-card"; card: Card; columnName: string }
  | { type: "rename-column"; column: Column };

interface CardFormData {
  title: string;
  description: string;
  priority: string;
  assignee: string;
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
      {(card.priority || card.assignee) && (
        <div className="kn-card-meta">
          {card.priority && (
            <span className={`kn-priority ${PRIORITY_CLASS[card.priority] ?? ""}`}>
              {PRIORITY_LABEL[card.priority]}
            </span>
          )}
          {card.assignee && (
            <span className="kn-card-assignee">{card.assignee}</span>
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
  onDeleteColumn,
  onRenameColumn,
}: {
  column: Column;
  cards: Card[];
  selectedCardId: string | null;
  onAddCard: (column: Column) => void;
  onEditCard: (card: Card, columnName: string) => void;
  onDeleteColumn: (column: Column) => void;
  onRenameColumn: (column: Column) => void;
}) {
  const { setNodeRef } = useSortable({
    id: `col-${column.id}`,
    data: { type: "column", column },
  });

  return (
    <div ref={setNodeRef} className="kn-column">
      <div className="kn-column-header">
        <span className="kn-column-name">{column.name}</span>
        <span className="kn-column-count">{cards.length}</span>
        <div className="kn-column-menu">
          <button className="kn-btn kn-btn--ghost" onClick={() => onRenameColumn(column)}>Rename</button>
          <button className="kn-btn kn-btn--ghost kn-btn--danger" onClick={() => onDeleteColumn(column)}>Delete</button>
        </div>
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
  const [assignee, setAssignee] = useState("");
  const [labels, setLabels] = useState("");
  const [dueDate, setDueDate] = useState("");  // "YYYY-MM-DD" or ""
  const [comments, setComments] = useState<Comment[]>([]);

  useEffect(() => {
    if (panel.type === "edit-card") {
      apiClient.tasks.listComments(panel.card.id)
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
      setPriority(c.priority ?? "");
      setAssignee(c.assignee ?? "");
      setLabels(c.labels ?? "");
      setDueDate(c.due_date ? new Date(c.due_date).toISOString().slice(0, 10) : "");
    } else if (panel.type === "rename-column") {
      setTitle(panel.column.name);
      setDescription(""); setPriority(""); setAssignee(""); setLabels(""); setDueDate("");
    } else {
      setTitle(""); setDescription(""); setPriority(""); setAssignee(""); setLabels(""); setDueDate("");
    }
  }, [panel]);

  const isCard = panel.type === "new-card" || panel.type === "edit-card";
  const isSimple = panel.type === "new-board" || panel.type === "new-column" || panel.type === "rename-column";

  const heading =
    panel.type === "new-board" ? "New Board" :
    panel.type === "new-column" ? "New Column" :
    panel.type === "new-card" ? `New Card` :
    panel.type === "edit-card" ? panel.card.title :
    `Rename Column`;

  function handleSubmit() {
    onSubmit({ title, description, priority, assignee, labels, dueDate });
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
            {panel.type === "new-board" ? "Board name" :
             isSimple ? "Column name" : "Title"}
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
          <div className="kn-field">
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
            <input
              className="kn-input"
              placeholder="Name or @handle"
              value={assignee}
              onChange={(e) => setAssignee(e.target.value)}
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
                      <span className="kn-comment-author">{c.author || "agent"}</span>
                      <span className="kn-comment-time">{formatCommentTime(c.created_at)}</span>
                    </div>
                    <div className="kn-comment-body">{c.body}</div>
                  </div>
                ))}
              </div>
            )}
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
            <button className="kn-btn kn-btn--primary" onClick={handleSubmit}>
              {panel.type === "edit-card" || panel.type === "rename-column" ? "Save" : "Create"}
            </button>
          </div>
        </div>
      </div>
    </aside>
  );
}

// ── Main ──────────────────────────────────────────────────────────────────────

export default function KanbanBoard() {
  const [boards, setBoards] = useState<Board[]>([]);
  const [activeBoardId, setActiveBoardId] = useState<string | null>(null);
  const [columns, setColumns] = useState<Column[]>([]);
  const [cards, setCards] = useState<Card[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [panel, setPanel] = useState<PanelState | null>(null);
  const [activeCard, setActiveCard] = useState<Card | null>(null);

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
        if (bs.length > 0) {
          setActiveBoardId(bs[0].id);
          return loadBoard(bs[0].id);
        }
      })
      .catch((e) => setError(String(e)))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    if (activeBoardId) loadBoard(activeBoardId).catch((e) => setError(String(e)));
  }, [activeBoardId]);

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
      }

      if (panel.type === "new-column" && activeBoardId) {
        const pos = positionAfter([...columns].sort((a, b) => a.position - b.position));
        const col = await apiClient.tasks.createColumn(activeBoardId, { name: trimTitle, position: pos });
        setColumns((prev) => [...prev, col]);
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
          assignee: data.assignee.trim(),
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
          assignee: data.assignee.trim(),
          labels: data.labels.trim(),
          due_date: dueDateMs ?? undefined,
          clear_due: !data.dueDate,
        });
        setCards((prev) => prev.map((c) => (c.id === updated.id ? updated : c)));
      }

      if (panel.type === "rename-column" && activeBoardId) {
        const updated = await apiClient.tasks.updateColumn(activeBoardId, panel.column.id, { name: trimTitle });
        setColumns((prev) => prev.map((c) => (c.id === updated.id ? updated : c)));
      }
    } catch (e) {
      setError(String(e));
    }

    setPanel(null);
  }

  // ── CRUD ────────────────────────────────────────────────────────────────────

  const deleteCard = useCallback(async (card: Card) => {
    if (!activeBoardId) return;
    await apiClient.tasks.deleteCard(activeBoardId, card.id);
    setCards((prev) => prev.filter((c) => c.id !== card.id));
    setPanel(null);
  }, [activeBoardId]);

  const deleteColumn = useCallback(async (column: Column) => {
    if (!activeBoardId) return;
    await apiClient.tasks.deleteColumn(activeBoardId, column.id);
    setColumns((prev) => prev.filter((c) => c.id !== column.id));
    setCards((prev) => prev.filter((c) => c.column_id !== column.id));
  }, [activeBoardId]);

  const onAddCard = useCallback((c: Column) =>
    setPanel({ type: "new-card", columnId: c.id, columnName: c.name }), []);

  const onEditCard = useCallback((card: Card, columnName: string) =>
    setPanel({ type: "edit-card", card, columnName }), []);

  const onRenameColumn = useCallback((c: Column) =>
    setPanel({ type: "rename-column", column: c }), []);

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
              onClick={() => { setActiveBoardId(b.id); setPanel(null); }}
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
              <button
                className={`kn-btn kn-btn--outline ${panel?.type === "new-column" ? "kn-btn--active" : ""}`}
                onClick={() => setPanel(panel?.type === "new-column" ? null : { type: "new-column" })}
              >
                + Column
              </button>
            </div>
          </div>
        )}

        {/* Board */}
        {!activeBoard ? (
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
                    onDeleteColumn={deleteColumn}
                    onRenameColumn={onRenameColumn}
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
