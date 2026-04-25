package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

// Target sentinels.
const (
	TargetWebhook = "webhook" // resolved at start from webhook:ready broadcast
)

// Role sentinels.
const (
	RoleIngress = "ingress" // emits ingress:ready when running
)

// Spec describes a single named tunnel as persisted in config.
type Spec struct {
	Name      string            `json:"name"`
	Driver    string            `json:"driver"`
	AutoStart bool              `json:"auto_start"`
	Role      string            `json:"role,omitempty"`
	Target    string            `json:"target"` // "webhook" | "host:port"
	Config    map[string]string `json:"config,omitempty"`
}

// TunnelView is a read-only snapshot of a tunnel for the HTTP API.
type TunnelView struct {
	Spec   Spec            `json:"spec"`
	Status drivers.Status  `json:"status"`
}

// ReadyFn is called when a tunnel transitions to running. role is the tunnel's
// role ("" if unset), url is the public URL reported by the driver.
type ReadyFn func(name, role, url string)

type tunnel struct {
	spec   Spec
	driver drivers.Driver
}

type Manager struct {
	mu            sync.RWMutex
	tunnels       map[string]*tunnel
	order         []string
	webhookTarget string
	onReady       ReadyFn
}

func New() *Manager {
	return &Manager{tunnels: map[string]*tunnel{}}
}

// OnReady sets the callback invoked when a tunnel starts successfully.
func (m *Manager) OnReady(fn ReadyFn) {
	m.mu.Lock()
	m.onReady = fn
	m.mu.Unlock()
}

// SetWebhookTarget updates the target used by tunnels whose Target is
// TargetWebhook. Returns true if the value changed.
func (m *Manager) SetWebhookTarget(target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webhookTarget == target {
		return false
	}
	m.webhookTarget = target
	return true
}

// WebhookTarget returns the currently known webhook target.
func (m *Manager) WebhookTarget() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.webhookTarget
}

// ApplySpecs reconciles the manager to the given list of specs. Tunnels not in
// the new list are stopped and removed. Tunnels that already exist with the
// same spec are left running. Tunnels with a changed spec are restarted.
func (m *Manager) ApplySpecs(ctx context.Context, specs []Spec) {
	wanted := map[string]Spec{}
	for _, s := range specs {
		if s.Name == "" {
			log.Printf("[tunnel-manager] skipping spec with empty name")
			continue
		}
		wanted[s.Name] = s
	}

	m.mu.Lock()
	// Remove/stop tunnels no longer wanted.
	for name, t := range m.tunnels {
		if _, ok := wanted[name]; ok {
			continue
		}
		_ = t.driver.Stop(ctx)
		delete(m.tunnels, name)
	}
	// Add or replace tunnels.
	for name, spec := range wanted {
		existing, ok := m.tunnels[name]
		if ok && specsEqual(existing.spec, spec) {
			continue
		}
		if ok {
			_ = existing.driver.Stop(ctx)
			delete(m.tunnels, name)
		}
		m.tunnels[name] = &tunnel{spec: spec}
	}
	m.order = orderedNames(wanted)
	m.mu.Unlock()

	if ctx != nil {
		m.startAutoStart(ctx)
	}
}

// startAutoStart starts every tunnel with AutoStart=true whose target is known.
func (m *Manager) startAutoStart(ctx context.Context) {
	m.mu.RLock()
	names := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, name := range names {
		m.mu.RLock()
		t := m.tunnels[name]
		m.mu.RUnlock()
		if t == nil || !t.spec.AutoStart {
			continue
		}
		if _, err := m.Start(ctx, name); err != nil {
			log.Printf("[tunnel-manager] auto-start %q: %v", name, err)
		}
	}
}

// Start starts the named tunnel. Idempotent: if already running, returns its
// current status. If the spec targets the webhook and the webhook plugin has
// not been discovered yet, returns an error and leaves the tunnel stopped.
func (m *Manager) Start(ctx context.Context, name string) (drivers.Status, error) {
	m.mu.Lock()
	t := m.tunnels[name]
	if t == nil {
		m.mu.Unlock()
		return drivers.Status{}, fmt.Errorf("tunnel %q not found", name)
	}
	target, err := m.resolveTargetLocked(t.spec)
	if err != nil {
		m.mu.Unlock()
		return drivers.Status{}, err
	}
	if t.driver == nil {
		d, err := drivers.Build(t.spec.Driver, target, t.spec.Config)
		if err != nil {
			m.mu.Unlock()
			return drivers.Status{}, fmt.Errorf("build %s: %w", t.spec.Driver, err)
		}
		t.driver = d
	}
	driver := t.driver
	spec := t.spec
	onReady := m.onReady
	m.mu.Unlock()

	st, err := driver.Start(ctx)
	if err != nil {
		return st, err
	}
	if onReady != nil && st.State == drivers.StateRunning {
		onReady(spec.Name, spec.Role, st.URL)
	}
	return st, nil
}

// AddOrReplace inserts (or replaces) a single tunnel spec at runtime. If a
// tunnel with the same name already exists and its spec differs, the old
// driver is stopped first. If AutoStart is true the new tunnel is started
// immediately; otherwise it stays stopped until Start is called.
func (m *Manager) AddOrReplace(ctx context.Context, spec Spec) (TunnelView, error) {
	if spec.Name == "" {
		return TunnelView{}, fmt.Errorf("spec.name required")
	}
	if spec.Driver == "" {
		return TunnelView{}, fmt.Errorf("spec.driver required")
	}

	m.mu.Lock()
	if existing, ok := m.tunnels[spec.Name]; ok {
		if specsEqual(existing.spec, spec) {
			m.mu.Unlock()
			if spec.AutoStart {
				if _, err := m.Start(ctx, spec.Name); err != nil {
					log.Printf("[tunnel-manager] start %q: %v", spec.Name, err)
				}
			}
			v, _ := m.Get(spec.Name)
			return v, nil
		}
		if existing.driver != nil {
			_ = existing.driver.Stop(ctx)
		}
		delete(m.tunnels, spec.Name)
	}
	m.tunnels[spec.Name] = &tunnel{spec: spec}
	m.order = orderedNamesFromMap(m.tunnels)
	m.mu.Unlock()

	if spec.AutoStart {
		if _, err := m.Start(ctx, spec.Name); err != nil {
			return TunnelView{}, fmt.Errorf("start %q: %w", spec.Name, err)
		}
	}
	v, _ := m.Get(spec.Name)
	return v, nil
}

// Remove stops and deletes the named tunnel. Idempotent: returns nil if the
// tunnel doesn't exist.
func (m *Manager) Remove(ctx context.Context, name string) error {
	m.mu.Lock()
	t, ok := m.tunnels[name]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	driver := t.driver
	delete(m.tunnels, name)
	m.order = orderedNamesFromMap(m.tunnels)
	m.mu.Unlock()
	if driver != nil {
		return driver.Stop(ctx)
	}
	return nil
}

// Stop stops the named tunnel. Idempotent: if already stopped, returns nil.
func (m *Manager) Stop(ctx context.Context, name string) error {
	m.mu.RLock()
	t := m.tunnels[name]
	m.mu.RUnlock()
	if t == nil {
		return fmt.Errorf("tunnel %q not found", name)
	}
	m.mu.Lock()
	driver := t.driver
	t.driver = nil
	m.mu.Unlock()
	if driver == nil {
		return nil
	}
	return driver.Stop(ctx)
}

// List returns a snapshot of all known tunnels.
func (m *Manager) List() []TunnelView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TunnelView, 0, len(m.order))
	for _, name := range m.order {
		t := m.tunnels[name]
		if t == nil {
			continue
		}
		view := TunnelView{Spec: t.spec, Status: drivers.Status{State: drivers.StateStopped}}
		if t.driver != nil {
			view.Status = t.driver.Status()
		}
		out = append(out, view)
	}
	return out
}

// Get returns a single tunnel view by name.
func (m *Manager) Get(name string) (TunnelView, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t := m.tunnels[name]
	if t == nil {
		return TunnelView{}, false
	}
	view := TunnelView{Spec: t.spec, Status: drivers.Status{State: drivers.StateStopped}}
	if t.driver != nil {
		view.Status = t.driver.Status()
	}
	return view, true
}

// StartTunnelsTargetingWebhook starts every AutoStart tunnel whose target is
// the webhook sentinel. Intended for the webhook:ready callback — when the
// webhook plugin is (re)discovered, any ingress tunnels can now come up.
func (m *Manager) StartTunnelsTargetingWebhook(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0)
	for _, name := range m.order {
		t := m.tunnels[name]
		if t != nil && t.spec.AutoStart && t.spec.Target == TargetWebhook {
			names = append(names, name)
		}
	}
	m.mu.RUnlock()
	for _, name := range names {
		if _, err := m.Start(ctx, name); err != nil {
			log.Printf("[tunnel-manager] restart-on-webhook %q: %v", name, err)
		}
	}
}

// StopAll stops every tunnel. Used at shutdown.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.RLock()
	names := append([]string(nil), m.order...)
	m.mu.RUnlock()
	for _, n := range names {
		if err := m.Stop(ctx, n); err != nil {
			log.Printf("[tunnel-manager] stop %q: %v", n, err)
		}
	}
}

func (m *Manager) resolveTargetLocked(s Spec) (string, error) {
	if s.Target == TargetWebhook {
		if m.webhookTarget == "" {
			return "", fmt.Errorf("tunnel %q targets webhook but webhook plugin not discovered yet", s.Name)
		}
		return m.webhookTarget, nil
	}
	if s.Target == "" {
		return "", fmt.Errorf("tunnel %q has no target", s.Name)
	}
	return s.Target, nil
}

// ParseSpecs parses the TUNNELS config value (JSON array) into specs.
func ParseSpecs(raw string) ([]Spec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []Spec
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse TUNNELS: %w", err)
	}
	return out, nil
}

func specsEqual(a, b Spec) bool {
	if a.Name != b.Name || a.Driver != b.Driver || a.AutoStart != b.AutoStart || a.Role != b.Role || a.Target != b.Target {
		return false
	}
	if len(a.Config) != len(b.Config) {
		return false
	}
	for k, v := range a.Config {
		if b.Config[k] != v {
			return false
		}
	}
	return true
}

func orderedNames(specs map[string]Spec) []string {
	out := make([]string, 0, len(specs))
	for name := range specs {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func orderedNamesFromMap(m map[string]*tunnel) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sortStrings(out)
	return out
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
