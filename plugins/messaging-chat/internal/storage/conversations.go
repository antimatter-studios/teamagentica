package storage

import "time"

// ConversationWithUnread extends Conversation with a computed unread count.
type ConversationWithUnread struct {
	Conversation
	UnreadCount int `json:"unread_count"`
}

func (d *DB) ListConversations(userID uint) ([]ConversationWithUnread, error) {
	var convos []Conversation
	if err := d.db.Where("user_id = ?", userID).Order("updated_at DESC").Find(&convos).Error; err != nil {
		return nil, err
	}

	results := make([]ConversationWithUnread, len(convos))
	for i, conv := range convos {
		var count int64
		q := d.db.Model(&Message{}).Where("conversation_id = ? AND role IN ('assistant','user')", conv.ID)
		if conv.State.LastReadAt != nil {
			q = q.Where("created_at > ?", *conv.State.LastReadAt)
		}
		q.Count(&count)
		results[i] = ConversationWithUnread{
			Conversation: conv,
			UnreadCount:  int(count),
		}
	}
	return results, nil
}

func (d *DB) GetConversation(id uint) (*Conversation, error) {
	var conv Conversation
	if err := d.db.First(&conv, id).Error; err != nil {
		return nil, err
	}
	return &conv, nil
}

func (d *DB) CreateConversation(conv *Conversation) error {
	return d.db.Create(conv).Error
}

func (d *DB) UpdateConversation(conv *Conversation) error {
	return d.db.Save(conv).Error
}

// MarkRead sets the conversation's LastReadAt to now.
func (d *DB) MarkRead(id uint) error {
	conv, err := d.GetConversation(id)
	if err != nil {
		return err
	}
	now := time.Now()
	conv.State.LastReadAt = &now
	return d.db.Save(conv).Error
}

func (d *DB) DeleteConversation(id uint) error {
	// Delete messages first, then conversation.
	if err := d.db.Where("conversation_id = ?", id).Delete(&Message{}).Error; err != nil {
		return err
	}
	return d.db.Delete(&Conversation{}, id).Error
}
