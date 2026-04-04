package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

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
}

// UpdateChatCommands processes a chat:commands:updated event and re-registers
// the commands as Discord slash commands.
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

	if b.botUserID == "" || b.guildID == "" {
		return
	}

	// Register each chat command as a Discord slash command.
	// Chat commands use "namespace:name" format — map to "/namespace-name" or "/namespace name" subcommand.
	registered := 0
	for _, cmd := range payload.Commands {
		discordName := strings.ReplaceAll(cmd.Name, ":", "-")
		appCmd := &discordgo.ApplicationCommand{
			Name:        discordName,
			Description: cmd.Description,
		}
		if _, err := b.session.ApplicationCommandCreate(b.botUserID, b.guildID, appCmd); err != nil {
			log.Printf("chatcmds: register /%s: %v", discordName, err)
			continue
		}
		registered++
	}
	log.Printf("chatcmds: registered %d/%d chat commands as Discord slash commands", registered, len(payload.Commands))
}

// HandleChatCommand routes a Discord slash command invocation through the
// chat command server's /invoke endpoint. Returns true if handled.
func (b *Bot) HandleChatCommand(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if b.sdk == nil {
		return false
	}

	data := i.ApplicationCommandData()
	discordName := data.Name

	// Check if this is a chat command (registered via chat:commands:updated).
	b.chatCmds.mu.RLock()
	var matched *chatCmdEntry
	for _, cmd := range b.chatCmds.commands {
		if strings.ReplaceAll(cmd.Name, ":", "-") == discordName {
			matched = &chatCmdEntry{Name: cmd.Name, Description: cmd.Description, PluginID: cmd.PluginID}
			break
		}
	}
	b.chatCmds.mu.RUnlock()

	if matched == nil {
		return false
	}

	// Acknowledge immediately.
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Build params from Discord options.
	params := make(map[string]string)
	for _, opt := range data.Options {
		params[opt.Name] = fmt.Sprintf("%v", opt.Value)
	}

	// Invoke via chat command server.
	invokeBody, _ := json.Marshal(map[string]interface{}{
		"command":  matched.Name,
		"params":   params,
		"platform": "discord",
		"user_id":  i.Member.User.ID,
	})

	raw, err := b.sdk.RouteToPlugin(context.Background(), "infra-chat-command-server", "POST", "/invoke", bytes.NewReader(invokeBody))
	if err != nil {
		log.Printf("chatcmds: invoke %s failed: %v", matched.Name, err)
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "Command failed: " + err.Error(),
		})
		return true
	}

	// Parse ChatCommandResponse and render for Discord.
	var resp pluginsdk.ChatCommandResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: "Invalid response from command server."})
		return true
	}

	webhookParams := renderChatCommandForDiscord(resp)
	s.FollowupMessageCreate(i.Interaction, true, webhookParams)
	return true
}

// renderChatCommandForDiscord converts a ChatCommandResponse to Discord webhook params.
func renderChatCommandForDiscord(resp pluginsdk.ChatCommandResponse) *discordgo.WebhookParams {
	params := &discordgo.WebhookParams{}

	switch resp.Type {
	case "text":
		params.Content = resp.Text

	case "error":
		params.Content = "Error: " + resp.Text

	case "table":
		if resp.Table != nil {
			params.Content = renderTable(resp.Table)
		}

	case "embed":
		for _, e := range resp.Embeds {
			embed := &discordgo.MessageEmbed{
				Title: e.Title,
				Color: e.Color,
			}
			for _, f := range e.Fields {
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   f.Name,
					Value:  f.Value,
					Inline: f.Inline,
				})
			}
			params.Embeds = append(params.Embeds, embed)
		}
	}

	return params
}

// renderTable formats a TableContent as a Discord code block.
func renderTable(t *pluginsdk.TableContent) string {
	if len(t.Rows) == 0 {
		return "No data."
	}

	// Calculate column widths.
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("```\n")

	// Header.
	for i, h := range t.Headers {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(fmt.Sprintf("%-*s", widths[i], h))
	}
	sb.WriteString("\n")

	// Separator.
	for i, w := range widths {
		if i > 0 {
			sb.WriteString("-+-")
		}
		sb.WriteString(strings.Repeat("-", w))
	}
	sb.WriteString("\n")

	// Rows.
	for _, row := range t.Rows {
		for i, cell := range row {
			if i > 0 {
				sb.WriteString(" | ")
			}
			if i < len(widths) {
				sb.WriteString(fmt.Sprintf("%-*s", widths[i], cell))
			} else {
				sb.WriteString(cell)
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("```")
	return sb.String()
}
