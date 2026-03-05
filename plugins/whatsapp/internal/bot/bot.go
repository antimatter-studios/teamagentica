package bot

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/whatsapp/internal/kernel"
	waClient "github.com/antimatter-studios/teamagentica/plugins/whatsapp/internal/whatsapp"
)

// Bot handles incoming WhatsApp messages.
type Bot struct {
	wa           *waClient.Client
	kernelClient *kernel.Client
	sdk          *pluginsdk.Client
	pluginID     string
	debug        bool
	aliases      *alias.AliasMap
	defaultAgent atomic.Pointer[string]
}

// NewBot creates a new WhatsApp bot.
func NewBot(wa *waClient.Client, kernelClient *kernel.Client, pluginID string, debug bool, aliases *alias.AliasMap, defaultAgent string) *Bot {
	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}
	if defaultAgent != "" {
		log.Printf("Coordinator agent: %s", defaultAgent)
	}
	b := &Bot{
		wa:           wa,
		kernelClient: kernelClient,
		pluginID:     pluginID,
		debug:        debug,
		aliases:      aliases,
	}
	if defaultAgent != "" {
		b.defaultAgent.Store(&defaultAgent)
	}
	return b
}

// SetDefaultAgent atomically updates the coordinator agent plugin ID.
func (b *Bot) SetDefaultAgent(agent string) {
	b.defaultAgent.Store(&agent)
	log.Printf("Coordinator agent updated: %s", agent)
}

// getDefaultAgent atomically reads the coordinator agent plugin ID.
func (b *Bot) getDefaultAgent() string {
	if p := b.defaultAgent.Load(); p != nil {
		return *p
	}
	return ""
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

	// Extract text from message based on type.
	text := b.extractText(msg)

	if text == "" {
		if b.debug {
			log.Printf("[message] empty text from %s, skipping", chatID)
		}
		return
	}

	log.Printf("[message] from %s (%s): %s", senderName, chatID, truncate(text, 100))
	b.emitEvent("message_received", fmt.Sprintf("from %s: %s", senderName, truncate(text, 100)))

	// Handle commands.
	if strings.HasPrefix(text, "/") {
		b.handleCommand(chatID, senderName, text)
		return
	}

	// Check for direct @mention routing (fast path).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(text)
		if result.Target != nil {
			b.handleAliasRoute(chatID, senderName, result)
			return
		}
	}

	// Route to coordinator agent (or default AI agent).
	coordinatorID := b.resolveDefaultAgent()
	systemPrompt := b.aliases.SystemPromptBlock()

	var response string
	var err error
	if coordinatorID != "" && systemPrompt != "" {
		response, err = b.kernelClient.ChatWithAgentDirect(chatID, coordinatorID, "", text, nil, systemPrompt)
	} else if coordinatorID != "" {
		response, err = b.kernelClient.ChatWithAgentDirect(chatID, coordinatorID, "", text, nil, "")
	} else {
		response, err = b.kernelClient.ChatWithAgent(chatID, text, nil)
	}

	if err != nil {
		log.Printf("[message] AI agent error: %v", err)
		b.emitEvent("error", fmt.Sprintf("agent error: %v", err))
		response = "Sorry, the AI agent is not available right now."
	} else {
		b.emitEvent("agent_response", fmt.Sprintf("len=%d chars", len(response)))

		// Check if coordinator delegated.
		if delegatedAlias, delegatedMsg, ok := alias.ParseCoordinatorResponse(response); ok {
			if target := b.aliases.Resolve(delegatedAlias); target != nil {
				b.emitEvent("coordinator_delegate", fmt.Sprintf("@%s → %s", delegatedAlias, target.PluginID))
				delegatedResp, delegErr := b.kernelClient.ChatWithAgentDirect(
					chatID, target.PluginID, target.Model, delegatedMsg, nil, "")
				if delegErr != nil {
					response = fmt.Sprintf("Failed to reach @%s: %v", delegatedAlias, delegErr)
				} else {
					response = delegatedResp
				}
			}
		}
	}

	if err := b.wa.SendText(chatID, response); err != nil {
		log.Printf("[message] send error: %v", err)
		b.emitEvent("error", fmt.Sprintf("send error: %v", err))
	}
}

// extractText pulls text content from any message type.
func (b *Bot) extractText(msg waClient.Message) string {
	switch msg.Type {
	case "text":
		if msg.Text != nil {
			return msg.Text.Body
		}
	case "image":
		if msg.Image != nil && msg.Image.Caption != "" {
			return msg.Image.Caption
		}
		return "What's in this image?"
	case "location":
		if msg.Location != nil {
			if msg.Location.Name != "" {
				return fmt.Sprintf("Location: %s, %s (%f, %f)",
					msg.Location.Name, msg.Location.Address,
					msg.Location.Latitude, msg.Location.Longitude)
			}
			return fmt.Sprintf("Location: %f, %f",
				msg.Location.Latitude, msg.Location.Longitude)
		}
	case "contacts":
		if len(msg.Contacts) > 0 {
			c := msg.Contacts[0]
			phone := ""
			if len(c.Phones) > 0 {
				phone = c.Phones[0].Phone
			}
			return fmt.Sprintf("Contact shared: %s %s", c.Name.FormattedName, phone)
		}
	case "audio", "video", "document":
		// Media without caption.
		if msg.Audio != nil {
			return "[Voice message received]"
		}
		if msg.Video != nil {
			return "[Video received]"
		}
		if msg.Document != nil && msg.Document.Filename != "" {
			return fmt.Sprintf("Document shared: %s", msg.Document.Filename)
		}
	}
	return ""
}

// handleCommand processes bot commands.
func (b *Bot) handleCommand(chatID, senderName, text string) {
	switch {
	case text == "/help" || text == "/start":
		helpMsg := "Available commands:\n\n" +
			"/clear — Clear conversation history\n" +
			"/aliases — List configured @mention aliases\n" +
			"/help — Show this message\n\n"
		if !b.aliases.IsEmpty() {
			helpMsg += "Use @nickname to route messages directly to a specific agent or tool.\n" +
				"Type /aliases to see the full list."
		} else {
			helpMsg += "Or just send any message to chat with the AI."
		}
		b.wa.SendText(chatID, helpMsg)

	case text == "/clear" || text == "/reset":
		b.kernelClient.ClearHistory(chatID)
		b.wa.SendText(chatID, "Conversation cleared.")

	case text == "/aliases":
		b.handleAliasesCommand(chatID)

	default:
		b.wa.SendText(chatID, "Unknown command. Type /help for available commands.")
	}
}

// handleAliasRoute routes a message based on an @mention match.
func (b *Bot) handleAliasRoute(chatID, senderName string, result alias.ParseResult) {
	target := result.Target
	message := result.Remainder
	if message == "" {
		b.wa.SendText(chatID, fmt.Sprintf("Usage: @%s <message>", result.Alias))
		return
	}

	b.emitEvent("alias_route", fmt.Sprintf("@%s → %s from %s", result.Alias, target.PluginID, senderName))

	response, err := b.kernelClient.ChatWithAgentDirect(chatID, target.PluginID, target.Model, message, nil, "")
	if err != nil {
		log.Printf("[alias] error @%s → %s: %v", result.Alias, target.PluginID, err)
		b.wa.SendText(chatID, fmt.Sprintf("@%s is not available: %v", result.Alias, err))
		return
	}

	b.wa.SendText(chatID, response)
}

// handleAliasesCommand lists all configured @mention aliases.
func (b *Bot) handleAliasesCommand(chatID string) {
	if b.aliases.IsEmpty() {
		b.wa.SendText(chatID, "No aliases configured.\n\nSet the ALIASES environment variable to enable @mention routing.")
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
	if da := b.getDefaultAgent(); da != "" {
		sb.WriteString(fmt.Sprintf("\nCoordinator: %s", da))
	}
	sb.WriteString("\n\nUsage: @nickname <message>")
	b.wa.SendText(chatID, sb.String())
}

// resolveDefaultAgent returns the coordinator agent plugin ID,
// falling back to auto-discovery if not set.
func (b *Bot) resolveDefaultAgent() string {
	if da := b.getDefaultAgent(); da != "" {
		return da
	}
	agentID, err := b.kernelClient.FindAIAgent()
	if err != nil {
		return ""
	}
	return agentID
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
