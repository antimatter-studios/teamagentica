package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

// EventClient manages event publishing and subscription for a plugin.
// It provides an outbox that queues events when the event bus is unavailable
// and flushes them when it comes online.
//
// Usage:
//
//	ec := sdkClient.Events()
//	ec.On("relay:progress", pluginsdk.NewNullDebouncer(handleProgress))
//	ec.On("config:update", pluginsdk.NewNullDebouncer(handleConfig))
//	ec.Publish("debug:info", "something happened")
//	ec.PublishTo("usage:report", detail, "infra-cost-tracking")
type EventClient struct {
	sdk   *Client
	ready bool

	mu           sync.Mutex
	outbox       []outboxEntry
	handlers     map[string]Debouncer
	busAvailable chan struct{} // closed when the event bus is first available
}

// outboxEntry is a queued event waiting for the bus to become available.
type outboxEntry struct {
	EventType   string
	Detail      string
	Destination string // empty for broadcast
}

// NewEventClient creates an EventClient bound to the given SDK client.
// It automatically detects when the event bus (infra:events) becomes available,
// flushes queued events, and subscribes all registered handlers.
func NewEventClient(sdk *Client) *EventClient {
	ec := &EventClient{
		sdk:          sdk,
		handlers:     make(map[string]Debouncer),
		busAvailable: make(chan struct{}),
	}

	// When the event bus comes online (or restarts), flush outbox and subscribe.
	sdk.OnPluginAvailable("infra:events", func(p PluginInfo) {
		ec.mu.Lock()
		ec.ready = true
		// Signal first availability.
		select {
		case <-ec.busAvailable:
		default:
			close(ec.busAvailable)
		}
		ec.mu.Unlock()

		ec.flushOutbox()
		ec.subscribeAll()
	})

	return ec
}

// On registers a handler for the given event type. The handler receives events
// pushed to this plugin's POST /events endpoint by the event bus.
//
// If the event bus is already available, the subscription is sent immediately.
// Otherwise it is deferred until the bus comes online.
func (ec *EventClient) On(eventType string, debouncer Debouncer) {
	// Register the handler for local dispatch (EventHandler in helpers.go).
	ec.sdk.OnEvent(eventType, debouncer)

	// Track for subscription.
	ec.mu.Lock()
	ec.handlers[eventType] = debouncer
	ready := ec.ready
	ec.mu.Unlock()

	// Subscribe immediately if bus is already available.
	if ready {
		ec.sdk.SubscribeEvent(eventType)
	}
}

// Publish sends a broadcast event to the event bus. If the bus is not yet
// available, the event is queued in the outbox and flushed when the bus
// comes online.
func (ec *EventClient) Publish(eventType, detail string) {
	ec.mu.Lock()
	if !ec.ready {
		ec.outbox = append(ec.outbox, outboxEntry{
			EventType: eventType,
			Detail:    detail,
		})
		ec.mu.Unlock()
		return
	}
	ec.mu.Unlock()

	ec.publish(eventType, detail, "")
}

// PublishTo sends an addressed event to a specific plugin via the event bus.
// If the bus is not yet available, the event is queued in the outbox.
func (ec *EventClient) PublishTo(eventType, detail, target string) {
	ec.mu.Lock()
	if !ec.ready {
		ec.outbox = append(ec.outbox, outboxEntry{
			EventType:   eventType,
			Detail:      detail,
			Destination: target,
		})
		ec.mu.Unlock()
		return
	}
	ec.mu.Unlock()

	ec.publish(eventType, detail, target)
}

// flushOutbox drains all queued events to the event bus.
func (ec *EventClient) flushOutbox() {
	ec.mu.Lock()
	entries := ec.outbox
	ec.outbox = nil
	ec.mu.Unlock()

	if len(entries) > 0 {
		log.Printf("pluginsdk: flushing %d queued events to event bus", len(entries))
	}

	for _, e := range entries {
		ec.publish(e.EventType, e.Detail, e.Destination)
	}
}

// subscribeAll subscribes all registered event handlers to the event bus.
func (ec *EventClient) subscribeAll() {
	ec.mu.Lock()
	types := make([]string, 0, len(ec.handlers))
	for t := range ec.handlers {
		types = append(types, t)
	}
	ec.mu.Unlock()

	for _, t := range types {
		ec.sdk.SubscribeEvent(t)
	}
}

// publish sends an event to the event bus via infra-redis.
func (ec *EventClient) publish(eventType, detail, destination string) {
	payload := map[string]interface{}{
		"event_type": eventType,
		"source":     ec.sdk.registration.ID,
		"detail":     detail,
	}
	if destination != "" {
		payload["target"] = destination
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("pluginsdk: event publish marshal error: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := ec.sdk.RouteToPlugin(ctx, "infra-redis", "POST", "/events/publish", bytes.NewReader(body)); err != nil {
		log.Printf("pluginsdk: event publish failed (%s): %v", eventType, err)
	}
}
