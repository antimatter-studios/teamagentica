package storage

func (d *DB) ListMessages(conversationID uint) ([]Message, error) {
	var msgs []Message
	err := d.db.Where("conversation_id = ?", conversationID).Order("created_at ASC").Find(&msgs).Error
	return msgs, err
}

func (d *DB) CreateMessage(msg *Message) error {
	return d.db.Create(msg).Error
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
