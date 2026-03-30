package channels

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Handler serves Discord channel management tool endpoints.
type Handler struct {
	session   func() *discordgo.Session
	guildID   func() string
	callbacks *CallbackStore
}

// NewHandler creates a channel management handler.
// Accepts getter funcs so the handler doesn't hold stale references during init.
func NewHandler(session func() *discordgo.Session, guildID func() string, callbacks *CallbackStore) *Handler {
	return &Handler{session: session, guildID: guildID, callbacks: callbacks}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, map[string]string{"error": msg})
}

func (h *Handler) checkReady(w http.ResponseWriter) bool {
	if h.session() == nil {
		h.writeError(w, http.StatusServiceUnavailable, "discord session not ready")
		return false
	}
	if h.guildID() == "" {
		h.writeError(w, http.StatusServiceUnavailable, "DISCORD_GUILD_ID not configured")
		return false
	}
	return true
}

// Tools returns tool definitions for agent discovery via GET /tools.
func (h *Handler) Tools(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{"tools": []map[string]any{
		{
			"name":        "create_category",
			"description": "Create a Discord category channel for organizing workspace channels. Categories group related text channels together.",
			"endpoint":    "/channels/create-category",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]string{"type": "string", "description": "Category name (typically the workspace name)"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "create_channel",
			"description": "Create a Discord text channel, optionally under a category. Channel names are auto-formatted to lowercase with hyphens.",
			"endpoint":    "/channels/create",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]string{"type": "string", "description": "Channel name"},
					"category_id": map[string]string{"type": "string", "description": "Parent category ID to place the channel under"},
					"topic":       map[string]string{"type": "string", "description": "Channel topic/description"},
				},
				"required": []string{"name"},
			},
		},
		{
			"name":        "list_channels",
			"description": "List Discord channels in the guild, optionally filtered by category.",
			"endpoint":    "/channels/list",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"category_id": map[string]string{"type": "string", "description": "Only list channels under this category"},
				},
			},
		},
		{
			"name":        "delete_channel",
			"description": "Delete a Discord channel by ID. This action is irreversible.",
			"endpoint":    "/channels/delete",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]string{"type": "string", "description": "ID of the channel to delete"},
				},
				"required": []string{"channel_id"},
			},
		},
		{
			"name":        "send_menu",
			"description": "Send an interactive menu to a Discord channel. When a user selects an option, the callback_message is sent back to you as a new chat message. Use this for browsable lists, confirmations, or multi-step workflows. Max 25 options for select style, max 25 buttons (5 rows of 5).",
			"endpoint":    "/channels/send-menu",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channel_id": map[string]string{"type": "string", "description": "Discord channel ID to send the menu to"},
					"title":      map[string]string{"type": "string", "description": "Header text displayed above the menu"},
					"options": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label":            map[string]string{"type": "string", "description": "Display text for the option (max 100 chars for select, 80 for buttons)"},
								"description":      map[string]string{"type": "string", "description": "Optional description shown below the label (select style only, max 100 chars)"},
								"callback_message": map[string]string{"type": "string", "description": "Message sent back to you when this option is selected"},
							},
							"required": []string{"label", "callback_message"},
						},
						"description": "List of selectable options",
					},
					"style": map[string]string{"type": "string", "description": "Display style: 'select' for dropdown menu (default), 'buttons' for clickable buttons"},
				},
				"required": []string{"channel_id", "title", "options"},
			},
		},
	}})
}

// CreateCategory handles POST /channels/create-category.
func (h *Handler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	if !h.checkReady(w) {
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	ch, err := h.session().GuildChannelCreateComplex(h.guildID(), discordgo.GuildChannelCreateData{
		Name: req.Name,
		Type: discordgo.ChannelTypeGuildCategory,
	})
	if err != nil {
		log.Printf("create category failed: %v", err)
		h.writeError(w, http.StatusBadGateway, "discord API error: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{
		"id":   ch.ID,
		"name": ch.Name,
		"type": "category",
	})
}

// CreateChannel handles POST /channels/create.
func (h *Handler) CreateChannel(w http.ResponseWriter, r *http.Request) {
	if !h.checkReady(w) {
		return
	}

	var req struct {
		Name       string `json:"name"`
		CategoryID string `json:"category_id"`
		Topic      string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		h.writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	data := discordgo.GuildChannelCreateData{
		Name:     SanitizeChannelName(req.Name),
		Type:     discordgo.ChannelTypeGuildText,
		ParentID: req.CategoryID,
		Topic:    req.Topic,
	}

	ch, err := h.session().GuildChannelCreateComplex(h.guildID(), data)
	if err != nil {
		log.Printf("create channel failed: %v", err)
		h.writeError(w, http.StatusBadGateway, "discord API error: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{
		"id":          ch.ID,
		"name":        ch.Name,
		"category_id": ch.ParentID,
		"topic":       ch.Topic,
	})
}

// ListChannels handles POST /channels/list.
func (h *Handler) ListChannels(w http.ResponseWriter, r *http.Request) {
	if !h.checkReady(w) {
		return
	}

	var req struct {
		CategoryID string `json:"category_id"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	channels, err := h.session().GuildChannels(h.guildID())
	if err != nil {
		log.Printf("list channels failed: %v", err)
		h.writeError(w, http.StatusBadGateway, "discord API error: "+err.Error())
		return
	}

	var result []map[string]string
	for _, ch := range channels {
		if req.CategoryID != "" && ch.ParentID != req.CategoryID {
			continue
		}
		chType := "text"
		switch ch.Type {
		case discordgo.ChannelTypeGuildCategory:
			chType = "category"
		case discordgo.ChannelTypeGuildVoice:
			chType = "voice"
		case discordgo.ChannelTypeGuildStageVoice:
			chType = "stage"
		case discordgo.ChannelTypeGuildForum:
			chType = "forum"
		}
		result = append(result, map[string]string{
			"id":          ch.ID,
			"name":        ch.Name,
			"type":        chType,
			"category_id": ch.ParentID,
			"topic":       ch.Topic,
		})
	}

	if result == nil {
		result = []map[string]string{}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"channels": result})
}

// DeleteChannel handles POST /channels/delete.
func (h *Handler) DeleteChannel(w http.ResponseWriter, r *http.Request) {
	if !h.checkReady(w) {
		return
	}

	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChannelID == "" {
		h.writeError(w, http.StatusBadRequest, "channel_id is required")
		return
	}

	_, err := h.session().ChannelDelete(req.ChannelID)
	if err != nil {
		log.Printf("delete channel failed: %v", err)
		h.writeError(w, http.StatusBadGateway, "discord API error: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"deleted":    true,
		"channel_id": req.ChannelID,
	})
}

// SendMenu handles POST /channels/send-menu.
func (h *Handler) SendMenu(w http.ResponseWriter, r *http.Request) {
	if h.session() == nil {
		h.writeError(w, http.StatusServiceUnavailable, "discord session not ready")
		return
	}

	var req struct {
		ChannelID string `json:"channel_id"`
		Title     string `json:"title"`
		Options   []struct {
			Label           string `json:"label"`
			Description     string `json:"description"`
			CallbackMessage string `json:"callback_message"`
		} `json:"options"`
		Style string `json:"style"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ChannelID == "" || req.Title == "" || len(req.Options) == 0 {
		h.writeError(w, http.StatusBadRequest, "channel_id, title, and options are required")
		return
	}

	if req.Style == "" {
		req.Style = "select"
	}

	var components []discordgo.MessageComponent

	if req.Style == "buttons" {
		// Build button rows (max 5 buttons per row, max 5 rows).
		var row []discordgo.MessageComponent
		for i, opt := range req.Options {
			if i >= 25 {
				break
			}
			customID := h.callbacks.Store(opt.CallbackMessage)
			label := opt.Label
			if len(label) > 80 {
				label = label[:80]
			}
			row = append(row, discordgo.Button{
				Label:    label,
				Style:    discordgo.SecondaryButton,
				CustomID: customID,
			})
			if len(row) == 5 || i == len(req.Options)-1 || i == 24 {
				components = append(components, discordgo.ActionsRow{Components: row})
				row = nil
			}
		}
	} else {
		// Build select menu (max 25 options).
		var menuOptions []discordgo.SelectMenuOption
		for i, opt := range req.Options {
			if i >= 25 {
				break
			}
			customID := h.callbacks.Store(opt.CallbackMessage)
			smo := discordgo.SelectMenuOption{
				Label: opt.Label,
				Value: customID,
			}
			if opt.Description != "" {
				desc := opt.Description
				if len(desc) > 100 {
					desc = desc[:100]
				}
				smo.Description = desc
			}
			menuOptions = append(menuOptions, smo)
		}

		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    "menu_select",
						Placeholder: "Choose an option...",
						Options:     menuOptions,
					},
				},
			},
		}
	}

	msg, err := h.session().ChannelMessageSendComplex(req.ChannelID, &discordgo.MessageSend{
		Content:    req.Title,
		Components: components,
	})
	if err != nil {
		log.Printf("send menu failed: %v", err)
		h.writeError(w, http.StatusBadGateway, "discord API error: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]any{
		"message_id": msg.ID,
		"channel_id": msg.ChannelID,
		"options":    len(req.Options),
		"style":      req.Style,
	})
}

// SanitizeChannelName converts a name to Discord-safe format (lowercase, hyphens, max 100 chars).
func SanitizeChannelName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	// Remove characters that aren't alphanumeric, hyphens, or underscores.
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if len(result) > 100 {
		result = result[:100]
	}
	return result
}
