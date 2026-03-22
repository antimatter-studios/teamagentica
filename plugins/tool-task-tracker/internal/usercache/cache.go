package usercache

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// PluginRouter is the subset of pluginsdk.Client needed for cross-plugin calls.
type PluginRouter interface {
	RouteToPlugin(ctx context.Context, pluginID, method, path string, body io.Reader) ([]byte, error)
}

// UserInfo holds the resolved identity of a user.
type UserInfo struct {
	ID          uint   `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
}

// FormatName returns "Display Name (email)" or just email if no display name.
func (u *UserInfo) FormatName() string {
	if u == nil {
		return ""
	}
	if u.DisplayName != "" {
		return fmt.Sprintf("%s (%s)", u.DisplayName, u.Email)
	}
	return u.Email
}

type entry struct {
	user      *UserInfo
	fetchedAt time.Time
}

// Cache is a small TTL cache that resolves user IDs via the user manager plugin.
type Cache struct {
	router PluginRouter
	mu     sync.RWMutex
	users  map[uint]*entry
	ttl    time.Duration
}

// New creates a user cache with the given TTL.
func New(router PluginRouter, ttl time.Duration) *Cache {
	return &Cache{
		router: router,
		users:  make(map[uint]*entry),
		ttl:    ttl,
	}
}

// Get resolves a single user ID. Returns nil for ID 0.
func (c *Cache) Get(ctx context.Context, id uint) (*UserInfo, error) {
	if id == 0 {
		return nil, nil
	}

	c.mu.RLock()
	if e, ok := c.users[id]; ok && time.Since(e.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return e.user, nil
	}
	c.mu.RUnlock()

	data, err := c.router.RouteToPlugin(ctx, "system-user-manager", "GET", fmt.Sprintf("/users/%d", id), nil)
	if err != nil {
		return nil, fmt.Errorf("fetch user %d: %w", id, err)
	}

	var envelope struct {
		User UserInfo `json:"user"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode user %d: %w", id, err)
	}
	u := envelope.User

	c.mu.Lock()
	c.users[id] = &entry{user: &u, fetchedAt: time.Now()}
	c.mu.Unlock()

	return &u, nil
}

// GetMany resolves multiple user IDs, returning available results.
// Errors for individual lookups are silently ignored.
func (c *Cache) GetMany(ctx context.Context, ids []uint) map[uint]*UserInfo {
	result := make(map[uint]*UserInfo, len(ids))
	for _, id := range ids {
		if u, err := c.Get(ctx, id); err == nil && u != nil {
			result[id] = u
		}
	}
	return result
}
