package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/storage"
)

// PromptData is the data passed to the prompt template.
type PromptData struct {
	TaskTitle       string
	TaskDescription string
	Priority        string
	Labels          string
	CurrentColumn   string
	Comments        string
	TriggerType     string // "assign" or "comment"
	TriggerComment  string
	Attempt         int
	MaxAttempts     int
	TestResults     string
}

// agentResponse is the structured JSON envelope agents respond with.
type agentResponse struct {
	Action             string `json:"action"`  // plan, execute, continue, replan, stop, reply, done, reopen, new_task
	Message            string `json:"message"` // comment text
	NewTaskTitle       string `json:"new_task_title,omitempty"`
	NewTaskDescription string `json:"new_task_description,omitempty"`
}

const defaultPromptTemplate = `You are an AI agent assigned to work on a task via a kanban board.

## Task
**Title:** {{.TaskTitle}}
**Description:** {{.TaskDescription}}
**Priority:** {{.Priority}}
**Labels:** {{.Labels}}
**Current Status:** {{.CurrentColumn}}

{{- if .Comments}}

## Conversation History
{{.Comments}}
{{- end}}

{{- if .TriggerComment}}

## Latest Message
{{.TriggerComment}}
{{- end}}

{{- if .TestResults}}

## Previous Test Results (Attempt {{.Attempt}}/{{.MaxAttempts}})
{{.TestResults}}
{{- end}}

## Instructions

{{- if eq .TriggerType "assign"}}
You have been newly assigned this task. Read it carefully, analyze the problem, and propose your plan. Do NOT start work yet — only describe what you would do.
{{- else}}
The user has commented on this task. Read the full conversation and judge their intent. Respond appropriately — answer questions, adjust your plan, or begin execution if they approve.
{{- end}}

## Response Format

You MUST respond with a JSON object. Choose the appropriate action:

- {"action": "plan", "message": "..."} — Propose a solution plan (do not execute yet)
- {"action": "execute", "message": "..."} — User approved, signal to start work
- {"action": "continue", "message": "..."} — Continue retrying after test failure
- {"action": "replan", "message": "..."} — Rethink the approach, go back to planning
- {"action": "stop", "message": "..."} — Cannot proceed, give up
- {"action": "reply", "message": "..."} — Conversational reply, no state change
- {"action": "done", "message": "..."} — Work complete, summarize solution
- {"action": "reopen", "message": "..."} — Fix was inadequate, reopen for more work
- {"action": "new_task", "message": "...", "new_task_title": "...", "new_task_description": "..."} — Fix caused a side-effect, create a new task

Respond ONLY with the JSON object, no other text.`

// initDispatch sets up dispatch event subscriptions and recovers stale entries.
func (s *Scheduler) initDispatch() {
	if !s.dispatch.Enabled {
		return
	}

	// Recover any entries stuck as "dispatched" from a previous crash
	if n, err := s.db.ResetStaleDispatched(); err == nil && n > 0 {
		log.Printf("[dispatch] recovered %d stale dispatched entries back to pending", n)
	}

	// Subscribe to task-tracking:assign
	if err := s.sdk.OnEvent("task-tracking:assign", pluginsdk.NewNullDebouncer(s.handleAssignEvent)); err != nil {
		log.Printf("[dispatch] failed to subscribe to task-tracking:assign: %v", err)
	} else {
		log.Println("[dispatch] subscribed to task-tracking:assign")
	}

	// Subscribe to task-tracking:comment
	if err := s.sdk.OnEvent("task-tracking:comment", pluginsdk.NewNullDebouncer(s.handleCommentEvent)); err != nil {
		log.Printf("[dispatch] failed to subscribe to task-tracking:comment: %v", err)
	} else {
		log.Println("[dispatch] subscribed to task-tracking:comment")
	}
}

// handleAssignEvent creates a triage dispatch when a task is assigned to an agent.
func (s *Scheduler) handleAssignEvent(e pluginsdk.EventCallback) {
	var payload struct {
		CardID        string `json:"card_id"`
		BoardID       string `json:"board_id"`
		Title         string `json:"title"`
		AssigneeAgent string `json:"assignee_agent"`
	}
	if err := json.Unmarshal([]byte(e.Detail), &payload); err != nil {
		log.Printf("[dispatch] failed to parse assign event: %v", err)
		return
	}

	if payload.AssigneeAgent == "" {
		return // human assignment, ignore
	}

	// Dedup: skip if there's already a pending/dispatched entry for this card+agent
	if exists, _ := s.db.HasPendingOrDispatched(payload.CardID, payload.AssigneeAgent); exists {
		log.Printf("[dispatch] skipping duplicate assign for card %s agent %s", payload.CardID, payload.AssigneeAgent)
		return
	}

	entry := &storage.DispatchEntry{
		CardID:       payload.CardID,
		BoardID:      payload.BoardID,
		CardTitle:    payload.Title,
		AgentAlias:   payload.AssigneeAgent,
		Status:       "pending",
		DispatchType: "triage",
	}
	if err := s.db.CreateDispatchEntry(entry); err != nil {
		log.Printf("[dispatch] failed to create triage entry: %v", err)
		return
	}
	log.Printf("[dispatch] queued triage for card %s → @%s", payload.CardID, payload.AssigneeAgent)
}

// handleCommentEvent creates a reply dispatch when a user comments on an agent-assigned card.
func (s *Scheduler) handleCommentEvent(e pluginsdk.EventCallback) {
	var payload struct {
		CardID        string `json:"card_id"`
		BoardID       string `json:"board_id"`
		AuthorID      uint   `json:"author_id"`
		Body          string `json:"body"`
		AssigneeAgent string `json:"assignee_agent"`
	}
	if err := json.Unmarshal([]byte(e.Detail), &payload); err != nil {
		log.Printf("[dispatch] failed to parse comment event: %v", err)
		return
	}

	// Skip agent/system comments (author_id=0) to prevent loops
	if payload.AuthorID == 0 {
		return
	}

	if payload.AssigneeAgent == "" {
		return // not an agent-assigned card
	}

	entry := &storage.DispatchEntry{
		CardID:         payload.CardID,
		BoardID:        payload.BoardID,
		CardTitle:      "", // will be fetched during dispatch
		AgentAlias:     payload.AssigneeAgent,
		Status:         "pending",
		DispatchType:   "reply",
		TriggerComment: payload.Body,
	}
	if err := s.db.CreateDispatchEntry(entry); err != nil {
		log.Printf("[dispatch] failed to create reply entry: %v", err)
		return
	}
	log.Printf("[dispatch] queued reply for card %s → @%s", payload.CardID, payload.AssigneeAgent)
}

// processDispatchQueue checks the queue and dispatches work to agents.
func (s *Scheduler) processDispatchQueue() {
	agents, err := s.db.ListPendingAgents()
	if err != nil || len(agents) == 0 {
		return
	}

	for _, agent := range agents {
		if !s.canDispatch(agent) {
			continue
		}

		entry, err := s.db.GetNextPending(agent)
		if err != nil || entry == nil {
			continue
		}

		// Mark as dispatched before spawning goroutine
		_ = s.db.UpdateDispatchStatus(entry.ID, "dispatched", map[string]interface{}{
			"dispatched_at": time.Now().UnixMilli(),
		})

		go s.executeDispatch(entry)
	}
}

// canDispatch checks if an agent has capacity for another dispatch.
func (s *Scheduler) canDispatch(agentAlias string) bool {
	// Check per-agent limit first
	if limit, ok := s.dispatch.AgentLimits[agentAlias]; ok {
		count, _ := s.db.CountInFlight(agentAlias)
		return count < int64(limit)
	}
	// Fall back to global limit
	if s.dispatch.GlobalLimit > 0 {
		count, _ := s.db.CountAllInFlight()
		return count < int64(s.dispatch.GlobalLimit)
	}
	// No limits configured
	return true
}

// executeDispatch runs a single dispatch entry (triage or reply).
func (s *Scheduler) executeDispatch(entry *storage.DispatchEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	log.Printf("[dispatch] executing %s for card %s → @%s", entry.DispatchType, entry.CardID, entry.AgentAlias)

	// Fetch full card
	card, err := s.fetchCard(ctx, entry.CardID)
	if err != nil {
		s.failDispatch(entry.ID, fmt.Sprintf("fetch card: %v", err))
		return
	}

	// Fetch comments
	comments, err := s.fetchComments(ctx, entry.CardID)
	if err != nil {
		comments = "(failed to fetch comments)"
	}

	// Determine current column name
	columnName := s.resolveColumnName(ctx, card)

	// Build prompt
	data := PromptData{
		TaskTitle:       card.Title,
		TaskDescription: card.Description,
		Priority:        card.Priority,
		Labels:          card.Labels,
		CurrentColumn:   columnName,
		Comments:        comments,
		TriggerType:     entry.DispatchType,
		TriggerComment:  entry.TriggerComment,
		MaxAttempts:     10,
	}
	if entry.DispatchType == "triage" {
		data.TriggerType = "assign"
	}

	prompt, err := s.renderPrompt(data)
	if err != nil {
		s.failDispatch(entry.ID, fmt.Sprintf("render prompt: %v", err))
		return
	}

	// Send to agent via relay
	resp, err := s.sendToAgent(ctx, entry.AgentAlias, entry.CardID, prompt)
	if err != nil {
		s.failDispatch(entry.ID, fmt.Sprintf("relay call: %v", err))
		return
	}

	// Parse structured response
	agentResp := s.parseAgentResponse(resp)

	// Post agent's message as comment
	if agentResp.Message != "" {
		_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s:**\n\n%s", entry.AgentAlias, agentResp.Message))
	}

	// Handle action
	s.handleAction(ctx, entry, card, agentResp)
}

// handleAction processes the agent's response action.
func (s *Scheduler) handleAction(ctx context.Context, entry *storage.DispatchEntry, card *cardData, resp agentResponse) {
	truncated := resp.Message
	if len(truncated) > 500 {
		truncated = truncated[:500]
	}

	switch resp.Action {
	case "plan":
		// Move to Todo, done
		s.moveCard(ctx, entry.CardID, card.BoardID, "Todo")
		s.completeDispatch(entry.ID, truncated)

	case "execute":
		// User approved — start execution loop
		s.completeDispatch(entry.ID, truncated)
		s.runExecutionLoop(ctx, entry, card)

	case "continue":
		// Should only happen inside execution loop, treat as done
		s.completeDispatch(entry.ID, truncated)

	case "replan":
		s.moveCard(ctx, entry.CardID, card.BoardID, "Todo")
		s.completeDispatch(entry.ID, truncated)

	case "stop":
		s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
		s.completeDispatch(entry.ID, truncated)

	case "reply":
		// No state change
		s.completeDispatch(entry.ID, truncated)

	case "done":
		s.moveCard(ctx, entry.CardID, card.BoardID, "Done")
		s.completeDispatch(entry.ID, truncated)

	case "reopen":
		s.moveCard(ctx, entry.CardID, card.BoardID, "In Progress")
		s.completeDispatch(entry.ID, truncated)
		// Re-enter execution loop
		s.runExecutionLoop(ctx, entry, card)

	case "new_task":
		// Create a new card on the same board
		if resp.NewTaskTitle != "" {
			s.createNewTask(ctx, card.BoardID, resp.NewTaskTitle, resp.NewTaskDescription, entry.AgentAlias)
		}
		s.completeDispatch(entry.ID, truncated)

	default:
		log.Printf("[dispatch] unknown action %q, treating as reply", resp.Action)
		s.completeDispatch(entry.ID, truncated)
	}
}

// runExecutionLoop runs the implement→test→retry loop.
func (s *Scheduler) runExecutionLoop(ctx context.Context, entry *storage.DispatchEntry, card *cardData) {
	const maxAttempts = 10

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		log.Printf("[dispatch] execution attempt %d/%d for card %s", attempt, maxAttempts, entry.CardID)

		// Move to In Progress
		s.moveCard(ctx, entry.CardID, card.BoardID, "In Progress")

		// Step 1: Implement
		comments, _ := s.fetchComments(ctx, entry.CardID)
		implData := PromptData{
			TaskTitle:       card.Title,
			TaskDescription: card.Description,
			Priority:        card.Priority,
			Labels:          card.Labels,
			CurrentColumn:   "In Progress",
			Comments:        comments,
			TriggerType:     "comment",
			TriggerComment:  "You have been approved to proceed. Implement the solution now. Describe what you changed.",
			Attempt:         attempt,
			MaxAttempts:     maxAttempts,
		}

		implPrompt, _ := s.renderPrompt(implData)
		implResp, err := s.sendToAgent(ctx, entry.AgentAlias, entry.CardID, implPrompt)
		if err != nil {
			_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s:** Failed to implement: %v", entry.AgentAlias, err))
			s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
			return
		}

		implAction := s.parseAgentResponse(implResp)
		_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s (attempt %d — implementation):**\n\n%s", entry.AgentAlias, attempt, implAction.Message))

		// Check for stop/replan from implementation
		if implAction.Action == "stop" {
			s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
			return
		}
		if implAction.Action == "replan" {
			s.moveCard(ctx, entry.CardID, card.BoardID, "Todo")
			return
		}

		// Step 2: Test
		s.moveCard(ctx, entry.CardID, card.BoardID, "In Review")

		comments, _ = s.fetchComments(ctx, entry.CardID)
		testData := PromptData{
			TaskTitle:       card.Title,
			TaskDescription: card.Description,
			Priority:        card.Priority,
			Labels:          card.Labels,
			CurrentColumn:   "In Review",
			Comments:        comments,
			TriggerType:     "comment",
			TriggerComment:  "Now write tests to verify your solution and run them. Report whether tests pass or fail.",
			Attempt:         attempt,
			MaxAttempts:     maxAttempts,
		}

		testPrompt, _ := s.renderPrompt(testData)
		testResp, err := s.sendToAgent(ctx, entry.AgentAlias, entry.CardID, testPrompt)
		if err != nil {
			_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s:** Failed to run tests: %v", entry.AgentAlias, err))
			s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
			return
		}

		testAction := s.parseAgentResponse(testResp)
		_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s (attempt %d — tests):**\n\n%s", entry.AgentAlias, attempt, testAction.Message))

		if testAction.Action == "done" {
			s.moveCard(ctx, entry.CardID, card.BoardID, "Done")
			s.sdk.ReportEvent("dispatch:completed", card.Title)
			return
		}

		if testAction.Action == "stop" {
			s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
			return
		}

		if testAction.Action == "replan" {
			s.moveCard(ctx, entry.CardID, card.BoardID, "Todo")
			return
		}

		// Tests failed — check for new user comments before retrying
		if attempt < maxAttempts {
			// Check for new user comments added during this attempt
			newComments, _ := s.fetchComments(ctx, entry.CardID)

			evalData := PromptData{
				TaskTitle:       card.Title,
				TaskDescription: card.Description,
				CurrentColumn:   "In Review",
				Comments:        newComments,
				TriggerType:     "comment",
				TriggerComment:  "Tests failed. Review the results and any new user comments. Decide: continue retrying, replan, or stop?",
				Attempt:         attempt,
				MaxAttempts:     maxAttempts,
				TestResults:     testAction.Message,
			}

			evalPrompt, _ := s.renderPrompt(evalData)
			evalResp, err := s.sendToAgent(ctx, entry.AgentAlias, entry.CardID, evalPrompt)
			if err != nil {
				continue // just retry
			}

			evalAction := s.parseAgentResponse(evalResp)
			_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s (attempt %d — evaluation):**\n\n%s", entry.AgentAlias, attempt, evalAction.Message))

			if evalAction.Action == "stop" {
				s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
				return
			}
			if evalAction.Action == "replan" {
				s.moveCard(ctx, entry.CardID, card.BoardID, "Todo")
				return
			}
			// "continue" or anything else → next attempt
		}
	}

	// All retries exhausted
	_ = s.postComment(ctx, entry.CardID, fmt.Sprintf("**@%s:** All %d attempts exhausted. Moving to Failed.", entry.AgentAlias, maxAttempts))
	s.moveCard(ctx, entry.CardID, card.BoardID, "Failed")
}

// --- Helper methods ---

type cardData struct {
	ID          string `json:"id"`
	BoardID     string `json:"board_id"`
	ColumnID    string `json:"column_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	Labels      string `json:"labels"`
}

func (s *Scheduler) fetchCard(ctx context.Context, cardID string) (*cardData, error) {
	resp, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "GET", "/cards/"+cardID, nil)
	if err != nil {
		return nil, err
	}
	var card cardData
	if err := json.Unmarshal(resp, &card); err != nil {
		return nil, fmt.Errorf("parse card: %w", err)
	}
	return &card, nil
}

type commentData struct {
	AuthorName string `json:"author_name"`
	Body       string `json:"body"`
	CreatedAt  int64  `json:"created_at"`
}

func (s *Scheduler) fetchComments(ctx context.Context, cardID string) (string, error) {
	resp, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "GET", "/cards/"+cardID+"/comments", nil)
	if err != nil {
		return "", err
	}
	var comments []commentData
	if err := json.Unmarshal(resp, &comments); err != nil {
		return "", err
	}
	if len(comments) == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	for _, c := range comments {
		author := c.AuthorName
		if author == "" {
			author = "system"
		}
		fmt.Fprintf(&buf, "**%s:** %s\n\n", author, c.Body)
	}
	return buf.String(), nil
}

func (s *Scheduler) resolveColumnName(ctx context.Context, card *cardData) string {
	if card.ColumnID == "" {
		return "unknown"
	}
	// Try to get column info from the board
	resp, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "GET", "/boards/"+card.BoardID+"/columns", nil)
	if err != nil {
		return card.ColumnID
	}
	var columns []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(resp, &columns); err != nil {
		return card.ColumnID
	}
	for _, col := range columns {
		if col.ID == card.ColumnID {
			return col.Name
		}
	}
	return card.ColumnID
}

func (s *Scheduler) renderPrompt(data PromptData) (string, error) {
	var buf bytes.Buffer
	if err := s.promptTmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (s *Scheduler) sendToAgent(ctx context.Context, agentAlias, cardID, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]string{
		"source_plugin": "infra-task-scheduler",
		"channel_id":    "dispatch:" + cardID,
		"message":       "@" + agentAlias + " " + prompt,
	})

	resp, err := s.sdk.RouteToPlugin(ctx, "infra-agent-relay", "POST", "/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}

	var relayResp struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(resp, &relayResp); err != nil {
		return string(resp), nil // return raw if can't parse
	}
	return relayResp.Response, nil
}

func (s *Scheduler) parseAgentResponse(raw string) agentResponse {
	cleaned := strings.TrimSpace(raw)

	// Strip markdown code fences (```json ... ``` or ``` ... ```)
	if strings.HasPrefix(cleaned, "```") {
		// Remove opening fence line
		if idx := strings.Index(cleaned, "\n"); idx != -1 {
			cleaned = cleaned[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(cleaned, "```"); idx != -1 {
			cleaned = strings.TrimSpace(cleaned[:idx])
		}
	}

	var resp agentResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		// Agent didn't respond with JSON — treat as a plain reply
		return agentResponse{Action: "reply", Message: raw}
	}
	if resp.Action == "" {
		resp.Action = "reply"
	}
	return resp
}

func (s *Scheduler) postComment(ctx context.Context, cardID, body string) error {
	commentBody, _ := json.Marshal(map[string]string{
		"body": body,
	})
	_, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "POST", "/cards/"+cardID+"/comments", bytes.NewReader(commentBody))
	return err
}

func (s *Scheduler) moveCard(ctx context.Context, cardID, boardID, columnName string) {
	// Resolve column ID by name
	resp, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "GET", "/boards/"+boardID+"/columns", nil)
	if err != nil {
		log.Printf("[dispatch] failed to fetch columns for board %s: %v", boardID, err)
		return
	}
	var columns []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(resp, &columns); err != nil {
		return
	}

	var targetID string
	for _, col := range columns {
		if col.Name == columnName {
			targetID = col.ID
			break
		}
	}
	if targetID == "" {
		log.Printf("[dispatch] column %q not found on board %s", columnName, boardID)
		return
	}

	body, _ := json.Marshal(map[string]string{
		"card_id":   cardID,
		"column_id": targetID,
	})
	_, _ = s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "POST", "/mcp/set_task_state", bytes.NewReader(body))
}

func (s *Scheduler) createNewTask(ctx context.Context, boardID, title, description, agentAlias string) {
	// Find the first column (usually Backlog or Todo)
	resp, err := s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "GET", "/boards/"+boardID+"/columns", nil)
	if err != nil {
		return
	}
	var columns []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(resp, &columns); err != nil || len(columns) == 0 {
		return
	}

	body, _ := json.Marshal(map[string]interface{}{
		"board_id":       boardID,
		"column_id":      columns[0].ID,
		"title":          title,
		"description":    description,
		"assignee_agent": agentAlias,
	})
	_, _ = s.sdk.RouteToPlugin(ctx, "tool-task-tracker", "POST", "/mcp/create_task", bytes.NewReader(body))
	log.Printf("[dispatch] created new task %q on board %s assigned to @%s", title, boardID, agentAlias)
}

func (s *Scheduler) completeDispatch(id, truncatedResponse string) {
	_ = s.db.UpdateDispatchStatus(id, "completed", map[string]interface{}{
		"completed_at":  time.Now().UnixMilli(),
		"agent_response": truncatedResponse,
	})
}

func (s *Scheduler) failDispatch(id, errMsg string) {
	log.Printf("[dispatch] failed: %s", errMsg)
	_ = s.db.UpdateDispatchStatus(id, "failed", map[string]interface{}{
		"completed_at":  time.Now().UnixMilli(),
		"error_message": errMsg,
	})
}
