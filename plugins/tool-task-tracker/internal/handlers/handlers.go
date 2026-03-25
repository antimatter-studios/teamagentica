package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/storage"
	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/usercache"
)

// EventEmitter is the subset of pluginsdk.Client needed by handlers.
type EventEmitter interface {
	ReportEvent(eventType, detail string)
}

// ensure pluginsdk.Client satisfies EventEmitter at compile time.
var _ EventEmitter = (*pluginsdk.Client)(nil)

type Handler struct {
	db        *storage.DB
	events    EventEmitter
	userCache *usercache.Cache
}

func New(db *storage.DB, events EventEmitter, cache *usercache.Cache) *Handler {
	return &Handler{db: db, events: events, userCache: cache}
}

// ── Response types with resolved user names ──────────────────────────────────

type CardResponse struct {
	storage.Card
	AssigneeName string `json:"assignee_name"`
}

type CommentResponse struct {
	storage.Comment
	AuthorName string `json:"author_name"`
}

func (h *Handler) enrichCard(ctx context.Context, card *storage.Card) CardResponse {
	resp := CardResponse{Card: *card}
	if card.AssigneeID != 0 {
		if u, err := h.userCache.Get(ctx, card.AssigneeID); err == nil && u != nil {
			resp.AssigneeName = u.FormatName()
		}
	} else if card.AssigneeAgent != "" {
		resp.AssigneeName = card.AssigneeAgent
	}
	return resp
}

func (h *Handler) enrichCards(ctx context.Context, cards []storage.Card) []CardResponse {
	ids := make(map[uint]bool)
	for _, c := range cards {
		if c.AssigneeID != 0 {
			ids[c.AssigneeID] = true
		}
	}
	idSlice := make([]uint, 0, len(ids))
	for id := range ids {
		idSlice = append(idSlice, id)
	}
	users := h.userCache.GetMany(ctx, idSlice)

	result := make([]CardResponse, len(cards))
	for i, c := range cards {
		result[i] = CardResponse{Card: c}
		if c.AssigneeID != 0 {
			if u, ok := users[c.AssigneeID]; ok {
				result[i].AssigneeName = u.FormatName()
			}
		} else if c.AssigneeAgent != "" {
			result[i].AssigneeName = c.AssigneeAgent
		}
	}
	return result
}

func (h *Handler) enrichComment(ctx context.Context, comment *storage.Comment) CommentResponse {
	resp := CommentResponse{Comment: *comment}
	if comment.AuthorID != 0 {
		if u, err := h.userCache.Get(ctx, comment.AuthorID); err == nil && u != nil {
			resp.AuthorName = u.FormatName()
		}
	}
	return resp
}

func (h *Handler) enrichComments(ctx context.Context, comments []storage.Comment) []CommentResponse {
	ids := make(map[uint]bool)
	for _, c := range comments {
		if c.AuthorID != 0 {
			ids[c.AuthorID] = true
		}
	}
	idSlice := make([]uint, 0, len(ids))
	for id := range ids {
		idSlice = append(idSlice, id)
	}
	users := h.userCache.GetMany(ctx, idSlice)

	result := make([]CommentResponse, len(comments))
	for i, c := range comments {
		result[i] = CommentResponse{Comment: c}
		if c.AuthorID != 0 {
			if u, ok := users[c.AuthorID]; ok {
				result[i].AuthorName = u.FormatName()
			}
		}
	}
	return result
}

// getUserID extracts the authenticated user ID from the kernel-injected header.
func getUserID(c *gin.Context) uint {
	idStr := c.GetHeader("X-User-ID")
	if idStr == "" {
		return 0
	}
	id, _ := strconv.ParseUint(idStr, 10, 64)
	return uint(id)
}

// emitAssign fires a task-tracking:assign event when a card gets a non-empty assignee.
func (h *Handler) emitAssign(ctx context.Context, card *storage.Card) {
	if (card.AssigneeID == 0 && card.AssigneeAgent == "") || h.events == nil {
		return
	}
	assigneeName := card.AssigneeAgent
	if card.AssigneeID != 0 {
		if u, err := h.userCache.Get(ctx, card.AssigneeID); err == nil && u != nil {
			assigneeName = u.FormatName()
		}
	}
	detail, _ := json.Marshal(map[string]interface{}{
		"card_id":        card.ID,
		"board_id":       card.BoardID,
		"title":          card.Title,
		"assignee_id":    card.AssigneeID,
		"assignee_agent": card.AssigneeAgent,
		"assignee_name":  assigneeName,
	})
	h.events.ReportEvent("task-tracking:assign", string(detail))
}

// emitComment fires a task-tracking:comment event when a comment is added
// to a card that has an agent assignee. Skips if author_id is 0 (system/agent comment).
func (h *Handler) emitComment(card *storage.Card, comment *storage.Comment) {
	if h.events == nil || card.AssigneeAgent == "" || comment.AuthorID == 0 {
		return
	}
	detail, _ := json.Marshal(map[string]interface{}{
		"card_id":        card.ID,
		"board_id":       card.BoardID,
		"author_id":      comment.AuthorID,
		"body":           comment.Body,
		"assignee_agent": card.AssigneeAgent,
	})
	h.events.ReportEvent("task-tracking:comment", string(detail))
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ── Boards ────────────────────────────────────────────────────────────────────

func (h *Handler) ListBoards(c *gin.Context) {
	boards, err := h.db.ListBoards()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, boards)
}

func (h *Handler) CreateBoard(c *gin.Context) {
	var req struct {
		Name        string `json:"name" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	b := &storage.Board{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
	}
	if err := h.db.CreateBoard(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, b)
}

func (h *Handler) GetBoard(c *gin.Context) {
	b, err := h.db.GetBoard(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	c.JSON(http.StatusOK, b)
}

func (h *Handler) UpdateBoard(c *gin.Context) {
	b, err := h.db.GetBoard(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		b.Name = *req.Name
	}
	if req.Description != nil {
		b.Description = *req.Description
	}
	if err := h.db.UpdateBoard(b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, b)
}

func (h *Handler) DeleteBoard(c *gin.Context) {
	if err := h.db.DeleteBoard(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Columns ───────────────────────────────────────────────────────────────────

func (h *Handler) ListColumns(c *gin.Context) {
	cols, err := h.db.ListColumns(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cols)
}

func (h *Handler) CreateColumn(c *gin.Context) {
	boardID := c.Param("id")
	if _, err := h.db.GetBoard(boardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	var req struct {
		Name     string  `json:"name" binding:"required"`
		Position float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	col := &storage.Column{
		ID:       uuid.New().String(),
		BoardID:  boardID,
		Name:     req.Name,
		Position: req.Position,
	}
	if err := h.db.CreateColumn(col); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, col)
}

func (h *Handler) UpdateColumn(c *gin.Context) {
	col, err := h.db.GetColumn(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "column not found"})
		return
	}
	var req struct {
		Name     *string  `json:"name"`
		Position *float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Name != nil {
		col.Name = *req.Name
	}
	if req.Position != nil {
		col.Position = *req.Position
	}
	if err := h.db.UpdateColumn(col); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, col)
}

func (h *Handler) DeleteColumn(c *gin.Context) {
	if err := h.db.DeleteColumn(c.Param("cid")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Cards ─────────────────────────────────────────────────────────────────────

func (h *Handler) ListCards(c *gin.Context) {
	cards, err := h.db.ListCards(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.enrichCards(c.Request.Context(), cards))
}

func (h *Handler) CreateCard(c *gin.Context) {
	boardID := c.Param("id")
	if _, err := h.db.GetBoard(boardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	var req struct {
		ColumnID      string  `json:"column_id" binding:"required"`
		Title         string  `json:"title" binding:"required"`
		Description   string  `json:"description"`
		Priority      string  `json:"priority"`
		AssigneeID    uint    `json:"assignee_id"`
		AssigneeAgent string  `json:"assignee_agent"`
		Labels        string  `json:"labels"`
		DueDate       *int64  `json:"due_date"`
		Position      float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card := &storage.Card{
		ID:            uuid.New().String(),
		BoardID:       boardID,
		ColumnID:      req.ColumnID,
		Title:         req.Title,
		Description:   req.Description,
		Priority:      req.Priority,
		AssigneeID:    req.AssigneeID,
		AssigneeAgent: req.AssigneeAgent,
		Labels:        req.Labels,
		DueDate:       req.DueDate,
		Position:      req.Position,
	}
	if err := h.db.CreateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.emitAssign(c.Request.Context(), card)
	c.JSON(http.StatusCreated, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) UpdateCard(c *gin.Context) {
	card, err := h.db.GetCard(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	oldAssigneeID := card.AssigneeID
	oldAssigneeAgent := card.AssigneeAgent

	var req struct {
		ColumnID       *string  `json:"column_id"`
		Title          *string  `json:"title"`
		Description    *string  `json:"description"`
		Priority       *string  `json:"priority"`
		AssigneeID     *uint    `json:"assignee_id"`
		AssigneeAgent  *string  `json:"assignee_agent"`
		ClearAssignee  bool     `json:"clear_assignee"`
		Labels         *string  `json:"labels"`
		DueDate        *int64   `json:"due_date"`
		ClearDue       bool     `json:"clear_due"`
		Position       *float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ColumnID != nil {
		card.ColumnID = *req.ColumnID
	}
	if req.Title != nil {
		card.Title = *req.Title
	}
	if req.Description != nil {
		card.Description = *req.Description
	}
	if req.Priority != nil {
		card.Priority = *req.Priority
	}
	if req.ClearAssignee {
		card.AssigneeID = 0
		card.AssigneeAgent = ""
	} else {
		if req.AssigneeID != nil {
			card.AssigneeID = *req.AssigneeID
			card.AssigneeAgent = "" // user assignee clears agent
		}
		if req.AssigneeAgent != nil {
			card.AssigneeAgent = *req.AssigneeAgent
			card.AssigneeID = 0 // agent assignee clears user
		}
	}
	if req.Labels != nil {
		card.Labels = *req.Labels
	}
	if req.ClearDue {
		card.DueDate = nil
	} else if req.DueDate != nil {
		card.DueDate = req.DueDate
	}
	if req.Position != nil {
		card.Position = *req.Position
	}
	if err := h.db.UpdateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if card.AssigneeID != oldAssigneeID || card.AssigneeAgent != oldAssigneeAgent {
		h.emitAssign(c.Request.Context(), card)
	}
	c.JSON(http.StatusOK, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) GetCard(c *gin.Context) {
	card, err := h.db.GetCard(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	c.JSON(http.StatusOK, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) DeleteCard(c *gin.Context) {
	if err := h.db.DeleteCard(c.Param("cid")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── Comments ──────────────────────────────────────────────────────────────────

func (h *Handler) ListComments(c *gin.Context) {
	comments, err := h.db.ListComments(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.enrichComments(c.Request.Context(), comments))
}

func (h *Handler) CreateComment(c *gin.Context) {
	cardID := c.Param("cid")
	card, err := h.db.GetCard(cardID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	var req struct {
		Body string `json:"body" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	authorID := getUserID(c)
	comment := &storage.Comment{
		ID:       uuid.New().String(),
		CardID:   cardID,
		AuthorID: authorID,
		Body:     req.Body,
	}
	if err := h.db.CreateComment(comment); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.emitComment(card, comment)
	c.JSON(http.StatusCreated, h.enrichComment(c.Request.Context(), comment))
}

func (h *Handler) DeleteComment(c *gin.Context) {
	if err := h.db.DeleteComment(c.Param("cmid")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── MCP tools ─────────────────────────────────────────────────────────────────

func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
		{
			"name":        "list_boards",
			"description": "List all kanban boards with their columns",
			"endpoint":    "/mcp/list_boards",
			"parameters": gin.H{
				"type":       "object",
				"properties": gin.H{},
			},
		},
		{
			"name":        "list_tasks",
			"description": "List all tasks on a board, grouped by status (column)",
			"endpoint":    "/mcp/list_tasks",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"board_id": gin.H{"type": "string", "description": "ID of the board"},
				},
				"required": []string{"board_id"},
			},
		},
		{
			"name":        "list_tasks_by_status",
			"description": "List tasks in a specific status (column)",
			"endpoint":    "/mcp/list_tasks_by_status",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"column_id": gin.H{"type": "string", "description": "ID of the status column"},
				},
				"required": []string{"column_id"},
			},
		},
		{
			"name":        "create_task",
			"description": "Create a new task (card) in a kanban board column",
			"endpoint":    "/mcp/create_task",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"board_id":       gin.H{"type": "string", "description": "ID of the board"},
					"column_id":      gin.H{"type": "string", "description": "ID of the column (state) to place the task in"},
					"title":          gin.H{"type": "string", "description": "Task title"},
					"description":    gin.H{"type": "string", "description": "Task description"},
					"priority":       gin.H{"type": "string", "description": "Priority: low, medium, high, urgent", "enum": []string{"", "low", "medium", "high", "urgent"}},
					"assignee_id":    gin.H{"type": "integer", "description": "User ID of assignee"},
					"assignee_agent": gin.H{"type": "string", "description": "Agent alias to assign (e.g. @relay)"},
					"labels":         gin.H{"type": "string", "description": "Comma-separated labels"},
				},
				"required": []string{"board_id", "column_id", "title"},
			},
		},
		{
			"name":        "set_task_state",
			"description": "Move a task to a different column (change its state)",
			"endpoint":    "/mcp/set_task_state",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"card_id":   gin.H{"type": "string", "description": "ID of the task/card"},
					"column_id": gin.H{"type": "string", "description": "ID of the target column"},
				},
				"required": []string{"card_id", "column_id"},
			},
		},
		{
			"name":        "update_task",
			"description": "Update fields on an existing task",
			"endpoint":    "/mcp/update_task",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"card_id":        gin.H{"type": "string", "description": "ID of the task/card"},
					"title":          gin.H{"type": "string", "description": "New title"},
					"description":    gin.H{"type": "string", "description": "New description"},
					"priority":       gin.H{"type": "string", "description": "Priority: low, medium, high, urgent", "enum": []string{"", "low", "medium", "high", "urgent"}},
					"assignee_id":    gin.H{"type": "integer", "description": "User ID of assignee"},
					"assignee_agent": gin.H{"type": "string", "description": "Agent alias to assign (e.g. @relay)"},
					"labels":         gin.H{"type": "string", "description": "Comma-separated labels"},
					"column_id":      gin.H{"type": "string", "description": "Move to this column"},
				},
				"required": []string{"card_id"},
			},
		},
		{
			"name":        "add_comment",
			"description": "Add a comment to a task",
			"endpoint":    "/mcp/add_comment",
			"parameters": gin.H{
				"type": "object",
				"properties": gin.H{
					"card_id":   gin.H{"type": "string", "description": "ID of the task/card"},
					"body":      gin.H{"type": "string", "description": "Comment text"},
					"author_id": gin.H{"type": "integer", "description": "User ID of author (defaults to caller)"},
				},
				"required": []string{"card_id", "body"},
			},
		},
	}
}

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

func (h *Handler) MCPListBoards(c *gin.Context) {
	boards, err := h.db.ListBoards()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type boardWithColumns struct {
		storage.Board
		Columns []storage.Column `json:"columns"`
	}
	result := make([]boardWithColumns, 0, len(boards))
	for _, b := range boards {
		cols, err := h.db.ListColumns(b.ID)
		if err != nil {
			cols = []storage.Column{}
		}
		result = append(result, boardWithColumns{Board: b, Columns: cols})
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) MCPListTasks(c *gin.Context) {
	var req struct {
		BoardID string `json:"board_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cols, err := h.db.ListColumns(req.BoardID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cards, err := h.db.ListCards(req.BoardID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	enriched := h.enrichCards(c.Request.Context(), cards)
	byColumn := make(map[string][]CardResponse)
	for _, cr := range enriched {
		byColumn[cr.ColumnID] = append(byColumn[cr.ColumnID], cr)
	}
	type statusGroup struct {
		StatusID   string         `json:"status_id"`
		StatusName string         `json:"status_name"`
		Tasks      []CardResponse `json:"tasks"`
	}
	groups := make([]statusGroup, 0, len(cols))
	for _, col := range cols {
		tasks := byColumn[col.ID]
		if tasks == nil {
			tasks = []CardResponse{}
		}
		groups = append(groups, statusGroup{
			StatusID:   col.ID,
			StatusName: col.Name,
			Tasks:      tasks,
		})
	}
	c.JSON(http.StatusOK, groups)
}

func (h *Handler) MCPListTasksByStatus(c *gin.Context) {
	var req struct {
		ColumnID string `json:"column_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	col, err := h.db.GetColumn(req.ColumnID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "status not found"})
		return
	}
	cards, err := h.db.ListCardsByColumn(req.ColumnID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status_id":   col.ID,
		"status_name": col.Name,
		"tasks":       h.enrichCards(c.Request.Context(), cards),
	})
}

func (h *Handler) MCPCreateTask(c *gin.Context) {
	var req struct {
		BoardID       string  `json:"board_id" binding:"required"`
		ColumnID      string  `json:"column_id" binding:"required"`
		Title         string  `json:"title" binding:"required"`
		Description   string  `json:"description"`
		Priority      string  `json:"priority"`
		AssigneeID    uint    `json:"assignee_id"`
		AssigneeAgent string  `json:"assignee_agent"`
		Labels        string  `json:"labels"`
		DueDate       *int64  `json:"due_date"`
		Position      float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.db.GetBoard(req.BoardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	card := &storage.Card{
		ID:            uuid.New().String(),
		BoardID:       req.BoardID,
		ColumnID:      req.ColumnID,
		Title:         req.Title,
		Description:   req.Description,
		Priority:      req.Priority,
		AssigneeID:    req.AssigneeID,
		AssigneeAgent: req.AssigneeAgent,
		Labels:        req.Labels,
		DueDate:       req.DueDate,
		Position:      req.Position,
	}
	if err := h.db.CreateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.emitAssign(c.Request.Context(), card)
	c.JSON(http.StatusCreated, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) MCPSetTaskState(c *gin.Context) {
	var req struct {
		CardID   string `json:"card_id" binding:"required"`
		ColumnID string `json:"column_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card, err := h.db.GetCard(req.CardID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	card.ColumnID = req.ColumnID
	if err := h.db.UpdateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) MCPUpdateTask(c *gin.Context) {
	var req struct {
		CardID        string   `json:"card_id" binding:"required"`
		ColumnID      *string  `json:"column_id"`
		Title         *string  `json:"title"`
		Description   *string  `json:"description"`
		Priority      *string  `json:"priority"`
		AssigneeID    *uint    `json:"assignee_id"`
		AssigneeAgent *string  `json:"assignee_agent"`
		Labels        *string  `json:"labels"`
		DueDate       *int64   `json:"due_date"`
		ClearDue      bool     `json:"clear_due"`
		Position      *float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card, err := h.db.GetCard(req.CardID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	oldAssigneeID := card.AssigneeID
	oldAssigneeAgent := card.AssigneeAgent

	if req.ColumnID != nil {
		card.ColumnID = *req.ColumnID
	}
	if req.Title != nil {
		card.Title = *req.Title
	}
	if req.Description != nil {
		card.Description = *req.Description
	}
	if req.Priority != nil {
		card.Priority = *req.Priority
	}
	if req.AssigneeID != nil {
		card.AssigneeID = *req.AssigneeID
		card.AssigneeAgent = ""
	}
	if req.AssigneeAgent != nil {
		card.AssigneeAgent = *req.AssigneeAgent
		card.AssigneeID = 0
	}
	if req.Labels != nil {
		card.Labels = *req.Labels
	}
	if req.ClearDue {
		card.DueDate = nil
	} else if req.DueDate != nil {
		card.DueDate = req.DueDate
	}
	if req.Position != nil {
		card.Position = *req.Position
	}
	if err := h.db.UpdateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if card.AssigneeID != oldAssigneeID || card.AssigneeAgent != oldAssigneeAgent {
		h.emitAssign(c.Request.Context(), card)
	}
	c.JSON(http.StatusOK, h.enrichCard(c.Request.Context(), card))
}

func (h *Handler) MCPAddComment(c *gin.Context) {
	var req struct {
		CardID   string `json:"card_id" binding:"required"`
		Body     string `json:"body" binding:"required"`
		AuthorID uint   `json:"author_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card, err := h.db.GetCard(req.CardID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	authorID := req.AuthorID
	if authorID == 0 {
		authorID = getUserID(c)
	}
	comment := &storage.Comment{
		ID:       uuid.New().String(),
		CardID:   req.CardID,
		AuthorID: authorID,
		Body:     req.Body,
	}
	if err := h.db.CreateComment(comment); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.emitComment(card, comment)
	c.JSON(http.StatusCreated, h.enrichComment(c.Request.Context(), comment))
}
