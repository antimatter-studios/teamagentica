package channels

import (
	"fmt"
	"log"
	"sync"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ChannelMapping stores a Discord channel → target association.
// Target is an alias name now (e.g. "codearchitect"), but can later hold
// structured values like "workspace/my_project".
type ChannelMapping struct {
	ID        uint   `gorm:"primaryKey"`
	ChannelID string `gorm:"uniqueIndex;not null"` // Discord channel snowflake
	Target    string `gorm:"not null"`             // alias (or future: "workspace/name")
}

// Store manages channel-to-target mappings in SQLite with an in-memory cache.
type Store struct {
	db    *gorm.DB
	mu    sync.RWMutex
	cache map[string]string // channelID → target
}

// NewStore opens (or creates) the SQLite database and loads mappings into memory.
func NewStore(dbPath string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(dbPath+"?_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open channel mappings db: %w", err)
	}

	if err := db.AutoMigrate(&ChannelMapping{}); err != nil {
		return nil, fmt.Errorf("migrate channel mappings: %w", err)
	}

	s := &Store{db: db, cache: make(map[string]string)}
	s.loadAll()
	return s, nil
}

// loadAll populates the in-memory cache from the database.
func (s *Store) loadAll() {
	var mappings []ChannelMapping
	s.db.Find(&mappings)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range mappings {
		s.cache[m.ChannelID] = m.Target
	}
	log.Printf("[channels] loaded %d channel-target mappings", len(mappings))
}

// Set creates or updates a channel → target mapping.
func (s *Store) Set(channelID, target string) error {
	m := ChannelMapping{ChannelID: channelID, Target: target}
	result := s.db.Where("channel_id = ?", channelID).
		Assign(ChannelMapping{Target: target}).
		FirstOrCreate(&m)
	if result.Error != nil {
		return result.Error
	}

	s.mu.Lock()
	s.cache[channelID] = target
	s.mu.Unlock()
	return nil
}

// Delete removes a channel mapping. Returns the old target if it existed.
func (s *Store) Delete(channelID string) (string, bool) {
	s.db.Where("channel_id = ?", channelID).Delete(&ChannelMapping{})

	s.mu.Lock()
	old, existed := s.cache[channelID]
	delete(s.cache, channelID)
	s.mu.Unlock()

	return old, existed
}

// DeleteByTarget removes all mappings for a given target (e.g. when alias is deleted).
func (s *Store) DeleteByTarget(target string) int {
	result := s.db.Where("target = ?", target).Delete(&ChannelMapping{})

	s.mu.Lock()
	for k, v := range s.cache {
		if v == target {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()

	return int(result.RowsAffected)
}

// Lookup returns the target for a given channel, or "" if not mapped.
func (s *Store) Lookup(channelID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[channelID]
}

// ListAll returns all channel mappings.
func (s *Store) ListAll() []ChannelMapping {
	var mappings []ChannelMapping
	s.db.Find(&mappings)
	return mappings
}
