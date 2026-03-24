package storage

func (d *DB) ListMessages(conversationID uint) ([]Message, error) {
	var msgs []Message
	err := d.db.Where("conversation_id = ?", conversationID).Order("created_at ASC").Find(&msgs).Error
	return msgs, err
}

func (d *DB) CreateMessage(msg *Message) error {
	return d.db.Create(msg).Error
}

// UpdateMessage updates an existing message by ID.
func (d *DB) UpdateMessage(msg *Message) error {
	return d.db.Save(msg).Error
}

// UpsertProgressMessage creates or updates the progress message for a conversation.
// There is at most one progress message per conversation (role = "progress").
func (d *DB) UpsertProgressMessage(conversationID uint, content string) (*Message, error) {
	var existing Message
	err := d.db.Where("conversation_id = ? AND role = ?", conversationID, "progress").First(&existing).Error
	if err == nil {
		existing.Content = content
		return &existing, d.db.Save(&existing).Error
	}
	msg := &Message{
		ConversationID: conversationID,
		Role:           "progress",
		Content:        content,
	}
	return msg, d.db.Create(msg).Error
}

// DeleteProgressMessages removes all progress messages from a conversation.
func (d *DB) DeleteProgressMessages(conversationID uint) error {
	return d.db.Where("conversation_id = ? AND role = ?", conversationID, "progress").Delete(&Message{}).Error
}

// ListMessagesForContext returns the most recent messages (up to limit) for building agent context.
func (d *DB) ListMessagesForContext(conversationID uint, limit int) ([]Message, error) {
	var msgs []Message
	err := d.db.Where("conversation_id = ?", conversationID).
		Order("created_at DESC").
		Limit(limit).
		Find(&msgs).Error
	if err != nil {
		return nil, err
	}
	// Reverse to chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}
