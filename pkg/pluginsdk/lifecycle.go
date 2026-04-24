package pluginsdk

import "time"

// defaultLifecycleDebounce is long enough to coalesce a kernel-restart burst
// (registry-sync + many plugin:ready events fire within a few hundred ms) but
// short enough that humans don't notice the settle window.
const defaultLifecycleDebounce = 500 * time.Millisecond

// lifecycleEventTypes are the kernel-emitted plugin lifecycle events worth
// listening to when maintaining a secondary cache that references other
// plugins (alias → plugin maps, routing tables, capability-derived state).
// plugin:started is omitted — the container is booting, not yet addressable;
// plugin:ready is the useful signal.
var lifecycleEventTypes = []string{
	"plugin:ready",
	"plugin:stopped",
	"plugin:healthy",
	"plugin:unhealthy",
}

// OnPluginLifecycleSettled registers a callback that fires once after a burst
// of plugin lifecycle events settles. Uses the default debounce window
// (500ms) — suitable for refreshing caches that reference other plugins
// without thrashing during a kernel restart or bulk plugin install.
//
// This is the right helper for: alias maps, routing tables, capability
// caches, any view of "which plugins provide what" that's maintained
// alongside the SDK's built-in peer registry.
func (c *Client) OnPluginLifecycleSettled(fn func()) {
	c.OnPluginLifecycleSettledAfter(defaultLifecycleDebounce, fn)
}

// OnPluginLifecycleSettledAfter is like OnPluginLifecycleSettled but lets
// the caller pick a different debounce window. Use a larger window for
// expensive rebuilds (e.g. 2s for a full config re-fetch); smaller windows
// offer tighter freshness at the cost of more rebuilds per burst.
func (c *Client) OnPluginLifecycleSettledAfter(debounce time.Duration, fn func()) {
	d := NewTimedDebouncer(debounce, func(EventCallback) { fn() })
	for _, t := range lifecycleEventTypes {
		c.Events().On(t, d)
	}
}

// OnPluginLifecycleChange registers a callback that fires for every plugin
// lifecycle event with no debounce. The callback receives the raw event so
// it can inspect which plugin changed and in what direction. Use only for
// cheap operations — for anything that rebuilds state, prefer
// OnPluginLifecycleSettled which naturally coalesces bursts.
func (c *Client) OnPluginLifecycleChange(fn func(event EventCallback)) {
	d := NewNullDebouncer(fn)
	for _, t := range lifecycleEventTypes {
		c.Events().On(t, d)
	}
}
