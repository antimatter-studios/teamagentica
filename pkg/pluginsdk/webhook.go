package pluginsdk

import (
	"encoding/json"
	"log"
)

// RegisterWebhook registers this plugin's webhook route with the webhook ingress.
// prefix is the URL prefix the ingress will match (e.g. "/tool-seedance").
// Also subscribes to webhook:ready so the route is re-registered if the ingress restarts.
func (c *Client) RegisterWebhook(prefix string) {
	// Ensure prefix starts with /
	if prefix == "" || prefix[0] != '/' {
		prefix = "/" + prefix
	}

	pluginID := c.registration.ID
	hostname := c.registration.Host
	port := c.registration.Port

	send := func() {
		payload, _ := json.Marshal(map[string]interface{}{
			"plugin_id":   pluginID,
			"prefix":      prefix,
			"target_host": hostname,
			"target_port": port,
		})
		c.PublishEventTo("webhook:api:update", string(payload), "network-webhook")
		log.Printf("pluginsdk: sent webhook route to ingress: prefix=%s target=%s:%d", prefix, hostname, port)
	}

	// Subscribe to webhook:ready so we re-register when the ingress (re)starts.
	c.Events().On("webhook:ready", NewNullDebouncer(func(event EventCallback) {
		log.Printf("pluginsdk: webhook:ready received — registering route")
		send()
	}))
}

// OnWebhookURL registers a callback that fires when the webhook ingress sends
// this plugin its public webhook URL. The callback receives the full URL
// (e.g. "https://abc.ngrok.io/tool-seedance").
func (c *Client) OnWebhookURL(fn func(webhookURL string)) {
	c.Events().On("webhook:plugin:url", NewNullDebouncer(func(event EventCallback) {
		var data struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.Unmarshal([]byte(event.Detail), &data); err != nil {
			log.Printf("pluginsdk: failed to parse webhook:plugin:url: %v", err)
			return
		}
		if data.WebhookURL == "" {
			log.Printf("pluginsdk: webhook:plugin:url has empty URL")
			return
		}
		log.Printf("pluginsdk: received webhook URL: %s", data.WebhookURL)
		fn(data.WebhookURL)
	}))
}
