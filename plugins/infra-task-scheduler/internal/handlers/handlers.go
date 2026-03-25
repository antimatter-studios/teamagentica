package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/scheduler"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/storage"
)

type Handler struct {
	sched *scheduler.Scheduler
	db    *storage.DB
}

func NewHandler(sched *scheduler.Scheduler, db *storage.DB) *Handler {
	return &Handler{sched: sched, db: db}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// --- Job CRUD ---

type createJobRequest struct {
	Name         string `json:"name" binding:"required"`
	Text         string `json:"text"`
	Type         string `json:"type" binding:"required"` // "once" | "repeat"
	TriggerType  string `json:"trigger_type"`             // "timer" | "event" (default "timer")
	Schedule     string `json:"schedule"`                  // required for timer
	Interval     string `json:"interval"`                  // backward compat
	EventPattern string `json:"event_pattern"`             // required for event
	ActionType   string `json:"action_type"`
	ActionConfig string `json:"action_config"`
}

func (h *Handler) CreateJob(c *gin.Context) {
	var req createJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Type != "once" && req.Type != "repeat" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type must be 'once' or 'repeat'"})
		return
	}

	triggerType := req.TriggerType
	if triggerType == "" {
		triggerType = "timer"
	}
	if triggerType != "timer" && triggerType != "event" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trigger_type must be 'timer' or 'event'"})
		return
	}

	actionType := req.ActionType
	if actionType == "" {
		actionType = "log"
	}

	job := &storage.Job{
		Name:         req.Name,
		Text:         req.Text,
		Type:         req.Type,
		TriggerType:  triggerType,
		ActionType:   actionType,
		ActionConfig: req.ActionConfig,
		Enabled:      true,
	}

	if triggerType == "timer" {
		schedule := req.Schedule
		if schedule == "" {
			schedule = req.Interval
		}
		if schedule == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "schedule is required for timer jobs"})
			return
		}
		nf, schedType, err := scheduler.ParseSchedule(schedule, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job.Schedule = schedule
		job.ScheduleType = schedType
		job.NextFire = nf.UnixMilli()
	} else {
		if req.EventPattern == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "event_pattern is required for event jobs"})
			return
		}
		job.EventPattern = req.EventPattern
	}

	if err := h.db.CreateJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.sched.Reload()
	c.JSON(http.StatusCreated, job)
}

func (h *Handler) ListJobs(c *gin.Context) {
	jobs, err := h.db.ListJobs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": jobs, "count": len(jobs)})
}

func (h *Handler) GetJob(c *gin.Context) {
	job, err := h.db.GetJob(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

type updateJobRequest struct {
	Name         *string `json:"name"`
	Text         *string `json:"text"`
	Schedule     *string `json:"schedule"`
	Interval     *string `json:"interval"`
	EventPattern *string `json:"event_pattern"`
	Enabled      *bool   `json:"enabled"`
	ActionType   *string `json:"action_type"`
	ActionConfig *string `json:"action_config"`
}

func (h *Handler) UpdateJob(c *gin.Context) {
	id := c.Param("id")
	job, err := h.db.GetJob(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	var req updateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Name != nil {
		job.Name = *req.Name
	}
	if req.Text != nil {
		job.Text = *req.Text
	}
	if req.ActionType != nil {
		job.ActionType = *req.ActionType
	}
	if req.ActionConfig != nil {
		job.ActionConfig = *req.ActionConfig
	}
	if req.EventPattern != nil {
		job.EventPattern = *req.EventPattern
	}

	newSchedule := req.Schedule
	if newSchedule == nil {
		newSchedule = req.Interval
	}
	if newSchedule != nil && job.TriggerType == "timer" {
		nf, schedType, err := scheduler.ParseSchedule(*newSchedule, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job.Schedule = *newSchedule
		job.ScheduleType = schedType
		job.NextFire = nf.UnixMilli()
	}

	if req.Enabled != nil {
		job.Enabled = *req.Enabled
		if *req.Enabled && job.TriggerType == "timer" && job.NextFire < time.Now().UnixMilli() {
			nf, _, _ := scheduler.ParseSchedule(job.Schedule, time.Now())
			job.NextFire = nf.UnixMilli()
		}
	}

	if err := h.db.UpdateJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	h.sched.Reload()
	c.JSON(http.StatusOK, job)
}

func (h *Handler) DeleteJob(c *gin.Context) {
	if err := h.db.DeleteJob(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	h.sched.Reload()
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// --- Log ---

func (h *Handler) GetLog(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := h.db.ListLogs(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

// --- MCP Tools ---

func (h *Handler) ToolDefs() interface{} {
	return []gin.H{
			{
				"name":        "list_jobs",
				"description": "List all scheduled jobs (both timer and event triggers)",
				"endpoint":    "/mcp/list_jobs",
				"parameters":  gin.H{"type": "object", "properties": gin.H{}},
			},
			{
				"name":        "create_job",
				"description": "Create a new scheduled job. Use trigger_type 'timer' for cron/interval schedules, or 'event' for event-driven triggers.",
				"endpoint":    "/mcp/create_job",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"name":          gin.H{"type": "string", "description": "Job name"},
						"text":          gin.H{"type": "string", "description": "Description or message text"},
						"type":          gin.H{"type": "string", "description": "Firing behavior", "enum": []string{"once", "repeat"}},
						"trigger_type":  gin.H{"type": "string", "description": "Trigger mechanism", "enum": []string{"timer", "event"}},
						"schedule":      gin.H{"type": "string", "description": "For timer: Go duration (5m, 1h) or cron expression (*/5 * * * *)"},
						"event_pattern": gin.H{"type": "string", "description": "For event: SDK event type to listen for (e.g. task-tracking:assign)"},
					},
					"required": []string{"name", "type", "trigger_type"},
				},
			},
			{
				"name":        "update_job",
				"description": "Update an existing job's fields",
				"endpoint":    "/mcp/update_job",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"job_id":        gin.H{"type": "string", "description": "ID of the job to update"},
						"name":          gin.H{"type": "string", "description": "New name"},
						"text":          gin.H{"type": "string", "description": "New description"},
						"schedule":      gin.H{"type": "string", "description": "New schedule (timer jobs only)"},
						"event_pattern": gin.H{"type": "string", "description": "New event pattern (event jobs only)"},
						"enabled":       gin.H{"type": "boolean", "description": "Enable or disable the job"},
					},
					"required": []string{"job_id"},
				},
			},
			{
				"name":        "delete_job",
				"description": "Delete a scheduled job",
				"endpoint":    "/mcp/delete_job",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"job_id": gin.H{"type": "string", "description": "ID of the job to delete"},
					},
					"required": []string{"job_id"},
				},
			},
			{
				"name":        "trigger_job",
				"description": "Manually fire a job immediately regardless of its schedule or trigger",
				"endpoint":    "/mcp/trigger_job",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"job_id": gin.H{"type": "string", "description": "ID of the job to trigger"},
					},
					"required": []string{"job_id"},
				},
			},
			{
				"name":        "get_log",
				"description": "Get recent execution log entries",
				"endpoint":    "/mcp/get_log",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"limit": gin.H{"type": "integer", "description": "Max entries to return (default 50)"},
					},
				},
			},
			{
				"name":        "list_dispatch_queue",
				"description": "List dispatch queue entries (agent task assignments). Filter by status: pending, dispatched, completed, failed.",
				"endpoint":    "/mcp/list_dispatch_queue",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"status": gin.H{"type": "string", "description": "Filter by status (optional)", "enum": []string{"pending", "dispatched", "completed", "failed"}},
						"limit":  gin.H{"type": "integer", "description": "Max entries to return (default 50)"},
					},
				},
			},
			{
				"name":        "retry_dispatch",
				"description": "Retry a failed dispatch entry",
				"endpoint":    "/mcp/retry_dispatch",
				"parameters": gin.H{
					"type": "object",
					"properties": gin.H{
						"entry_id": gin.H{"type": "string", "description": "ID of the failed dispatch entry to retry"},
					},
					"required": []string{"entry_id"},
				},
			},
	}
}

func (h *Handler) GetTools(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"tools": h.ToolDefs()})
}

// MCP endpoint handlers — accept POST with JSON body.

func (h *Handler) MCPListJobs(c *gin.Context) {
	jobs, err := h.db.ListJobs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs, "count": len(jobs)})
}

func (h *Handler) MCPCreateJob(c *gin.Context) {
	h.CreateJob(c)
}

type mcpUpdateReq struct {
	JobID        string  `json:"job_id" binding:"required"`
	Name         *string `json:"name"`
	Text         *string `json:"text"`
	Schedule     *string `json:"schedule"`
	EventPattern *string `json:"event_pattern"`
	Enabled      *bool   `json:"enabled"`
}

func (h *Handler) MCPUpdateJob(c *gin.Context) {
	var req mcpUpdateReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "id", Value: req.JobID})

	updateReq := updateJobRequest{
		Name:         req.Name,
		Text:         req.Text,
		Schedule:     req.Schedule,
		EventPattern: req.EventPattern,
		Enabled:      req.Enabled,
	}
	// Rebind so UpdateJob picks it up.
	c.Set("_mcp_update", updateReq)

	job, err := h.db.GetJob(req.JobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	if req.Name != nil {
		job.Name = *req.Name
	}
	if req.Text != nil {
		job.Text = *req.Text
	}
	if req.EventPattern != nil {
		job.EventPattern = *req.EventPattern
	}
	if req.Schedule != nil && job.TriggerType == "timer" {
		nf, schedType, err := scheduler.ParseSchedule(*req.Schedule, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job.Schedule = *req.Schedule
		job.ScheduleType = schedType
		job.NextFire = nf.UnixMilli()
	}
	if req.Enabled != nil {
		job.Enabled = *req.Enabled
		if *req.Enabled && job.TriggerType == "timer" && job.NextFire < time.Now().UnixMilli() {
			nf, _, _ := scheduler.ParseSchedule(job.Schedule, time.Now())
			job.NextFire = nf.UnixMilli()
		}
	}

	if err := h.db.UpdateJob(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.sched.Reload()
	c.JSON(http.StatusOK, job)
}

type mcpDeleteReq struct {
	JobID string `json:"job_id" binding:"required"`
}

func (h *Handler) MCPDeleteJob(c *gin.Context) {
	var req mcpDeleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.db.DeleteJob(req.JobID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	h.sched.Reload()
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

type mcpTriggerReq struct {
	JobID string `json:"job_id" binding:"required"`
}

func (h *Handler) MCPTriggerJob(c *gin.Context) {
	var req mcpTriggerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.sched.FireJob(req.JobID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"triggered": true})
}

type mcpGetLogReq struct {
	Limit int `json:"limit"`
}

func (h *Handler) MCPGetLog(c *gin.Context) {
	var req mcpGetLogReq
	_ = c.ShouldBindJSON(&req)
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	entries, err := h.db.ListLogs(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

// --- Dispatch Queue Endpoints ---

func (h *Handler) ListDispatchQueue(c *gin.Context) {
	status := c.Query("status")
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	entries, err := h.db.ListDispatchEntries(status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

func (h *Handler) GetDispatchEntry(c *gin.Context) {
	entry, err := h.db.GetDispatchEntry(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dispatch entry not found"})
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (h *Handler) RetryDispatch(c *gin.Context) {
	entry, err := h.db.GetDispatchEntry(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dispatch entry not found"})
		return
	}
	if entry.Status != "failed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "can only retry failed entries"})
		return
	}
	if err := h.db.UpdateDispatchStatus(entry.ID, "pending", map[string]interface{}{
		"error_message": "",
		"dispatched_at": 0,
		"completed_at":  0,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"retried": true})
}

// MCP dispatch endpoints

func (h *Handler) MCPListDispatchQueue(c *gin.Context) {
	var req struct {
		Status string `json:"status"`
		Limit  int    `json:"limit"`
	}
	_ = c.ShouldBindJSON(&req)
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	entries, err := h.db.ListDispatchEntries(req.Status, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

func (h *Handler) MCPRetryDispatch(c *gin.Context) {
	var req struct {
		EntryID string `json:"entry_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	entry, err := h.db.GetDispatchEntry(req.EntryID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "dispatch entry not found"})
		return
	}
	if entry.Status != "failed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "can only retry failed entries"})
		return
	}
	if err := h.db.UpdateDispatchStatus(entry.ID, "pending", map[string]interface{}{
		"error_message": "",
		"dispatched_at": 0,
		"completed_at":  0,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"retried": true})
}
