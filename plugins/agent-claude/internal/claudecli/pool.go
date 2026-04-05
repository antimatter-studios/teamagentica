package claudecli

import (
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	defaultIdleTTL = 2 * time.Minute
	defaultHotSize = 2
	maxHotSize     = 10
	reaperInterval = 60 * time.Second
	warmupInterval = 10 * time.Second
	warmThreshold  = 0.8 // trigger warm-up when hot pool drops to 80% or below
)

// Pool manages a set of persistent Claude CLI subprocesses.
//
// Each process is started with a unique --session-id so Claude Code persists
// its session to disk. Active processes are mapped to conversation IDs.
// A hot pool of pre-warmed processes eliminates cold start for new conversations.
//
// When a conversation's process is reaped (idle TTL), the session mapping is
// preserved. If the same conversation sends another message, a new process is
// started with the original --session-id, and Claude Code resumes the session
// from disk with full context.
type Pool struct {
	mu     sync.Mutex
	client *Client

	// active maps conversationID → assigned process + metadata.
	active map[string]*poolEntry

	// sessions maps conversationID → session UUID (survives process reaping).
	sessions map[string]string

	// hot holds pre-warmed processes ready for assignment.
	hot []*process

	// config template used for processes (session ID is overridden per-process).
	baseCfg processConfig

	idleTTL time.Duration
	hotSize int // initial/target hot pool size (always 2)
	maxSize int // max hot pool size (configurable, minimum 2)
	closed  bool
	stopCh  chan struct{}
}

type poolEntry struct {
	proc      *process
	sessionID string
	lastUsed  time.Time
}

// NewPool creates a process pool with the given base config.
// Hot pool processes are each started with a unique session ID.
func NewPool(client *Client, cfg processConfig, poolMax int, idleTTL time.Duration) *Pool {
	if poolMax < defaultHotSize {
		poolMax = maxHotSize
	}
	if idleTTL <= 0 {
		idleTTL = defaultIdleTTL
	}
	p := &Pool{
		client:   client,
		active:   make(map[string]*poolEntry),
		sessions: make(map[string]string),
		baseCfg:  cfg,
		idleTTL:  idleTTL,
		hotSize:  defaultHotSize, // always start with 2
		maxSize:  poolMax,
		stopCh:   make(chan struct{}),
	}

	// Start background goroutines.
	go p.reaper()
	go p.warmer()

	return p
}

// startWithSession creates a process using the base config but with the given session ID.
func (p *Pool) startWithSession(sessionID string) (*process, error) {
	cfg := p.baseCfg
	cfg.sessionID = sessionID
	return p.client.startProcess(cfg)
}

// Acquire returns a process for the given conversation ID.
// If one is already active, it's reused. If a previous session exists for this
// conversation but the process was reaped, a new process is started with the
// same session ID (Claude resumes from disk). Otherwise a hot process is assigned.
func (p *Pool) Acquire(conversationID string) (*process, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 1. Check active map — process still alive for this conversation.
	if entry, ok := p.active[conversationID]; ok {
		if entry.proc.alive {
			entry.lastUsed = time.Now()
			return entry.proc, nil
		}
		// Dead process — clean up but preserve session mapping.
		delete(p.active, conversationID)
		log.Printf("[pool] dead process for %s removed (session %s preserved)", conversationID, entry.sessionID)
	}

	// 2. Check if we have a previous session for this conversation (process was reaped).
	//    Start a new process with the same session ID to resume context.
	if sid, ok := p.sessions[conversationID]; ok {
		log.Printf("[pool] resuming session %s for %s", sid, conversationID)
		proc, err := p.startWithSession(sid)
		if err != nil {
			return nil, err
		}
		p.active[conversationID] = &poolEntry{proc: proc, sessionID: sid, lastUsed: time.Now()}
		return proc, nil
	}

	// 3. New conversation — grab from hot pool.
	for len(p.hot) > 0 {
		proc := p.hot[len(p.hot)-1]
		p.hot = p.hot[:len(p.hot)-1]
		if proc.alive {
			sid := proc.cfg.sessionID
			p.sessions[conversationID] = sid
			p.active[conversationID] = &poolEntry{proc: proc, sessionID: sid, lastUsed: time.Now()}
			log.Printf("[pool] assigned hot process pid=%d session=%s to %s (hot remaining=%d)",
				proc.cmd.Process.Pid, sid, conversationID, len(p.hot))
			p.triggerWarmIfNeeded()
			return proc, nil
		}
		// Dead hot process, skip it.
	}

	// 4. Cold start — create new process with fresh session.
	sid := uuid.New().String()
	log.Printf("[pool] no hot processes, cold-starting session=%s for %s", sid, conversationID)
	proc, err := p.startWithSession(sid)
	if err != nil {
		return nil, err
	}
	p.sessions[conversationID] = sid
	p.active[conversationID] = &poolEntry{proc: proc, sessionID: sid, lastUsed: time.Now()}
	go p.warmUp()
	return proc, nil
}

// Release marks a process as done with its current request (updates lastUsed).
// The process stays in the active map for reuse by the same conversation.
func (p *Pool) Release(conversationID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.active[conversationID]; ok {
		entry.lastUsed = time.Now()
	}
}

// MarkDead removes a dead process from the active map.
// The session mapping is preserved so the session can be resumed.
func (p *Pool) MarkDead(conversationID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.active[conversationID]; ok {
		if !entry.proc.alive {
			delete(p.active, conversationID)
		}
	}
}

// Shutdown kills all processes and stops background goroutines.
func (p *Pool) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}
	p.closed = true
	close(p.stopCh)

	for id, entry := range p.active {
		entry.proc.kill()
		delete(p.active, id)
	}
	for _, proc := range p.hot {
		proc.kill()
	}
	p.hot = nil

	log.Printf("[pool] shutdown complete")
}

// Stats returns current pool statistics for debugging.
func (p *Pool) Stats() (active, hot, sessions int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.active), len(p.hot), len(p.sessions)
}

// reaper periodically kills idle active processes that haven't been used
// within the TTL. Session mappings are preserved for resumption.
func (p *Pool) reaper() {
	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.reapIdle()
		}
	}
}

func (p *Pool) reapIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for id, entry := range p.active {
		if !entry.proc.alive {
			delete(p.active, id)
			log.Printf("[pool] reaped dead process for %s (session %s preserved)", id, entry.sessionID)
			continue
		}
		// TODO: TTL-based reaping disabled — requests routinely take 2-3+ minutes.
		// Re-enable with inUse guard once we have proper idle detection.
		_ = now
	}

	// Also clean dead hot processes.
	alive := p.hot[:0]
	for _, proc := range p.hot {
		if proc.alive {
			alive = append(alive, proc)
		}
	}
	p.hot = alive
}

// warmer ensures the hot pool stays topped up.
func (p *Pool) warmer() {
	p.warmUp()

	ticker := time.NewTicker(warmupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.warmUp()
		}
	}
}

// triggerWarmIfNeeded kicks off an async warm-up if the hot pool has dropped
// to 80% or below its target size. Caller must hold p.mu.
func (p *Pool) triggerWarmIfNeeded() {
	threshold := int(float64(p.hotSize) * warmThreshold)
	if len(p.hot) <= threshold {
		go p.warmUp()
	}
}

func (p *Pool) warmUp() {
	p.mu.Lock()
	target := p.hotSize
	if target > p.maxSize {
		target = p.maxSize
	}
	need := target - len(p.hot)
	maxAllowed := p.maxSize
	closed := p.closed
	p.mu.Unlock()

	if closed || need <= 0 {
		return
	}

	for i := 0; i < need; i++ {
		sid := uuid.New().String()
		proc, err := p.startWithSession(sid)
		if err != nil {
			log.Printf("[pool] failed to warm process: %v", err)
			return
		}

		p.mu.Lock()
		if p.closed || len(p.hot) >= maxAllowed {
			p.mu.Unlock()
			proc.kill()
			return
		}
		p.hot = append(p.hot, proc)
		log.Printf("[pool] warmed process pid=%d session=%s (hot=%d/%d)",
			proc.cmd.Process.Pid, sid, len(p.hot), target)
		p.mu.Unlock()
	}
}
