package bot

import (
	"fmt"
	"log"
	"strings"
)

// handleCreateAgentChannel creates a forum topic and maps it to an alias.
// Usage: /newchannel @alias
func (b *Bot) handleCreateAgentChannel(chatID int64, topicID int, args string) {
	aliasName := strings.TrimPrefix(strings.TrimSpace(args), "@")
	if aliasName == "" {
		b.sendToChat(chatID, topicID, "Usage: /newchannel @alias\n\nExample: /newchannel @codearchitect")
		return
	}

	// Validate: alias must exist and be chattable (agent, image, or video — not plain tools/storage).
	target := b.aliases.Resolve(aliasName)
	if target == nil {
		available := b.aliases.ListChattableAliases()
		if len(available) > 0 {
			b.sendToChat(chatID, topicID, fmt.Sprintf("Unknown alias @%s.\n\nAvailable agents: @%s", aliasName, strings.Join(available, ", @")))
		} else {
			b.sendToChat(chatID, topicID, fmt.Sprintf("Unknown alias @%s. No agents are currently registered.", aliasName))
		}
		return
	}
	if !target.IsChatTarget() {
		b.sendToChat(chatID, topicID, fmt.Sprintf("@%s is a tool/storage plugin and cannot be chatted with directly.\n\nOnly agents and agent-tools can have channels.", aliasName))
		return
	}

	// If there's already a mapping for this alias, remove the old one (topic may have been deleted externally).
	existing := b.topicStore.ListForChat(chatID)
	for _, m := range existing {
		if m.Alias == aliasName {
			b.topicStore.Delete(chatID, m.TopicID)
			log.Printf("[forum] replacing old topic mapping for @%s (old topic_id=%d)", aliasName, m.TopicID)
		}
	}

	// Create the forum topic via Telegram API.
	newTopicID, err := b.createForumTopic(chatID, aliasName)
	if err != nil {
		log.Printf("[forum] failed to create topic for @%s in chat %d: %v", aliasName, chatID, err)
		b.sendToChat(chatID, topicID, fmt.Sprintf("Failed to create topic: %v\n\nMake sure this group has Topics enabled (Group Settings → Topics).", err))
		return
	}

	// Persist the mapping.
	if err := b.topicStore.Set(chatID, newTopicID, aliasName); err != nil {
		log.Printf("[forum] failed to persist topic mapping: %v", err)
		b.sendToChat(chatID, topicID, "Topic created but failed to save mapping. Try /deletechannel in the new topic and recreate.")
		return
	}

	// Send confirmation in the new topic.
	b.sendToTopic(chatID, newTopicID, fmt.Sprintf("This topic is now linked to @%s.\n\nAll messages here will be automatically routed to this agent — no @mention needed.", aliasName))

	// Also confirm in the original chat/topic.
	b.sendToChat(chatID, topicID, fmt.Sprintf("Created agent channel for @%s.", aliasName))

	log.Printf("[forum] created agent channel: chat=%d topic=%d → @%s", chatID, newTopicID, aliasName)
	b.emitEvent("agent_channel_created", fmt.Sprintf("chat=%d topic=%d alias=@%s", chatID, newTopicID, aliasName))
}

// handleDeleteAgentChannel removes the topic→alias mapping.
// Two modes:
//   - Inside a topic: /deletechannel (no args needed, deletes current topic mapping)
//   - From general chat: /deletechannel @alias (deletes by alias name)
func (b *Bot) handleDeleteAgentChannel(chatID int64, topicID int, args string) {
	aliasArg := strings.TrimPrefix(strings.TrimSpace(args), "@")

	// Mode 1: by alias name from any chat
	if aliasArg != "" {
		removed := b.topicStore.DeleteByAlias(aliasArg)
		if removed == 0 {
			b.sendToChat(chatID, topicID, fmt.Sprintf("No agent channel found for @%s.", aliasArg))
			return
		}
		b.sendToChat(chatID, topicID, fmt.Sprintf("Removed @%s agent channel.", aliasArg))
		log.Printf("[forum] deleted agent channel by alias: @%s (%d mappings removed)", aliasArg, removed)
		b.emitEvent("agent_channel_deleted", fmt.Sprintf("chat=%d alias=@%s", chatID, aliasArg))
		return
	}

	// Mode 2: inside a topic, no args
	if topicID == 0 {
		b.sendToChat(chatID, 0, "Usage:\n• /deletechannel @alias — from any chat\n• /deletechannel — from inside the agent topic")
		return
	}

	aliasName := b.topicStore.Lookup(chatID, topicID)
	if aliasName == "" {
		b.sendToChat(chatID, topicID, "This topic is not linked to any agent.")
		return
	}

	b.topicStore.Delete(chatID, topicID)
	b.sendToChat(chatID, topicID, fmt.Sprintf("Unlinked @%s from this topic. Messages here will no longer auto-route.", aliasName))

	log.Printf("[forum] deleted agent channel: chat=%d topic=%d was @%s", chatID, topicID, aliasName)
	b.emitEvent("agent_channel_deleted", fmt.Sprintf("chat=%d topic=%d alias=@%s", chatID, topicID, aliasName))
}

// handleListAgentChannels shows all topic→alias mappings for the current group.
func (b *Bot) handleListAgentChannels(chatID int64, topicID int) {
	mappings := b.topicStore.ListForChat(chatID)
	if len(mappings) == 0 {
		b.sendToChat(chatID, topicID, "No agent channels in this group.\n\nUse /newchannel @alias to create one.")
		return
	}

	var sb strings.Builder
	sb.WriteString("Agent channels in this group:\n\n")
	for _, m := range mappings {
		sb.WriteString(fmt.Sprintf("• Topic %d → @%s\n", m.TopicID, m.Alias))
	}
	sb.WriteString("\nUse /deletechannel inside a topic to unlink it.")
	b.sendToChat(chatID, topicID, sb.String())
}
