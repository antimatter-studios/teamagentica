package scheduler

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"text/template"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/events"
	"github.com/antimatter-studios/teamagentica/plugins/infra-task-scheduler/internal/storage"
)

// SDKClient is the interface the scheduler needs from pluginsdk.Client.
type SDKClient interface {
	PublishEvent(eventType, detail string)
	OnEvent(eventType string, debouncer pluginsdk.Debouncer)
	RouteToPlugin(ctx context.Context, pluginID, method, path string, body io.Reader) ([]byte, error)
}

// DispatchConfig holds dispatch queue configuration.
type DispatchConfig struct {
	Enabled        bool
	GlobalLimit    int            // 0 = unlimited
	AgentLimits    map[string]int // per-agent concurrency limits
	PromptTemplate string         // Go text/template for agent prompts
}

// cacheEntry tracks the next fire time for timer jobs.
type cacheEntry struct {
	ID       string
	NextFire time.Time
}

// Scheduler manages both timer and event-based job execution.
type Scheduler struct {
	db               *storage.DB
	sdk              SDKClient
	mu               sync.Mutex
	cache            []cacheEntry          // timer jobs only
	registeredEvents map[string]bool       // event types we've subscribed to
	stopCh           chan struct{}
	dispatch         DispatchConfig
	promptTmpl       *template.Template
}

// New creates a Scheduler, loads jobs from DB, and starts the tick loop.
func New(db *storage.DB, sdk SDKClient, cfg DispatchConfig) *Scheduler {
	s := &Scheduler{
		db:               db,
		sdk:              sdk,
		registeredEvents: make(map[string]bool),
		stopCh:           make(chan struct{}),
		dispatch:         cfg,
	}

	// Parse prompt template
	tmplSrc := cfg.PromptTemplate
	if tmplSrc == "" {
		tmplSrc = defaultPromptTemplate
	}
	t, err := template.New("dispatch").Parse(tmplSrc)
	if err != nil {
		log.Printf("[dispatch] failed to parse prompt template, using default: %v", err)
		t, _ = template.New("dispatch").Parse(defaultPromptTemplate)
	}
	s.promptTmpl = t

	s.reloadTimerCache()
	s.registerEventJobs()
	s.initDispatch()
	go s.run()
	return s
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
}

// Reload rebuilds timer cache and re-registers event subscriptions.
func (s *Scheduler) Reload() {
	s.reloadTimerCache()
	s.registerEventJobs()
}

// --- Timer jobs ---

func (s *Scheduler) reloadTimerCache() {
	jobs, err := s.db.ListTimerJobs()
	if err != nil {
		log.Printf("[scheduler] failed to reload timer cache: %v", err)
		return
	}

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache = make([]cacheEntry, 0, len(jobs))
	for _, j := range jobs {
		nf := time.UnixMilli(j.NextFire)
		if nf.Before(now) {
			recalc, err := NextFireTime(j.Schedule, now)
			if err != nil {
				log.Printf("[scheduler] bad schedule for job %s: %v", j.ID, err)
				continue
			}
			nf = recalc
			_ = s.db.IncrementFired(j.ID, nf.UnixMilli(), false)
		}
		s.cache = append(s.cache, cacheEntry{ID: j.ID, NextFire: nf})
	}
	sortCache(s.cache)
}

func sortCache(c []cacheEntry) {
	sort.Slice(c, func(i, j int) bool {
		return c[i].NextFire.Before(c[j].NextFire)
	})
}

func (s *Scheduler) run() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

func (s *Scheduler) tick(now time.Time) {
	s.mu.Lock()
	var due []cacheEntry
	for len(s.cache) > 0 && !s.cache[0].NextFire.After(now) {
		due = append(due, s.cache[0])
		s.cache = s.cache[1:]
	}
	s.mu.Unlock()

	for _, entry := range due {
		s.fireTimerJob(entry.ID, now)
	}

	// Process dispatch queue
	if s.dispatch.Enabled {
		s.processDispatchQueue()
	}
}

func (s *Scheduler) fireTimerJob(id string, now time.Time) {
	job, err := s.db.GetJob(id)
	if err != nil || !job.Enabled {
		return
	}

	s.fireAndLog(job, "timer", now)

	isOnce := job.Type == "once"
	var nextFire int64
	if !isOnce {
		nf, err := NextFireTime(job.Schedule, now)
		if err != nil {
			log.Printf("[scheduler] bad schedule for job %s on refire: %v", id, err)
			return
		}
		nextFire = nf.UnixMilli()
	}
	_ = s.db.IncrementFired(id, nextFire, isOnce)

	if !isOnce {
		s.mu.Lock()
		s.cache = append(s.cache, cacheEntry{ID: id, NextFire: time.UnixMilli(nextFire)})
		sortCache(s.cache)
		s.mu.Unlock()
	}
}

// --- Event jobs ---

func (s *Scheduler) registerEventJobs() {
	jobs, err := s.db.ListEventJobs()
	if err != nil {
		log.Printf("[scheduler] failed to load event jobs: %v", err)
		return
	}

	for _, j := range jobs {
		pattern := j.EventPattern
		if pattern == "" || s.registeredEvents[pattern] {
			continue
		}

		log.Printf("[scheduler] subscribing to event: %s", pattern)
		handler := s.makeEventHandler(pattern)
		s.sdk.OnEvent(pattern, pluginsdk.NewNullDebouncer(handler))
		s.registeredEvents[pattern] = true
	}
}

func (s *Scheduler) makeEventHandler(pattern string) pluginsdk.EventHandler {
	return func(e pluginsdk.EventCallback) {
		log.Printf("[scheduler] received event %s: %s", e.EventType, e.Detail)

		jobs, err := s.db.ListEventJobsByPattern(pattern)
		if err != nil {
			log.Printf("[scheduler] failed to query event jobs for %s: %v", pattern, err)
			return
		}

		now := time.Now()
		for _, job := range jobs {
			s.fireAndLog(&job, "event:"+e.Detail, now)

			isOnce := job.Type == "once"
			_ = s.db.IncrementFired(job.ID, 0, isOnce)
		}
	}
}

// FireJob manually triggers a job (for the /mcp/trigger_job endpoint).
func (s *Scheduler) FireJob(id string) error {
	job, err := s.db.GetJob(id)
	if err != nil {
		return fmt.Errorf("job not found: %w", err)
	}
	s.fireAndLog(job, "manual", time.Now())
	_ = s.db.IncrementFired(id, job.NextFire, false)
	return nil
}

// --- Shared firing logic ---

func (s *Scheduler) fireAndLog(job *storage.Job, trigger string, now time.Time) {
	log.Printf("[scheduler] FIRED job %s (%s) trigger=%s: %s", job.ID, job.Name, trigger, job.Text)

	_ = s.db.CreateLog(&storage.ExecutionLog{
		JobID:   job.ID,
		JobName: job.Name,
		Text:    job.Text,
		Result:  "ok",
	})

	if s.sdk != nil {
		s.sdk.PublishEvent(events.SchedulerFired, job.Name+": "+job.Text)
	}
}

// --- Schedule Parsing ---

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func ParseSchedule(schedule string, from time.Time) (nextFire time.Time, schedType string, err error) {
	if d, parseErr := time.ParseDuration(schedule); parseErr == nil {
		if d < 1*time.Second {
			return time.Time{}, "", fmt.Errorf("interval must be at least 1s")
		}
		return from.Add(d), "interval", nil
	}
	sched, parseErr := cronParser.Parse(schedule)
	if parseErr != nil {
		return time.Time{}, "", fmt.Errorf("invalid schedule %q: not a Go duration or cron expression", schedule)
	}
	return sched.Next(from), "cron", nil
}

func NextFireTime(schedule string, from time.Time) (time.Time, error) {
	nf, _, err := ParseSchedule(schedule, from)
	return nf, err
}
