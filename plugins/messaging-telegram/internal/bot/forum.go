package bot

import (
	"encoding/json"
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// forumTopicResult is the Telegram API response for createForumTopic.
type forumTopicResult struct {
	MessageThreadID int    `json:"message_thread_id"`
	Name            string `json:"name"`
	IconColor       int    `json:"icon_color"`
}

// createForumTopic creates a new forum topic in a supergroup.
// Returns the message_thread_id of the created topic.
func (b *Bot) createForumTopic(chatID int64, name string) (int, error) {
	params := tgbotapi.Params{
		"chat_id": fmt.Sprintf("%d", chatID),
		"name":    name,
	}

	resp, err := b.api.MakeRequest("createForumTopic", params)
	if err != nil {
		return 0, fmt.Errorf("createForumTopic: %w", err)
	}

	var result forumTopicResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, fmt.Errorf("parse createForumTopic result: %w", err)
	}

	log.Printf("[forum] created topic %q in chat %d → thread_id=%d", name, chatID, result.MessageThreadID)
	return result.MessageThreadID, nil
}

// closeForumTopic closes a forum topic in a supergroup.
func (b *Bot) closeForumTopic(chatID int64, topicID int) error {
	params := tgbotapi.Params{
		"chat_id":            fmt.Sprintf("%d", chatID),
		"message_thread_id":  fmt.Sprintf("%d", topicID),
	}
	_, err := b.api.MakeRequest("closeForumTopic", params)
	return err
}

// sendToTopic sends a text message to a specific forum topic. The text is
// expected to already be rendered as Telegram HTML — parse_mode=HTML is set
// so bold, italic, code, links, etc. render correctly.
func (b *Bot) sendToTopic(chatID int64, topicID int, text string) (tgbotapi.Message, error) {
	params := tgbotapi.Params{
		"chat_id":            fmt.Sprintf("%d", chatID),
		"message_thread_id":  fmt.Sprintf("%d", topicID),
		"text":               text,
		"parse_mode":         "HTML",
	}

	resp, err := b.api.MakeRequest("sendMessage", params)
	if err != nil {
		return tgbotapi.Message{}, err
	}

	var msg tgbotapi.Message
	if err := json.Unmarshal(resp.Result, &msg); err != nil {
		return tgbotapi.Message{}, fmt.Errorf("parse sendMessage result: %w", err)
	}
	return msg, nil
}

// editTopicMessage edits a message within a forum topic.
func (b *Bot) editTopicMessage(chatID int64, messageID int, text string) error {
	params := tgbotapi.Params{
		"chat_id":    fmt.Sprintf("%d", chatID),
		"message_id": fmt.Sprintf("%d", messageID),
		"text":       text,
	}
	_, err := b.api.MakeRequest("editMessageText", params)
	return err
}

// deleteTopicMessage deletes a message within a forum topic.
func (b *Bot) deleteTopicMessage(chatID int64, messageID int) error {
	params := tgbotapi.Params{
		"chat_id":    fmt.Sprintf("%d", chatID),
		"message_id": fmt.Sprintf("%d", messageID),
	}
	_, err := b.api.MakeRequest("deleteMessage", params)
	return err
}

// sendTypingToTopic sends a typing indicator to a specific forum topic.
func (b *Bot) sendTypingToTopic(chatID int64, topicID int) {
	params := tgbotapi.Params{
		"chat_id":            fmt.Sprintf("%d", chatID),
		"message_thread_id":  fmt.Sprintf("%d", topicID),
		"action":             "typing",
	}
	b.api.MakeRequest("sendChatAction", params)
}

// getMessageThreadID extracts the message_thread_id from a raw Telegram update.
// The tgbotapi v5 library doesn't expose this field, so we parse it from raw JSON.
func getMessageThreadID(rawJSON []byte) int {
	// Quick extraction — look for message_thread_id in the message object.
	var update struct {
		Message *struct {
			MessageThreadID int  `json:"message_thread_id"`
			IsTopicMessage  bool `json:"is_topic_message"`
		} `json:"message"`
		ChannelPost *struct {
			MessageThreadID int  `json:"message_thread_id"`
			IsTopicMessage  bool `json:"is_topic_message"`
		} `json:"channel_post"`
	}
	if err := json.Unmarshal(rawJSON, &update); err != nil {
		return 0
	}
	if update.Message != nil && update.Message.MessageThreadID != 0 {
		return update.Message.MessageThreadID
	}
	if update.ChannelPost != nil && update.ChannelPost.MessageThreadID != 0 {
		return update.ChannelPost.MessageThreadID
	}
	return 0
}
