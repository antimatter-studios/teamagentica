package store

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Manifest stores a versioned plugin manifest in the catalog database.
type Manifest struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	PluginID  string    `gorm:"not null;index:idx_plugin_version,unique" json:"plugin_id"`
	Version   string    `gorm:"not null;index:idx_plugin_version,unique" json:"version"`
	Data      string    `gorm:"type:text;not null" json:"-"` // full plugin.yaml as JSON
	CreatedAt time.Time `json:"created_at"`
}

// Entry is a browsing summary derived from a manifest.
type Entry struct {
	PluginID     string   `json:"plugin_id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Group        string   `json:"group,omitempty"`
	Version      string   `json:"version"`
	Image        string   `json:"image"`
	Author       string   `json:"author"`
	Tags         []string `json:"tags"`
	Capabilities []string `json:"capabilities,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// GroupMeta holds display metadata for a plugin group.
type GroupMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Order       int    `json:"order"`
}

// Groups defines all known plugin groups in display order.
var Groups = []GroupMeta{
	{ID: "agents", Name: "AI Agents", Description: "Chat and reasoning providers powered by LLMs", Order: 1},
	{ID: "messaging", Name: "Messaging", Description: "Communication channels and chat interfaces", Order: 2},
	{ID: "tools", Name: "Tools", Description: "Image, video, and other AI generation tools", Order: 3},
	{ID: "storage", Name: "Storage", Description: "File and data storage backends", Order: 4},
	{ID: "network", Name: "Network", Description: "Tunnels, webhooks, and external connectivity", Order: 5},
	{ID: "infrastructure", Name: "Infrastructure", Description: "Platform internals and system services", Order: 6},
	{ID: "user", Name: "User Tools", Description: "Workspace environments and developer tools", Order: 7},
}

// Store wraps the catalog database.
type Store struct {
	db *gorm.DB
}

// Open creates or opens the catalog SQLite database.
func Open(path string) (*Store, error) {
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open catalog db: %w", err)
	}

	if err := db.AutoMigrate(&Manifest{}); err != nil {
		return nil, fmt.Errorf("migrate catalog db: %w", err)
	}

	return &Store{db: db}, nil
}

// Upsert inserts or updates a manifest for a given plugin+version.
// The data is the full plugin.yaml content as a map.
func (s *Store) Upsert(pluginID, version string, data map[string]interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	var existing Manifest
	result := s.db.Where("plugin_id = ? AND version = ?", pluginID, version).First(&existing)
	if result.Error == nil {
		// Update existing.
		return s.db.Model(&existing).Update("data", string(jsonData)).Error
	}

	// Insert new.
	return s.db.Create(&Manifest{
		PluginID: pluginID,
		Version:  version,
		Data:     string(jsonData),
	}).Error
}

// GetManifest returns the full manifest data for a plugin (latest semver version).
func (s *Store) GetManifest(pluginID string) (map[string]interface{}, bool) {
	var manifests []Manifest
	if err := s.db.Where("plugin_id = ?", pluginID).Find(&manifests).Error; err != nil {
		return nil, false
	}
	if len(manifests) == 0 {
		return nil, false
	}

	latest := manifests[0]
	for _, m := range manifests[1:] {
		if semverLess(latest.Version, m.Version) {
			latest = m
		}
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(latest.Data), &data); err != nil {
		log.Printf("catalog: corrupt manifest for %s: %v", pluginID, latest.Version)
		return nil, false
	}
	return data, true
}

// Search returns browsing entries matching the query (or all if empty).
func (s *Store) Search(q string) []Entry {
	var manifests []Manifest
	s.db.Find(&manifests)

	// Keep the highest semver per plugin_id.
	latest := make(map[string]Manifest, len(manifests))
	for _, m := range manifests {
		if existing, ok := latest[m.PluginID]; !ok || semverLess(existing.Version, m.Version) {
			latest[m.PluginID] = m
		}
	}

	var entries []Entry
	for _, m := range latest {
		e := manifestToEntry(m)
		if q == "" || matchesQuery(e, strings.ToLower(q)) {
			entries = append(entries, e)
		}
	}
	return entries
}

// semverLess reports whether version a is less than version b.
// Compares dot-separated numeric components; non-numeric parts fall back to string comparison.
func semverLess(a, b string) bool {
	aParts := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bParts := strings.Split(strings.TrimPrefix(b, "v"), ".")
	n := len(aParts)
	if len(bParts) > n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		var ap, bp string
		if i < len(aParts) {
			ap = aParts[i]
		}
		if i < len(bParts) {
			bp = bParts[i]
		}
		ai, aErr := strconv.Atoi(ap)
		bi, bErr := strconv.Atoi(bp)
		if aErr == nil && bErr == nil {
			if ai != bi {
				return ai < bi
			}
		} else if ap != bp {
			return ap < bp
		}
	}
	return false
}

// ListAll returns browsing entries for all plugins (latest version each).
func (s *Store) ListAll() []Entry {
	return s.Search("")
}

// Count returns the number of unique plugins in the catalog.
func (s *Store) Count() int64 {
	var count int64
	s.db.Model(&Manifest{}).Distinct("plugin_id").Count(&count)
	return count
}

func manifestToEntry(m Manifest) Entry {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(m.Data), &data); err != nil {
		return Entry{PluginID: m.PluginID, Version: m.Version}
	}

	return Entry{
		PluginID:     m.PluginID,
		Name:         strField(data, "name"),
		Description:  strField(data, "description"),
		Group:        strField(data, "group"),
		Version:      m.Version,
		Image:        strField(data, "image"),
		Author:       strField(data, "author"),
		Tags:         strSliceField(data, "tags"),
		Capabilities: strSliceField(data, "capabilities"),
		Dependencies: strSliceField(data, "dependencies"),
	}
}

func strField(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func strSliceField(m map[string]interface{}, key string) []string {
	raw, ok := m[key].([]interface{})
	if !ok {
		return nil
	}
	var result []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func matchesQuery(e Entry, q string) bool {
	if strings.Contains(strings.ToLower(e.PluginID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Name), q) {
		return true
	}
	if strings.Contains(strings.ToLower(e.Description), q) {
		return true
	}
	for _, tag := range e.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}
