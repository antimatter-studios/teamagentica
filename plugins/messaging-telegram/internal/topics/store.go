package topics

import (
	"fmt"
	"log"
	"sync"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Mapping stores a forum topic → alias association.
type Mapping struct {
	ID      uint   `gorm:"primaryKey"`
	ChatID  int64  `gorm:"index:idx_chat_topic,unique"`
	TopicID int    `gorm:"index:idx_chat_topic,unique"`
	Alias   string `gorm:"not null"`
}

// Store manages topic-to-alias mappings in SQLite.
type Store struct {
	db *gorm.DB
	mu sync.RWMutex
	// In-memory cache: "chatID:topicID" → alias
	cache map[string]string
}

// NewStore opens (or creates) the SQLite database and loads mappings into memory.
func NewStore(dbPath string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(dbPath+"?_journal_mode=WAL"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("open topics db: %w", err)
	}

	if err := db.AutoMigrate(&Mapping{}); err != nil {
		return nil, fmt.Errorf("migrate topics: %w", err)
	}

	s := &Store{db: db, cache: make(map[string]string)}
	s.loadAll()
	return s, nil
}

func cacheKey(chatID int64, topicID int) string {
	return fmt.Sprintf("%d:%d", chatID, topicID)
}

// loadAll populates the in-memory cache from the database.
func (s *Store) loadAll() {
	var mappings []Mapping
	s.db.Find(&mappings)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range mappings {
		s.cache[cacheKey(m.ChatID, m.TopicID)] = m.Alias
	}
	log.Printf("[topics] loaded %d topic-alias mappings", len(mappings))
}

// Set creates or updates a topic → alias mapping.
func (s *Store) Set(chatID int64, topicID int, alias string) error {
	m := Mapping{ChatID: chatID, TopicID: topicID, Alias: alias}
	result := s.db.Where("chat_id = ? AND topic_id = ?", chatID, topicID).
		Assign(Mapping{Alias: alias}).
		FirstOrCreate(&m)
	if result.Error != nil {
		return result.Error
	}

	s.mu.Lock()
	s.cache[cacheKey(chatID, topicID)] = alias
	s.mu.Unlock()
	return nil
}

// Delete removes a topic mapping. Returns true if it existed.
func (s *Store) Delete(chatID int64, topicID int) bool {
	result := s.db.Where("chat_id = ? AND topic_id = ?", chatID, topicID).Delete(&Mapping{})

	s.mu.Lock()
	key := cacheKey(chatID, topicID)
	_, existed := s.cache[key]
	delete(s.cache, key)
	s.mu.Unlock()

	return existed && result.RowsAffected > 0
}

// DeleteByAlias removes all mappings for a given alias (e.g. when alias is deleted).
func (s *Store) DeleteByAlias(alias string) int {
	result := s.db.Where("alias = ?", alias).Delete(&Mapping{})

	s.mu.Lock()
	for k, v := range s.cache {
		if v == alias {
			delete(s.cache, k)
		}
	}
	s.mu.Unlock()

	return int(result.RowsAffected)
}

// Lookup returns the alias for a given topic, or "" if not mapped.
func (s *Store) Lookup(chatID int64, topicID int) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cache[cacheKey(chatID, topicID)]
}

// ListForChat returns all mappings for a given chat.
func (s *Store) ListForChat(chatID int64) []Mapping {
	var mappings []Mapping
	s.db.Where("chat_id = ?", chatID).Find(&mappings)
	return mappings
}
