package storage

import (
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Board struct {
	ID          string `gorm:"primaryKey" json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt   int64  `gorm:"autoUpdateTime:milli" json:"updated_at"`
}

type Column struct {
	ID        string  `gorm:"primaryKey" json:"id"`
	BoardID   string  `gorm:"index;not null" json:"board_id"`
	Name      string  `json:"name"`
	Position  float64 `json:"position"`
	CreatedAt int64   `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt int64   `gorm:"autoUpdateTime:milli" json:"updated_at"`
}

type Card struct {
	ID            string  `gorm:"primaryKey" json:"id"`
	BoardID       string  `gorm:"index;not null" json:"board_id"`
	ColumnID      string  `gorm:"index;not null" json:"column_id"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Priority      string  `json:"priority"`       // "", "low", "medium", "high", "urgent"
	AssigneeID    uint    `json:"assignee_id"`     // user ID (0 = unassigned)
	AssigneeAgent string  `json:"assignee_agent"`  // agent alias ("" = none)
	Labels        string  `json:"labels"`          // comma-separated
	DueDate       *int64  `json:"due_date"`        // unix ms, nullable
	Position      float64 `json:"position"`
	CreatedAt     int64   `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt     int64   `gorm:"autoUpdateTime:milli" json:"updated_at"`
}

type Comment struct {
	ID        string `gorm:"primaryKey" json:"id"`
	CardID    string `gorm:"index;not null" json:"card_id"`
	AuthorID  uint   `json:"author_id"` // user ID from X-User-ID header
	Body      string `json:"body"`
	CreatedAt int64  `gorm:"autoCreateTime:milli" json:"created_at"`
}

type DB struct {
	db *gorm.DB
}

func Open(dataPath string) (*DB, error) {
	dbPath := filepath.Join(dataPath, "tasks.db")
	dsn := dbPath + "?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_foreign_keys=ON"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&Board{}, &Column{}, &Card{}, &Comment{}); err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

// Boards

func (d *DB) ListBoards() ([]Board, error) {
	var boards []Board
	return boards, d.db.Order("created_at asc").Find(&boards).Error
}

func (d *DB) CreateBoard(b *Board) error {
	return d.db.Create(b).Error
}

func (d *DB) GetBoard(id string) (*Board, error) {
	var b Board
	return &b, d.db.First(&b, "id = ?", id).Error
}

func (d *DB) UpdateBoard(b *Board) error {
	return d.db.Save(b).Error
}

func (d *DB) DeleteBoard(id string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		tx.Where("board_id = ?", id).Delete(&Card{})
		tx.Where("board_id = ?", id).Delete(&Column{})
		return tx.Delete(&Board{}, "id = ?", id).Error
	})
}

// Columns

func (d *DB) ListColumns(boardID string) ([]Column, error) {
	var cols []Column
	return cols, d.db.Where("board_id = ?", boardID).Order("position asc").Find(&cols).Error
}

func (d *DB) CreateColumn(c *Column) error {
	return d.db.Create(c).Error
}

func (d *DB) GetColumn(id string) (*Column, error) {
	var c Column
	return &c, d.db.First(&c, "id = ?", id).Error
}

func (d *DB) UpdateColumn(c *Column) error {
	return d.db.Save(c).Error
}

func (d *DB) DeleteColumn(id string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		tx.Where("column_id = ?", id).Delete(&Card{})
		return tx.Delete(&Column{}, "id = ?", id).Error
	})
}

// Cards

func (d *DB) ListCards(boardID string) ([]Card, error) {
	var cards []Card
	return cards, d.db.Where("board_id = ?", boardID).Order("column_id asc, position asc").Find(&cards).Error
}

func (d *DB) ListCardsByColumn(columnID string) ([]Card, error) {
	var cards []Card
	return cards, d.db.Where("column_id = ?", columnID).Order("position asc").Find(&cards).Error
}

func (d *DB) CreateCard(c *Card) error {
	return d.db.Create(c).Error
}

func (d *DB) GetCard(id string) (*Card, error) {
	var c Card
	return &c, d.db.First(&c, "id = ?", id).Error
}

func (d *DB) UpdateCard(c *Card) error {
	return d.db.Save(c).Error
}

func (d *DB) DeleteCard(id string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		tx.Where("card_id = ?", id).Delete(&Comment{})
		return tx.Delete(&Card{}, "id = ?", id).Error
	})
}

// Comments

func (d *DB) ListComments(cardID string) ([]Comment, error) {
	var comments []Comment
	return comments, d.db.Where("card_id = ?", cardID).Order("created_at asc").Find(&comments).Error
}

func (d *DB) CreateComment(c *Comment) error {
	return d.db.Create(c).Error
}

func (d *DB) DeleteComment(id string) error {
	return d.db.Delete(&Comment{}, "id = ?", id).Error
}
