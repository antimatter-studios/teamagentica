package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// chatCmdEntry is a chat command from the chat command server registry.
type chatCmdEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	PluginID    string `json:"plugin_id"`
}

// chatCmdState holds the current chat command registry from the command server.
type chatCmdState struct {
	mu       sync.RWMutex
	commands []chatCmdEntry
	sdk      *pluginsdk.Client
}

// SetSDK gives the bot access to the plugin SDK for P2P routing.
func (b *Bot) SetSDK(sdk *pluginsdk.Client) {
	b.chatCmds.sdk = sdk
}

// UpdateChatCommands processes a chat:commands:updated event and registers
// the commands with Telegram's BotFather command list.
func (b *Bot) UpdateChatCommands(detail string) {
	var payload struct {
		Commands []chatCmdEntry `json:"commands"`
		Count    int            `json:"count"`
	}
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		log.Printf("chatcmds: failed to parse commands:updated: %v", err)
		return
	}

	b.chatCmds.mu.Lock()
	b.chatCmds.commands = payload.Commands
	b.chatCmds.mu.Unlock()

	// Re-register commands with Telegram, merging native + chat commands.
	b.registerCommandsWithChatCmds()

	log.Printf("chatcmds: updated %d chat commands", len(payload.Commands))
}

// registerCommandsWithChatCmds re-registers the BotFather command list
// including both native commands and chat commands from the server.
func (b *Bot) registerCommandsWithChatCmds() {
	commands := []tgbotapi.BotCommand{
		{Command: "clear", Description: "Clear conversation history"},
		{Command: "aliases", Description: "List configured @mention aliases"},
		{Command: "newchannel", Description: "Create a dedicated topic for an agent"},
		{Command: "deletechannel", Description: "Remove agent routing from current topic"},
		{Command: "channels", Description: "Show all agent topics"},
		{Command: "help", Description: "Show available commands"},
	}

	b.chatCmds.mu.RLock()
	for _, cmd := range b.chatCmds.commands {
		// Convert "namespace:name" → "namespace_name" for Telegram command format.
		tgName := strings.ReplaceAll(cmd.Name, ":", "_")
		commands = append(commands, tgbotapi.BotCommand{
			Command:     tgName,
			Description: cmd.Description,
		})
	}
	b.chatCmds.mu.RUnlock()

	cfg := tgbotapi.NewSetMyCommands(commands...)
	if _, err := b.api.Request(cfg); err != nil {
		log.Printf("chatcmds: setMyCommands failed: %v", err)
	} else {
		log.Printf("chatcmds: registered %d commands with Telegram", len(commands))
	}
}

// HandleChatCommand checks if a /command is a chat command and routes it
// through the chat command server. Returns true if handled.
func (b *Bot) HandleChatCommand(chatID int64, topicID int, text string) bool {
	if b.chatCmds.sdk == nil {
		return false
	}

	parts := strings.SplitN(text, " ", 2)
	cmd := strings.TrimPrefix(parts[0], "/")
	cmd = strings.TrimSuffix(cmd, "@"+b.api.Self.UserName)

	// Look up in chat commands (convert telegram format back to original).
	b.chatCmds.mu.RLock()
	var matched *chatCmdEntry
	for _, entry := range b.chatCmds.commands {
		tgName := strings.ReplaceAll(entry.Name, ":", "_")
		if tgName == cmd {
			matched = &chatCmdEntry{Name: entry.Name, Description: entry.Description, PluginID: entry.PluginID}
			break
		}
	}
	b.chatCmds.mu.RUnlock()

	if matched == nil {
		return false
	}

	// Parse simple key=value params from args (or positional).
	params := make(map[string]string)
	if len(parts) > 1 {
		args := strings.TrimSpace(parts[1])
		// Simple: put the whole args string as "args" param.
		params["args"] = args
	}

	invokeBody, _ := json.Marshal(map[string]interface{}{
		"command":  matched.Name,
		"params":   params,
		"platform": "telegram",
		"user_id":  fmt.Sprintf("%d", chatID),
	})

	raw, err := b.chatCmds.sdk.RouteToPlugin(
		context.Background(),
		"infra-chat-command-server",
		"POST", "/invoke",
		bytes.NewReader(invokeBody),
	)
	if err != nil {
		log.Printf("chatcmds: invoke %s failed: %v", matched.Name, err)
		b.sendToChat(chatID, topicID, "Command failed: "+err.Error())
		return true
	}

	var resp pluginsdk.ChatCommandResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		b.sendToChat(chatID, topicID, "Invalid response from command server.")
		return true
	}

	msg := renderChatCommandForTelegram(resp)
	b.sendToChat(chatID, topicID, msg)
	return true
}

// renderChatCommandForTelegram converts a ChatCommandResponse to Telegram-formatted text.
func renderChatCommandForTelegram(resp pluginsdk.ChatCommandResponse) string {
	switch resp.Type {
	case "text":
		return resp.Text

	case "error":
		return "Error: " + resp.Text

	case "table":
		if resp.Table == nil {
			return "No data."
		}
		return renderTableForTelegram(resp.Table)

	case "embed":
		var parts []string
		for _, e := range resp.Embeds {
			if e.Title != "" {
				parts = append(parts, "*"+escapeMarkdown(e.Title)+"*")
			}
			for _, f := range e.Fields {
				parts = append(parts, fmt.Sprintf("*%s:* %s", escapeMarkdown(f.Name), escapeMarkdown(f.Value)))
			}
		}
		return strings.Join(parts, "\n\n")
	}

	return resp.Text
}

// renderTableForTelegram formats a TableContent as a Telegram pre-formatted block.
func renderTableForTelegram(t *pluginsdk.TableContent) string {
	if len(t.Rows) == 0 {
		return "No data."
	}

	var sb strings.Builder
	sb.WriteString("```\n")

	// Header.
	sb.WriteString(strings.Join(t.Headers, " | "))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("-", len(strings.Join(t.Headers, " | "))))
	sb.WriteString("\n")

	for _, row := range t.Rows {
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteString("\n")
	}
	sb.WriteString("```")
	return sb.String()
}

// escapeMarkdown escapes Telegram MarkdownV2 special characters.
func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_",
		"*", "\\*",
		"[", "\\[",
		"]", "\\]",
		"(", "\\(",
		")", "\\)",
		"~", "\\~",
		"`", "\\`",
		">", "\\>",
		"#", "\\#",
		"+", "\\+",
		"-", "\\-",
		"=", "\\=",
		"|", "\\|",
		"{", "\\{",
		"}", "\\}",
		".", "\\.",
		"!", "\\!",
	)
	return replacer.Replace(s)
}
