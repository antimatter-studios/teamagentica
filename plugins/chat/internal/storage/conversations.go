package storage

func (d *DB) ListConversations(userID uint) ([]Conversation, error) {
	var convos []Conversation
	err := d.db.Where("user_id = ?", userID).Order("updated_at DESC").Find(&convos).Error
	return convos, err
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

func (d *DB) DeleteConversation(id uint) error {
	// Delete messages first, then conversation.
	if err := d.db.Where("conversation_id = ?", id).Delete(&Message{}).Error; err != nil {
		return err
	}
	return d.db.Delete(&Conversation{}, id).Error
}
