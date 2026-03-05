package bot

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/discord/internal/kernel"
)

const maxMessageLength = 2000

// Bot manages the Discord bot session.
type Bot struct {
	session      *discordgo.Session
	kernelClient *kernel.Client
	botUserID    string
	aliases      *alias.AliasMap
	defaultAgent atomic.Pointer[string]
}

// New creates a new Bot instance. It does not open the connection yet.
func New(token string, kernelClient *kernel.Client, aliases *alias.AliasMap, defaultAgent string) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}
	if defaultAgent != "" {
		log.Printf("Coordinator agent: %s", defaultAgent)
	}

	b := &Bot{
		session:      session,
		kernelClient: kernelClient,
		aliases:      aliases,
	}
	if defaultAgent != "" {
		b.defaultAgent.Store(&defaultAgent)
	}
	return b, nil
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

// Start opens the Discord connection and begins listening for messages.
func (b *Bot) Start() error {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onMessageCreate)

	if err := b.session.Open(); err != nil {
		return fmt.Errorf("opening discord connection: %w", err)
	}

	log.Println("Discord bot is now running")
	return nil
}

// Stop gracefully closes the Discord connection.
func (b *Bot) Stop() error {
	log.Println("Shutting down Discord bot...")
	return b.session.Close()
}

// onReady is called when the bot successfully connects to Discord.
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.botUserID = r.User.ID
	log.Printf("Connected to Discord as %s#%s (ID: %s)", r.User.Username, r.User.Discriminator, r.User.ID)
}

// onMessageCreate handles incoming messages.
func (b *Bot) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages from bots (including ourselves)
	if m.Author.Bot {
		return
	}

	// Check if this is a DM or the bot was mentioned
	isDM := m.GuildID == ""
	isMentioned := b.isBotMentioned(m.Message)

	if !isDM && !isMentioned {
		return
	}

	// Strip bot mention from message text
	content := b.stripBotMention(m.Content)
	content = strings.TrimSpace(content)

	if content == "" {
		return
	}

	log.Printf("Message from %s: %s", m.Author.Username, content)

	// Show typing indicator
	s.ChannelTyping(m.ChannelID)

	// Check for direct @mention routing (fast path).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(content)
		if result.Target != nil && result.Target.Type == alias.TargetAgent {
			if result.Remainder == "" {
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Usage: @%s <message>", result.Alias))
				return
			}
			response, err := b.kernelClient.ChatWithAgentDirect(
				result.Target.PluginID, result.Target.Model, result.Remainder, "")
			if err != nil {
				log.Printf("Alias route error (@%s): %v", result.Alias, err)
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("@%s is not available: %v", result.Alias, err))
				return
			}
			b.sendResponse(s, m.ChannelID, response)
			return
		}
	}

	// Route to coordinator agent (or default AI agent).
	coordinatorID := b.resolveDefaultAgent()
	systemPrompt := b.aliases.SystemPromptBlock()

	var response string
	var err error
	if coordinatorID != "" {
		response, err = b.kernelClient.ChatWithAgentDirect(coordinatorID, "", content, systemPrompt)
	} else {
		response, err = b.kernelClient.ChatWithAgent(content)
	}

	if err != nil {
		log.Printf("Error calling kernel: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Sorry, I encountered an error processing your message.")
		return
	}

	// Check if coordinator delegated to another alias.
	if delegatedAlias, delegatedMsg, ok := alias.ParseCoordinatorResponse(response); ok {
		if target := b.aliases.Resolve(delegatedAlias); target != nil && target.Type == alias.TargetAgent {
			delegatedResp, delegErr := b.kernelClient.ChatWithAgentDirect(
				target.PluginID, target.Model, delegatedMsg, "")
			if delegErr != nil {
				response = fmt.Sprintf("Failed to reach @%s: %v", delegatedAlias, delegErr)
			} else {
				response = delegatedResp
			}
		}
	}

	// Send the response, splitting if necessary
	if err := b.sendResponse(s, m.ChannelID, response); err != nil {
		log.Printf("Error sending response: %v", err)
	}
}

// isBotMentioned checks whether the bot was mentioned in the message.
func (b *Bot) isBotMentioned(m *discordgo.Message) bool {
	for _, mention := range m.Mentions {
		if mention.ID == b.botUserID {
			return true
		}
	}
	return false
}

// stripBotMention removes the bot's @mention from the message text.
func (b *Bot) stripBotMention(content string) string {
	if b.botUserID == "" {
		return content
	}
	// Discord mentions look like <@USER_ID> or <@!USER_ID>
	content = strings.ReplaceAll(content, "<@"+b.botUserID+">", "")
	content = strings.ReplaceAll(content, "<@!"+b.botUserID+">", "")
	return content
}

// resolveDefaultAgent returns the configured coordinator agent plugin ID,
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

// sendResponse sends a message to the channel, splitting into chunks if over 2000 chars.
func (b *Bot) sendResponse(s *discordgo.Session, channelID, response string) error {
	if len(response) == 0 {
		response = "(empty response)"
	}

	chunks := splitMessage(response, maxMessageLength)
	for _, chunk := range chunks {
		if _, err := s.ChannelMessageSend(channelID, chunk); err != nil {
			return fmt.Errorf("sending message chunk: %w", err)
		}
	}
	return nil
}

// splitMessage splits text into chunks of at most maxLen characters,
// preferring to break at newlines or spaces.
func splitMessage(text string, maxLen int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// Try to find a good break point
		chunk := text[:maxLen]
		breakIdx := -1

		// Prefer breaking at newline
		if idx := strings.LastIndex(chunk, "\n"); idx > 0 {
			breakIdx = idx
		} else if idx := strings.LastIndex(chunk, " "); idx > 0 {
			// Fall back to space
			breakIdx = idx
		}

		if breakIdx > 0 {
			chunks = append(chunks, text[:breakIdx])
			text = text[breakIdx+1:]
		} else {
			// No good break point, hard cut
			chunks = append(chunks, chunk)
			text = text[maxLen:]
		}
	}

	return chunks
}
