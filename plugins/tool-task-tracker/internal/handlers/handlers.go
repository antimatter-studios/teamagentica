package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/plugins/tool-task-tracker/internal/storage"
)

type Handler struct {
	db *storage.DB
}

func New(db *storage.DB) *Handler {
	return &Handler{db: db}
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
	c.JSON(http.StatusOK, cards)
}

func (h *Handler) CreateCard(c *gin.Context) {
	boardID := c.Param("id")
	if _, err := h.db.GetBoard(boardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "board not found"})
		return
	}
	var req struct {
		ColumnID    string  `json:"column_id" binding:"required"`
		Title       string  `json:"title" binding:"required"`
		Description string  `json:"description"`
		Priority    string  `json:"priority"`
		Assignee    string  `json:"assignee"`
		Labels      string  `json:"labels"`
		DueDate     *int64  `json:"due_date"`
		Position    float64 `json:"position"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	card := &storage.Card{
		ID:          uuid.New().String(),
		BoardID:     boardID,
		ColumnID:    req.ColumnID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Labels:      req.Labels,
		DueDate:     req.DueDate,
		Position:    req.Position,
	}
	if err := h.db.CreateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, card)
}

func (h *Handler) UpdateCard(c *gin.Context) {
	card, err := h.db.GetCard(c.Param("cid"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	var req struct {
		ColumnID    *string  `json:"column_id"`
		Title       *string  `json:"title"`
		Description *string  `json:"description"`
		Priority    *string  `json:"priority"`
		Assignee    *string  `json:"assignee"`
		Labels      *string  `json:"labels"`
		DueDate     *int64   `json:"due_date"`
		ClearDue    bool     `json:"clear_due"`
		Position    *float64 `json:"position"`
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
	if req.Assignee != nil {
		card.Assignee = *req.Assignee
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
	c.JSON(http.StatusOK, card)
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
	c.JSON(http.StatusOK, comments)
}

func (h *Handler) CreateComment(c *gin.Context) {
	cardID := c.Param("cid")
	if _, err := h.db.GetCard(cardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	var req struct {
		Author string `json:"author"`
		Body   string `json:"body" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	comment := &storage.Comment{
		ID:     uuid.New().String(),
		CardID: cardID,
		Author: req.Author,
		Body:   req.Body,
	}
	if err := h.db.CreateComment(comment); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, comment)
}

func (h *Handler) DeleteComment(c *gin.Context) {
	if err := h.db.DeleteComment(c.Param("cmid")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// ── MCP tools ─────────────────────────────────────────────────────────────────

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tools": []gin.H{
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
						"board_id":    gin.H{"type": "string", "description": "ID of the board"},
						"column_id":   gin.H{"type": "string", "description": "ID of the column (state) to place the task in"},
						"title":       gin.H{"type": "string", "description": "Task title"},
						"description": gin.H{"type": "string", "description": "Task description"},
						"priority":    gin.H{"type": "string", "description": "Priority: low, medium, high, urgent", "enum": []string{"", "low", "medium", "high", "urgent"}},
						"assignee":    gin.H{"type": "string", "description": "Assignee name or identifier"},
						"labels":      gin.H{"type": "string", "description": "Comma-separated labels"},
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
						"card_id":     gin.H{"type": "string", "description": "ID of the task/card"},
						"title":       gin.H{"type": "string", "description": "New title"},
						"description": gin.H{"type": "string", "description": "New description"},
						"priority":    gin.H{"type": "string", "description": "Priority: low, medium, high, urgent", "enum": []string{"", "low", "medium", "high", "urgent"}},
						"assignee":    gin.H{"type": "string", "description": "Assignee name or identifier"},
						"labels":      gin.H{"type": "string", "description": "Comma-separated labels"},
						"column_id":   gin.H{"type": "string", "description": "Move to this column"},
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
						"card_id": gin.H{"type": "string", "description": "ID of the task/card"},
						"body":    gin.H{"type": "string", "description": "Comment text"},
						"author":  gin.H{"type": "string", "description": "Author name or identifier"},
					},
					"required": []string{"card_id", "body"},
				},
			},
		},
	})
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
	// Index cards by column.
	byColumn := make(map[string][]storage.Card)
	for _, card := range cards {
		byColumn[card.ColumnID] = append(byColumn[card.ColumnID], card)
	}
	type statusGroup struct {
		StatusID   string         `json:"status_id"`
		StatusName string         `json:"status_name"`
		Tasks      []storage.Card `json:"tasks"`
	}
	groups := make([]statusGroup, 0, len(cols))
	for _, col := range cols {
		tasks := byColumn[col.ID]
		if tasks == nil {
			tasks = []storage.Card{}
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
	var cards []storage.Card
	cards, err = h.db.ListCardsByColumn(req.ColumnID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status_id":   col.ID,
		"status_name": col.Name,
		"tasks":       cards,
	})
}

func (h *Handler) MCPCreateTask(c *gin.Context) {
	var req struct {
		BoardID     string  `json:"board_id" binding:"required"`
		ColumnID    string  `json:"column_id" binding:"required"`
		Title       string  `json:"title" binding:"required"`
		Description string  `json:"description"`
		Priority    string  `json:"priority"`
		Assignee    string  `json:"assignee"`
		Labels      string  `json:"labels"`
		DueDate     *int64  `json:"due_date"`
		Position    float64 `json:"position"`
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
		ID:          uuid.New().String(),
		BoardID:     req.BoardID,
		ColumnID:    req.ColumnID,
		Title:       req.Title,
		Description: req.Description,
		Priority:    req.Priority,
		Assignee:    req.Assignee,
		Labels:      req.Labels,
		DueDate:     req.DueDate,
		Position:    req.Position,
	}
	if err := h.db.CreateCard(card); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, card)
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
	c.JSON(http.StatusOK, card)
}

func (h *Handler) MCPUpdateTask(c *gin.Context) {
	var req struct {
		CardID      string   `json:"card_id" binding:"required"`
		ColumnID    *string  `json:"column_id"`
		Title       *string  `json:"title"`
		Description *string  `json:"description"`
		Priority    *string  `json:"priority"`
		Assignee    *string  `json:"assignee"`
		Labels      *string  `json:"labels"`
		DueDate     *int64   `json:"due_date"`
		ClearDue    bool     `json:"clear_due"`
		Position    *float64 `json:"position"`
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
	if req.Assignee != nil {
		card.Assignee = *req.Assignee
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
	c.JSON(http.StatusOK, card)
}

func (h *Handler) MCPAddComment(c *gin.Context) {
	var req struct {
		CardID string `json:"card_id" binding:"required"`
		Body   string `json:"body" binding:"required"`
		Author string `json:"author"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := h.db.GetCard(req.CardID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "card not found"})
		return
	}
	comment := &storage.Comment{
		ID:     uuid.New().String(),
		CardID: req.CardID,
		Author: req.Author,
		Body:   req.Body,
	}
	if err := h.db.CreateComment(comment); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, comment)
}
