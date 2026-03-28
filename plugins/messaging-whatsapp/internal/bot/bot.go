package bot

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/relay"
	waClient "github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/whatsapp"
)

// Bot handles incoming WhatsApp messages.
type Bot struct {
	wa       *waClient.Client
	relay    *relay.Client
	sdk      *pluginsdk.Client
	pluginID string
	debug    bool
	aliases  *alias.AliasMap
}

// NewBot creates a new WhatsApp bot.
func NewBot(wa *waClient.Client, rc *relay.Client, pluginID string, debug bool, aliases *alias.AliasMap) *Bot {
	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}
	return &Bot{
		wa:       wa,
		relay:    rc,
		pluginID: pluginID,
		debug:    debug,
		aliases:  aliases,
	}
}

// SetSDK attaches the plugin SDK client.
func (b *Bot) SetSDK(sdk *pluginsdk.Client) {
	b.sdk = sdk
}

// emitEvent sends a debug event to the kernel console.
func (b *Bot) emitEvent(eventType, detail string) {
	if b.sdk != nil {
		b.sdk.ReportEvent(eventType, detail)
	}
}

// VerifyWebhook handles the GET webhook verification from Meta.
// GET /webhook?hub.mode=subscribe&hub.verify_token=TOKEN&hub.challenge=CHALLENGE
func (b *Bot) VerifyWebhook(verifyToken string) gin.HandlerFunc {
	return func(c *gin.Context) {
		mode := c.Query("hub.mode")
		token := c.Query("hub.verify_token")
		challenge := c.Query("hub.challenge")

		if mode == "subscribe" && token == verifyToken {
			log.Printf("[webhook] verification successful")
			b.emitEvent("webhook_verified", "Meta webhook verified")
			c.String(http.StatusOK, challenge)
			return
		}

		log.Printf("[webhook] verification failed: mode=%s token_match=%v", mode, token == verifyToken)
		c.String(http.StatusForbidden, "verification failed")
	}
}

// HandleWebhook processes incoming WhatsApp messages.
// POST /webhook
func (b *Bot) HandleWebhook(c *gin.Context) {
	var payload waClient.WebhookPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		log.Printf("[webhook] invalid payload: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}

	// Always respond 200 quickly to avoid Meta retries.
	c.JSON(http.StatusOK, gin.H{"status": "ok"})

	// Process messages asynchronously.
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			for _, msg := range change.Value.Messages {
				// Find sender name from contacts.
				senderName := msg.From
				for _, contact := range change.Value.Contacts {
					if contact.WaID == msg.From {
						senderName = contact.Profile.Name
						break
					}
				}
				go b.handleMessage(msg, senderName)
			}
		}
	}
}

// handleMessage processes a single incoming message.
func (b *Bot) handleMessage(msg waClient.Message, senderName string) {
	chatID := msg.From

	if b.debug {
		log.Printf("[message] from=%s (%s) type=%s id=%s", senderName, chatID, msg.Type, msg.ID)
	}

	// Mark as read.
	b.wa.MarkRead(msg.ID)

	// Extract text and media URLs from message based on type.
	text, imageURLs := b.extractContent(msg)

	if text == "" {
		if b.debug {
			log.Printf("[message] empty text from %s, skipping", chatID)
		}
		return
	}

	log.Printf("[message] from %s (%s): %s (media=%d)", senderName, chatID, truncate(text, 100), len(imageURLs))
	b.emitEvent("message_received", fmt.Sprintf("from %s: %s", senderName, truncate(text, 100)))

	// Handle commands.
	if strings.HasPrefix(text, "/") {
		b.handleCommand(chatID, text)
		return
	}

	// Route everything through the relay — it handles @alias routing,
	// coordinator resolution, persona injection, and conversation memory.
	resp, err := b.relay.Chat(chatID, text, imageURLs)
	if err != nil {
		var ue *relay.UserError
		if errors.As(err, &ue) {
			b.wa.SendText(chatID, ue.Message)
		} else {
			log.Printf("[message] relay error: %v", err)
			b.emitEvent("error", fmt.Sprintf("relay error: %v", err))
			b.wa.SendText(chatID, "Sorry, I encountered an error processing your message.")
		}
		return
	}

	b.emitEvent("agent_response", fmt.Sprintf("responder=%s len=%d chars", resp.Responder, len(resp.Response)))

	if err := b.wa.SendText(chatID, resp.Response); err != nil {
		log.Printf("[message] send error: %v", err)
		b.emitEvent("error", fmt.Sprintf("send error: %v", err))
	}
}

// extractContent pulls text content and media URLs from any message type.
// Returns the text to send to the agent and any media URLs for vision/processing.
func (b *Bot) extractContent(msg waClient.Message) (string, []string) {
	var imageURLs []string

	switch msg.Type {
	case "text":
		if msg.Text != nil {
			return msg.Text.Body, nil
		}
	case "image":
		if msg.Image != nil {
			text := "What's in this image?"
			if msg.Image.Caption != "" {
				text = msg.Image.Caption
			}
			if mediaURL, err := b.wa.DownloadMedia(msg.Image.ID); err != nil {
				log.Printf("[media] failed to get image URL: %v", err)
			} else {
				imageURLs = append(imageURLs, mediaURL)
			}
			return text, imageURLs
		}
	case "video":
		if msg.Video != nil {
			text := "I sent you a video."
			if msg.Video.Caption != "" {
				text = msg.Video.Caption
			}
			if mediaURL, err := b.wa.DownloadMedia(msg.Video.ID); err != nil {
				log.Printf("[media] failed to get video URL: %v", err)
			} else {
				imageURLs = append(imageURLs, mediaURL)
			}
			return text, imageURLs
		}
	case "audio":
		if msg.Audio != nil {
			text := "I sent you a voice message."
			if mediaURL, err := b.wa.DownloadMedia(msg.Audio.ID); err != nil {
				log.Printf("[media] failed to get audio URL: %v", err)
			} else {
				imageURLs = append(imageURLs, mediaURL)
			}
			return text, imageURLs
		}
	case "location":
		if msg.Location != nil {
			if msg.Location.Name != "" {
				return fmt.Sprintf("Location: %s, %s (%f, %f)",
					msg.Location.Name, msg.Location.Address,
					msg.Location.Latitude, msg.Location.Longitude), nil
			}
			return fmt.Sprintf("Location: %f, %f",
				msg.Location.Latitude, msg.Location.Longitude), nil
		}
	case "contacts":
		if len(msg.Contacts) > 0 {
			c := msg.Contacts[0]
			phone := ""
			if len(c.Phones) > 0 {
				phone = c.Phones[0].Phone
			}
			return fmt.Sprintf("Contact shared: %s %s", c.Name.FormattedName, phone), nil
		}
	case "document":
		if msg.Document != nil && msg.Document.Filename != "" {
			return fmt.Sprintf("Document shared: %s", msg.Document.Filename), nil
		}
	}
	return "", nil
}

// handleCommand processes bot commands.
func (b *Bot) handleCommand(chatID, text string) {
	switch {
	case text == "/help" || text == "/start":
		helpMsg := "Available commands:\n\n" +
			"/aliases — List configured @mention aliases\n" +
			"/help — Show this message\n\n"
		if !b.aliases.IsEmpty() {
			helpMsg += "Use @nickname to route messages directly to a specific agent or tool.\n" +
				"Type /aliases to see the full list."
		} else {
			helpMsg += "Or just send any message to chat with the AI."
		}
		b.wa.SendText(chatID, helpMsg)

	case text == "/aliases":
		b.handleAliasesCommand(chatID)

	default:
		b.wa.SendText(chatID, "Unknown command. Type /help for available commands.")
	}
}

// handleAliasesCommand lists all configured @mention aliases.
func (b *Bot) handleAliasesCommand(chatID string) {
	if b.aliases.IsEmpty() {
		b.wa.SendText(chatID, "No aliases configured.")
		return
	}

	var sb strings.Builder
	sb.WriteString("Configured aliases:\n\n")
	for _, entry := range b.aliases.List() {
		switch entry.Target.Type {
		case alias.TargetAgent:
			desc := entry.Target.PluginID
			if entry.Target.Model != "" {
				desc += " (" + entry.Target.Model + ")"
			}
			sb.WriteString(fmt.Sprintf("@%s → %s\n", entry.Alias, desc))
		case alias.TargetImage:
			sb.WriteString(fmt.Sprintf("@%s → image: %s\n", entry.Alias, entry.Target.PluginID))
		case alias.TargetVideo:
			sb.WriteString(fmt.Sprintf("@%s → video: %s\n", entry.Alias, entry.Target.PluginID))
		}
	}
	sb.WriteString("\nUsage: @nickname <message>")
	b.wa.SendText(chatID, sb.String())
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
