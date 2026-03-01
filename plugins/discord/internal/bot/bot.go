package bot

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"roboslop/plugins/discord/internal/kernel"
)

const maxMessageLength = 2000

// Bot manages the Discord bot session.
type Bot struct {
	session       *discordgo.Session
	kernelClient  *kernel.Client
	agentConfigID *uint
	botUserID     string
}

// New creates a new Bot instance. It does not open the connection yet.
func New(token string, kernelClient *kernel.Client, agentConfigID *uint) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent

	return &Bot{
		session:       session,
		kernelClient:  kernelClient,
		agentConfigID: agentConfigID,
	}, nil
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

	// Call kernel agent
	response, err := b.kernelClient.ChatWithAgent(content, b.agentConfigID)
	if err != nil {
		log.Printf("Error calling kernel: %v", err)
		s.ChannelMessageSend(m.ChannelID, "Sorry, I encountered an error processing your message.")
		return
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
