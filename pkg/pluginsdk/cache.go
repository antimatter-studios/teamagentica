package pluginsdk

import (
	"context"
	"fmt"
	"log"

	"github.com/redis/go-redis/v9"
)

// connectCache creates a cache client for the given plugin info.
func connectCache(p PluginInfo) (*redis.Client, error) {
	host := fmt.Sprintf("teamagentica-plugin-%s", p.ID)
	client := redis.NewClient(&redis.Options{
		Addr: host + ":6379",
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("cache ping failed: %w", err)
	}
	return client, nil
}

// CacheClient discovers the memory:cache plugin and returns a connected cache
// client immediately if available (nil otherwise).
//
// If onConnect is non-nil, it is called whenever the cache plugin registers or
// re-registers (e.g. after a restart). The callback receives a fresh client and
// it is the caller's responsibility to swap it in thread-safely.
//
// Usage:
//
//	cache := sdk.CacheClient(func(c *redis.Client) {
//	    bot.SetCache(c) // caller handles thread-safe swap
//	})
func (c *Client) CacheClient(onConnect func(*redis.Client)) *redis.Client {
	var immediate *redis.Client

	// Try to connect right now.
	plugins, err := c.SearchPlugins("memory:cache")
	if err == nil && len(plugins) > 0 {
		if client, err := connectCache(plugins[0]); err == nil {
			immediate = client
			if onConnect != nil {
				onConnect(client)
			}
		}
	}

	// Listen for future (re)registrations.
	if onConnect != nil {
		c.OnPluginAvailable("memory:cache", func(p PluginInfo) {
			client, err := connectCache(p)
			if err != nil {
				log.Printf("pluginsdk: cache connect failed: %v", err)
				return
			}
			onConnect(client)
		})
	}

	return immediate
}
