// Package daglog provides an in-memory ring buffer for tracking DAG executions
// in the agent relay. It records lifecycle events (start, node transitions,
// completion) and exposes them for the readonly status schema.
package daglog

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// NodeState represents the execution state of a single DAG node.
type NodeState int

const (
	NodePending    NodeState = iota
	NodeRunning
	NodeCompleted
	NodeFailed
)

func (s NodeState) String() string {
	switch s {
	case NodePending:
		return "pending"
	case NodeRunning:
		return "running"
	case NodeCompleted:
		return "completed"
	case NodeFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func (s NodeState) Icon() string {
	switch s {
	case NodePending:
		return "[ ]"
	case NodeRunning:
		return "[>]"
	case NodeCompleted:
		return "[ok]"
	case NodeFailed:
		return "[!!]"
	default:
		return "[?]"
	}
}

// DAGState represents the overall state of a DAG execution.
type DAGState int

const (
	DAGRunning   DAGState = iota
	DAGCompleted
	DAGFailed
)

func (s DAGState) String() string {
	switch s {
	case DAGRunning:
		return "running"
	case DAGCompleted:
		return "completed"
	case DAGFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Node tracks the state of a single task in a DAG.
type Node struct {
	ID        string
	Alias     string
	Tool      string // non-empty if this is a direct tool call
	Prompt    string // the interpolated prompt text sent to the agent/tool
	State     NodeState
	StartedAt time.Time
	DoneAt    time.Time
	Error     string
}

// Entry represents a single DAG execution.
type Entry struct {
	ID          string // task_group_id
	Message     string // truncated user message
	StartedAt   time.Time
	DoneAt      time.Time
	State       DAGState
	Nodes       []Node
	Coordinator string // coordinator alias used
}

// completedCount returns the number of nodes that are completed or failed.
func (e *Entry) completedCount() int {
	n := 0
	for i := range e.Nodes {
		if e.Nodes[i].State == NodeCompleted || e.Nodes[i].State == NodeFailed {
			n++
		}
	}
	return n
}

// elapsed returns the duration since start (or total duration if done).
func (e *Entry) elapsed() time.Duration {
	if !e.DoneAt.IsZero() {
		return e.DoneAt.Sub(e.StartedAt)
	}
	return time.Since(e.StartedAt)
}

// aliasChain returns a compact representation of the DAG aliases.
func (e *Entry) aliasChain() string {
	var parts []string
	for i := range e.Nodes {
		parts = append(parts, "@"+e.Nodes[i].Alias)
	}
	return strings.Join(parts, " > ")
}

// FormatSummary returns a compact one-line status for schema display.
func (e *Entry) FormatSummary() string {
	done := e.completedCount()
	total := len(e.Nodes)
	elapsed := e.elapsed()

	var stateIcon string
	switch e.State {
	case DAGRunning:
		// Find the currently running node.
		var running string
		for i := range e.Nodes {
			if e.Nodes[i].State == NodeRunning {
				running = "@" + e.Nodes[i].Alias
				break
			}
		}
		if running != "" {
			stateIcon = fmt.Sprintf("> %d/%d | %.1fs | %s running", done, total, elapsed.Seconds(), running)
		} else {
			stateIcon = fmt.Sprintf("> %d/%d | %.1fs", done, total, elapsed.Seconds())
		}
	case DAGCompleted:
		stateIcon = fmt.Sprintf("ok %d/%d | %.1fs | %s", done, total, elapsed.Seconds(), e.aliasChain())
	case DAGFailed:
		// Find the failed node.
		var failedAlias string
		for i := range e.Nodes {
			if e.Nodes[i].State == NodeFailed {
				failedAlias = "@" + e.Nodes[i].Alias
				break
			}
		}
		stateIcon = fmt.Sprintf("!! %d/%d | %.1fs | failed at %s", done, total, elapsed.Seconds(), failedAlias)
	}

	return stateIcon
}

// FormatKey returns the display key for this entry (time + full message).
func (e *Entry) FormatKey() string {
	ts := e.StartedAt.Format("3:04:05 PM")
	msg := e.Message
	return fmt.Sprintf("%s %s", ts, msg)
}

const (
	maxEntries = 200
	maxAge     = 1 * time.Hour
)

// Log is a thread-safe ring buffer of recent DAG executions.
type Log struct {
	mu      sync.RWMutex
	entries []*Entry
}

// New creates a new DAG log.
func New() *Log {
	return &Log{}
}

// Start records a new DAG execution. Returns the entry for subsequent updates.
func (l *Log) Start(id, message, coordinator string, tasks []TaskDef) *Entry {
	nodes := make([]Node, len(tasks))
	for i, t := range tasks {
		nodes[i] = Node{
			ID:    t.ID,
			Alias: t.Alias,
			Tool:  t.Tool,
			State: NodePending,
		}
	}

	entry := &Entry{
		ID:          id,
		Message:     message,
		StartedAt:   time.Now(),
		State:       DAGRunning,
		Nodes:       nodes,
		Coordinator: coordinator,
	}

	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.prune()
	l.mu.Unlock()

	return entry
}

// TaskDef is the minimal task info needed to initialize a DAG log entry.
type TaskDef struct {
	ID    string
	Alias string
	Tool  string
}

// NodeStarted marks a node as running and records the prompt sent to it.
func (l *Log) NodeStarted(entry *Entry, nodeID, prompt string) {
	l.mu.Lock()
	for i := range entry.Nodes {
		if entry.Nodes[i].ID == nodeID {
			entry.Nodes[i].State = NodeRunning
			entry.Nodes[i].StartedAt = time.Now()
			entry.Nodes[i].Prompt = prompt
			break
		}
	}
	l.mu.Unlock()
}

// NodeCompleted marks a node as completed.
func (l *Log) NodeCompleted(entry *Entry, nodeID string) {
	l.mu.Lock()
	for i := range entry.Nodes {
		if entry.Nodes[i].ID == nodeID {
			entry.Nodes[i].State = NodeCompleted
			entry.Nodes[i].DoneAt = time.Now()
			break
		}
	}
	l.mu.Unlock()
}

// NodeFailed marks a node as failed with an error message.
func (l *Log) NodeFailed(entry *Entry, nodeID, errMsg string) {
	l.mu.Lock()
	for i := range entry.Nodes {
		if entry.Nodes[i].ID == nodeID {
			entry.Nodes[i].State = NodeFailed
			entry.Nodes[i].DoneAt = time.Now()
			entry.Nodes[i].Error = errMsg
			break
		}
	}
	l.mu.Unlock()
}

// Complete marks the entire DAG as completed.
func (l *Log) Complete(entry *Entry) {
	l.mu.Lock()
	entry.State = DAGCompleted
	entry.DoneAt = time.Now()
	l.mu.Unlock()
}

// Fail marks the entire DAG as failed.
func (l *Log) Fail(entry *Entry) {
	l.mu.Lock()
	entry.State = DAGFailed
	entry.DoneAt = time.Now()
	l.mu.Unlock()
}

// NodeSummary is a structured representation of a single DAG node for schema display.
type NodeSummary struct {
	ID         string  `json:"id"`
	Alias      string  `json:"alias"`
	Tool       string  `json:"tool,omitempty"`
	Prompt     string  `json:"prompt,omitempty"`
	State      string  `json:"state"` // "pending", "running", "completed", "failed"
	DurationMs float64 `json:"duration_ms,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// DAGSummary is a structured representation of a DAG entry for schema display.
type DAGSummary struct {
	ID      string        `json:"id"`
	Time    string        `json:"time"`
	Message string        `json:"message"`
	State   string        `json:"state"`   // "running", "completed", "failed"
	Summary string        `json:"summary"` // compact one-line status
	Nodes   []NodeSummary `json:"nodes"`   // individual steps in the DAG
}

// AllDags returns all DAGs (active first, then recent) as a structured list.
func (l *Log) AllDags() []DAGSummary {
	l.mu.RLock()
	defer l.mu.RUnlock()

	cutoff := time.Now().Add(-maxAge)
	var active, recent []DAGSummary

	for _, e := range l.entries {
		if !e.StartedAt.After(cutoff) && e.State != DAGRunning {
			continue
		}
		nodes := make([]NodeSummary, len(e.Nodes))
		for j, n := range e.Nodes {
			ns := NodeSummary{
				ID:     n.ID,
				Alias:  n.Alias,
				Tool:   n.Tool,
				Prompt: n.Prompt,
				State:  n.State.String(),
				Error:  n.Error,
			}
			if !n.StartedAt.IsZero() {
				end := n.DoneAt
				if end.IsZero() {
					end = time.Now()
				}
				ns.DurationMs = float64(end.Sub(n.StartedAt).Milliseconds())
			}
			nodes[j] = ns
		}
		s := DAGSummary{
			ID:      e.ID,
			Time:    e.StartedAt.Format("3:04:05 PM"),
			Message: e.Message,
			State:   e.State.String(),
			Summary: e.FormatSummary(),
			Nodes:   nodes,
		}
		if e.State == DAGRunning {
			active = append(active, s)
		} else {
			recent = append(recent, s)
		}
	}

	// Active first, then recent (newest first).
	result := make([]DAGSummary, 0, len(active)+len(recent))
	result = append(result, active...)
	// Reverse recent so newest is first.
	for i := len(recent) - 1; i >= 0; i-- {
		result = append(result, recent[i])
	}
	return result
}

// prune removes entries older than maxAge and trims to maxEntries.
// Must be called with l.mu held.
func (l *Log) prune() {
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range l.entries {
		if e.StartedAt.After(cutoff) {
			l.entries[n] = e
			n++
		}
	}
	l.entries = l.entries[:n]

	if len(l.entries) > maxEntries {
		l.entries = l.entries[len(l.entries)-maxEntries:]
	}
}
