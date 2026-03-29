package storage

import (
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"gorm.io/gorm"
)

type Board struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	Name        string         `json:"name"`
	Prefix      string         `gorm:"uniqueIndex" json:"prefix"`
	Description string         `json:"description"`
	CreatedAt   int64          `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt   int64          `gorm:"autoUpdateTime:milli" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

type Column struct {
	ID        string         `gorm:"primaryKey" json:"id"`
	BoardID   string         `gorm:"index;not null" json:"board_id"`
	Name      string         `json:"name"`
	Position  float64        `json:"position"`
	CreatedAt int64          `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt int64          `gorm:"autoUpdateTime:milli" json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

type Epic struct {
	ID          string         `gorm:"primaryKey" json:"id"`
	BoardID     string         `gorm:"index;not null" json:"board_id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Color       string         `json:"color"` // hex color e.g. "#4A90D9"
	Position    float64        `json:"position"`
	CreatedAt   int64          `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt   int64          `gorm:"autoUpdateTime:milli" json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"`
}

type Card struct {
	ID            string         `gorm:"primaryKey" json:"id"`
	Number        uint           `gorm:"index;not null;default:0" json:"number"` // auto-increment per board
	BoardID       string         `gorm:"index;not null" json:"board_id"`
	ColumnID      string         `gorm:"index;not null" json:"column_id"`
	EpicID        string         `gorm:"index" json:"epic_id"`              // optional epic grouping
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	CardType      string         `json:"card_type"`      // "task", "bug"
	Priority      string         `json:"priority"`       // "", "low", "medium", "high", "urgent"
	AssigneeID    uint           `json:"assignee_id"`     // user ID (0 = unassigned)
	AssigneeAgent string         `json:"assignee_agent"`  // agent alias ("" = none)
	Labels        string         `json:"labels"`          // comma-separated
	DueDate       *int64         `json:"due_date"`        // unix ms, nullable
	Position      float64        `json:"position"`
	CreatedAt     int64          `gorm:"autoCreateTime:milli" json:"created_at"`
	UpdatedAt     int64          `gorm:"autoUpdateTime:milli" json:"updated_at"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
}

type Comment struct {
	ID        string         `gorm:"primaryKey" json:"id"`
	CardID    string         `gorm:"index;not null" json:"card_id"`
	AuthorID  uint           `json:"author_id"` // user ID from X-User-ID header
	Body      string         `json:"body"`
	CreatedAt int64          `gorm:"autoCreateTime:milli" json:"created_at"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
}

type DB struct {
	db *gorm.DB
}

func Open(dataPath string) (*DB, error) {
	conn, err := pluginsdk.OpenDatabase(dataPath, "tasks.db", &Board{}, &Column{}, &Epic{}, &Card{}, &Comment{})
	if err != nil {
		return nil, err
	}
	d := &DB{db: conn}
	d.backfillCardNumbers()
	return d, nil
}

// backfillCardNumbers assigns sequential numbers to any cards that have number=0.
func (d *DB) backfillCardNumbers() {
	var boards []Board
	d.db.Find(&boards)
	for _, b := range boards {
		var cards []Card
		d.db.Where("board_id = ? AND number = 0", b.ID).Order("created_at asc").Find(&cards)
		if len(cards) == 0 {
			continue
		}
		// Find current max number for this board.
		var maxNum uint
		d.db.Model(&Card{}).Where("board_id = ? AND number > 0", b.ID).
			Select("COALESCE(MAX(number), 0)").Scan(&maxNum)
		for i, c := range cards {
			c.Number = maxNum + uint(i) + 1
			d.db.Model(&Card{}).Where("id = ?", c.ID).Update("number", c.Number)
		}
	}
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
		tx.Where("board_id = ?", id).Delete(&Epic{})
		return tx.Delete(&Board{}, "id = ?", id).Error
	})
}

// Epics

func (d *DB) ListEpics(boardID string) ([]Epic, error) {
	var epics []Epic
	return epics, d.db.Where("board_id = ?", boardID).Order("position asc").Find(&epics).Error
}

func (d *DB) CreateEpic(e *Epic) error {
	return d.db.Create(e).Error
}

func (d *DB) GetEpic(id string) (*Epic, error) {
	var e Epic
	return &e, d.db.First(&e, "id = ?", id).Error
}

func (d *DB) UpdateEpic(e *Epic) error {
	return d.db.Save(e).Error
}

func (d *DB) DeleteEpic(id string) error {
	return d.db.Transaction(func(tx *gorm.DB) error {
		// Unlink cards from this epic (don't delete them)
		tx.Model(&Card{}).Where("epic_id = ?", id).Update("epic_id", "")
		return tx.Delete(&Epic{}, "id = ?", id).Error
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

func (d *DB) SearchCards(boardID, query string) ([]Card, error) {
	var cards []Card
	pattern := "%" + query + "%"
	return cards, d.db.Where("board_id = ? AND (title LIKE ? OR description LIKE ? OR labels LIKE ?)", boardID, pattern, pattern, pattern).
		Order("column_id asc, position asc").Find(&cards).Error
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

func (d *DB) GetCardByNumber(boardID string, number uint) (*Card, error) {
	var c Card
	return &c, d.db.First(&c, "board_id = ? AND number = ?", boardID, number).Error
}

func (d *DB) NextCardNumber(boardID string) uint {
	var maxNum uint
	d.db.Model(&Card{}).Where("board_id = ?", boardID).
		Select("COALESCE(MAX(number), 0)").Scan(&maxNum)
	return maxNum + 1
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
