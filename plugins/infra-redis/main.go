package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/plugins/infra-redis/internal/handlers"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	const defaultPort = 8081

	debug := os.Getenv("PLUGIN_DEBUG") == "true"

	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	sdkCfg := pluginsdk.LoadConfig()
	manifest := pluginsdk.LoadManifest()
	sdkClient := pluginsdk.NewClient(sdkCfg, pluginsdk.Registration{
		ID:           manifest.ID,
		Host:         getHostname(),
		Port:         defaultPort,
		Capabilities: manifest.Capabilities,
		Version:      pluginsdk.DevVersion(manifest.Version),
		SchemaFunc: func() map[string]interface{} {
			schema := map[string]interface{}{
				"config": manifest.ConfigSchema,
			}

			ctx := context.Background()

			// Connection info.
			schema["connection"] = buildConnectionInfo(rdb, ctx)

			// Server stats.
			schema["server_stats"] = buildServerStats(rdb, ctx)

			// Stream stats as a table.
			schema["event_streams"] = buildStreamStats(rdb, ctx)

			return schema
		},
	})
	sdkClient.Start(context.Background())

	eventHandler := handlers.NewEventHandler(rdb, sdkClient, debug)

	router := gin.Default()

	// Health check.
	router.GET("/health", func(c *gin.Context) {
		if err := rdb.Ping(c.Request.Context()).Err(); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Event system REST API — backed by Redis Streams.
	router.POST("/events/publish", eventHandler.Publish)
	router.POST("/events/subscribe", eventHandler.Subscribe)
	router.DELETE("/events/subscribe/:plugin_id/:event_type", eventHandler.Unsubscribe)
	router.GET("/events/consume/:plugin_id/:event_type", eventHandler.Consume)
	router.GET("/events/history", eventHandler.History)
	router.GET("/events/stream", eventHandler.Stream)
	router.GET("/events/stats", eventHandler.Stats)

	sdkClient.ListenAndServe(defaultPort, router)
}

// buildConnectionInfo returns Redis connection details for the readonly schema.
func buildConnectionInfo(rdb *redis.Client, ctx context.Context) map[string]interface{} {
	info := map[string]interface{}{
		"host": "localhost",
		"port": "6379",
	}

	// Parse server info for version and mode.
	serverInfo, err := rdb.Info(ctx, "server").Result()
	if err != nil {
		info["status"] = "unreachable"
		return info
	}
	info["status"] = "connected"
	if v := parseInfoField(serverInfo, "redis_version"); v != "" {
		info["version"] = v
	}
	if v := parseInfoField(serverInfo, "redis_mode"); v != "" {
		info["mode"] = v
	}
	if v := parseInfoField(serverInfo, "tcp_port"); v != "" {
		info["port"] = v
	}
	if v := parseInfoField(serverInfo, "uptime_in_seconds"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			days := secs / 86400
			hours := (secs % 86400) / 3600
			mins := (secs % 3600) / 60
			info["uptime"] = fmt.Sprintf("%dd %dh %dm", days, hours, mins)
		}
	}
	return info
}

// buildServerStats returns key Redis metrics for the readonly schema.
func buildServerStats(rdb *redis.Client, ctx context.Context) map[string]interface{} {
	stats := map[string]interface{}{}

	// Memory info.
	memInfo, err := rdb.Info(ctx, "memory").Result()
	if err == nil {
		if v := parseInfoField(memInfo, "used_memory_human"); v != "" {
			stats["memory_used"] = v
		}
		if v := parseInfoField(memInfo, "used_memory_peak_human"); v != "" {
			stats["memory_peak"] = v
		}
	}

	// Keyspace info.
	keyspaceInfo, err := rdb.Info(ctx, "keyspace").Result()
	if err == nil {
		if v := parseInfoField(keyspaceInfo, "db0"); v != "" {
			// db0:keys=123,expires=45,avg_ttl=6789
			for _, part := range strings.Split(v, ",") {
				kv := strings.SplitN(part, "=", 2)
				if len(kv) == 2 {
					stats["db0_"+kv[0]] = kv[1]
				}
			}
		}
	}

	// Client info.
	clientInfo, err := rdb.Info(ctx, "clients").Result()
	if err == nil {
		if v := parseInfoField(clientInfo, "connected_clients"); v != "" {
			stats["connected_clients"] = v
		}
	}

	// Stats info.
	statsInfo, err := rdb.Info(ctx, "stats").Result()
	if err == nil {
		if v := parseInfoField(statsInfo, "total_commands_processed"); v != "" {
			stats["total_commands"] = v
		}
		if v := parseInfoField(statsInfo, "instantaneous_ops_per_sec"); v != "" {
			stats["ops_per_sec"] = v
		}
	}

	return stats
}

// buildStreamStats returns event stream info as a table for the readonly schema.
func buildStreamStats(rdb *redis.Client, ctx context.Context) map[string]interface{} {
	// Scan for all event streams.
	var cursor uint64
	var streamKeys []string
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "events:*", 100).Result()
		if err != nil {
			break
		}
		streamKeys = append(streamKeys, keys...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Strings(streamKeys)

	items := make([]map[string]interface{}, 0, len(streamKeys))
	for _, key := range streamKeys {
		name := strings.TrimPrefix(key, "events:")

		length, err := rdb.XLen(ctx, key).Result()
		if err != nil {
			continue
		}

		row := map[string]interface{}{
			"stream":   name,
			"messages": length,
		}

		// Get consumer group info.
		groups, err := rdb.XInfoGroups(ctx, key).Result()
		if err == nil {
			row["groups"] = len(groups)
			var consumers int
			var pending int64
			for _, g := range groups {
				consumers += int(g.Consumers)
				pending += g.Pending
			}
			row["consumers"] = consumers
			row["pending"] = pending
		}

		items = append(items, row)
	}

	return map[string]interface{}{
		"_display":  "table",
		"_columns":  []string{"stream", "messages", "groups", "consumers", "pending"},
		"items":     items,
	}
}

// parseInfoField extracts a value from Redis INFO output by key.
func parseInfoField(info, field string) string {
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, field+":") {
			return strings.TrimPrefix(line, field+":")
		}
	}
	return ""
}

func getHostname() string {
	hostname, err := os.Hostname()
	if err != nil {
		log.Printf("Failed to get hostname: %v", err)
		return "localhost"
	}
	return hostname
}
