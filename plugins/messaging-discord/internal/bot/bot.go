package bot

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/kernel"
)

const maxMessageLength = 2000

// Bot manages the Discord bot session.
type Bot struct {
	session      *discordgo.Session
	kernelClient *kernel.Client
	botUserID    string
	aliases      *alias.AliasMap
	defaultAgent atomic.Pointer[string]
	debug        atomic.Bool
	sdk          *pluginsdk.Client
	msgBuffer    *MessageBuffer
}

// New creates a new Bot instance. It does not open the connection yet.
// The default agent must be set via the plugin config UI (config:update event).
func New(token string, kernelClient *kernel.Client, aliases *alias.AliasMap) (*Bot, error) {
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

	b := &Bot{
		session:      session,
		kernelClient: kernelClient,
		aliases:      aliases,
	}

	b.msgBuffer = NewMessageBuffer(1*time.Second, func(channelID string, text string, mediaURLs []string) {
		b.processBuffered(channelID, text, mediaURLs)
	})

	return b, nil
}

// SetMessageBufferMS updates the debounce duration in milliseconds.
func (b *Bot) SetMessageBufferMS(ms int) {
	if ms < 0 {
		ms = 0
	}
	b.msgBuffer.SetDuration(time.Duration(ms) * time.Millisecond)
	log.Printf("Message buffer duration updated: %dms", ms)
}

// SetSDK attaches the plugin SDK client for event reporting.
func (b *Bot) SetSDK(sdk *pluginsdk.Client) {
	b.sdk = sdk
}

// SetDebug atomically updates the debug mode.
func (b *Bot) SetDebug(enabled bool) {
	b.debug.Store(enabled)
	log.Printf("Debug mode: %v", enabled)
}

// emitEvent sends a debug event to the kernel console.
func (b *Bot) emitEvent(eventType, detail string) {
	if b.sdk != nil {
		b.sdk.ReportEvent(eventType, detail)
	}
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
	b.msgBuffer.Stop()
	return b.session.Close()
}

// onReady is called when the bot successfully connects to Discord.
func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	b.botUserID = r.User.ID
	log.Printf("Connected to Discord as %s#%s (ID: %s)", r.User.Username, r.User.Discriminator, r.User.ID)
}

// onMessageCreate handles incoming messages.
// Commands are processed immediately; all other messages are buffered per-channel.
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

	// Extract media URLs from attachments, embeds, and forwarded message snapshots.
	var mediaURLs []string
	mediaURLs = appendAttachmentURLs(mediaURLs, m.Attachments)
	mediaURLs = appendEmbedImageURLs(mediaURLs, m.Embeds)
	for _, snap := range m.MessageSnapshots {
		if snap.Message != nil {
			mediaURLs = appendAttachmentURLs(mediaURLs, snap.Message.Attachments)
			mediaURLs = appendEmbedImageURLs(mediaURLs, snap.Message.Embeds)
			// Use forwarded message text if the outer message is empty.
			if content == "" && snap.Message.Content != "" {
				content = snap.Message.Content
			}
			if b.debug.Load() {
				log.Printf("[message] snapshot: content=%q attachments=%d embeds=%d",
					truncate(snap.Message.Content, 100), len(snap.Message.Attachments), len(snap.Message.Embeds))
				for i, att := range snap.Message.Attachments {
					log.Printf("[message] snapshot attachment[%d]: filename=%q content_type=%q url=%q",
						i, att.Filename, att.ContentType, truncate(att.URL, 100))
				}
			}
		}
	}

	// If a forwarded message (MessageReference) has no extracted media,
	// try fetching the original message's attachments directly via API.
	if len(mediaURLs) == 0 && m.MessageReference != nil && m.MessageReference.MessageID != "" {
		if b.debug.Load() {
			log.Printf("[message] forwarded message ref: channel=%s message=%s type=%d",
				m.MessageReference.ChannelID, m.MessageReference.MessageID, m.MessageReference.Type)
		}
		chID := m.MessageReference.ChannelID
		if chID == "" {
			chID = m.ChannelID
		}
		origMsg, err := s.ChannelMessage(chID, m.MessageReference.MessageID)
		if err != nil {
			log.Printf("[message] failed to fetch referenced message: %v", err)
		} else {
			mediaURLs = appendAttachmentURLs(mediaURLs, origMsg.Attachments)
			mediaURLs = appendEmbedImageURLs(mediaURLs, origMsg.Embeds)
			if content == "" && origMsg.Content != "" {
				content = origMsg.Content
			}
			if b.debug.Load() {
				log.Printf("[message] fetched original message: content=%q attachments=%d embeds=%d media_extracted=%d",
					truncate(origMsg.Content, 100), len(origMsg.Attachments), len(origMsg.Embeds), len(mediaURLs))
			}
		}
	}

	// If no text but media attached, provide a default prompt
	if content == "" && len(mediaURLs) > 0 {
		content = "What's in this image?"
	}

	if content == "" {
		return
	}

	log.Printf("Message from %s: %s", m.Author.Username, content)

	if b.debug.Load() {
		b.emitEvent("message_received", fmt.Sprintf("from %s: %s", m.Author.Username, truncate(content, 200)))
	} else {
		b.emitEvent("message_received", fmt.Sprintf("from %s (%d chars)", m.Author.Username, len(content)))
	}

	// Show typing indicator on first message so user sees immediate feedback.
	s.ChannelTyping(m.ChannelID)

	// Buffer the message — will be flushed after debounce window.
	b.msgBuffer.Add(m.ChannelID, content, mediaURLs)
}

// processBuffered handles the merged text and media after the debounce timer fires.
func (b *Bot) processBuffered(channelID string, text string, mediaURLs []string) {
	s := b.session

	// Show typing indicator.
	s.ChannelTyping(channelID)

	// Check for direct @mention routing (fast path).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(text)
		if result.Target != nil {
			switch result.Target.Type {
			case alias.TargetAgent:
				if result.Remainder == "" {
					s.ChannelMessageSend(channelID, fmt.Sprintf("Usage: @%s <message>", result.Alias))
					return
				}
				b.emitEvent("alias_route", fmt.Sprintf("@%s → %s", result.Alias, result.Target.PluginID))
				response, err := b.kernelClient.ChatWithAgentDirect(
					result.Target.PluginID, result.Target.Model, result.Remainder, mediaURLs, false, result.Alias)
				if err != nil {
					log.Printf("Alias route error (@%s): %v", result.Alias, err)
					b.emitEvent("alias_error", fmt.Sprintf("@%s: %v", result.Alias, err))
					s.ChannelMessageSend(channelID, fmt.Sprintf("@%s is not available: %v", result.Alias, err))
					return
				}
				if b.debug.Load() {
					b.emitEvent("agent_response", fmt.Sprintf("from @%s: %s", result.Alias, truncate(response, 200)))
				} else {
					b.emitEvent("agent_response", fmt.Sprintf("from @%s (%d chars)", result.Alias, len(response)))
				}
				b.sendResponse(s, channelID, formatAttributedResponse(result.Alias, response))
			case alias.TargetImage:
				b.handleImageGenerate(s, channelID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
			case alias.TargetVideo:
				b.handleVideoGenerate(s, channelID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
			}
			return
		}
	}

	// Route to coordinator agent — requires DEFAULT_AGENT to be set in plugin config.
	coordinator := b.resolveDefaultAgent()
	if coordinator == nil {
		log.Printf("No coordinator agent configured — rejecting buffered message")
		b.emitEvent("error", "no coordinator agent configured")
		s.ChannelMessageSend(channelID, "No coordinator agent configured. Please set the Coordinator Agent in the plugin settings.")
		return
	}

	response, err := b.kernelClient.ChatWithAgentDirect(coordinator.PluginID, coordinator.Model, text, mediaURLs, true, "")

	if err != nil {
		log.Printf("Error calling kernel: %v", err)
		b.emitEvent("error", fmt.Sprintf("agent error: %v", err))
		s.ChannelMessageSend(channelID, "Sorry, I encountered an error processing your message.")
		return
	}

	if b.debug.Load() {
		b.emitEvent("agent_response", fmt.Sprintf("response: %s", truncate(response, 200)))
	} else {
		b.emitEvent("agent_response", fmt.Sprintf("response length=%d chars", len(response)))
	}

	// Check if coordinator delegated to another alias.
	if delegatedAlias, delegatedMsg, ok := alias.ParseCoordinatorResponse(response); ok {
		if target := b.aliases.Resolve(delegatedAlias); target != nil {
			b.emitEvent("coordinator_delegate", fmt.Sprintf("@%s → %s", delegatedAlias, target.PluginID))
			switch target.Type {
			case alias.TargetAgent:
				delegatedResp, delegErr := b.kernelClient.ChatWithAgentDirect(
					target.PluginID, target.Model, delegatedMsg, nil, false, delegatedAlias)
				if delegErr != nil {
					response = fmt.Sprintf("Failed to reach @%s: %v", delegatedAlias, delegErr)
				} else {
					response = formatAttributedResponse(delegatedAlias, delegatedResp)
				}
			case alias.TargetImage:
				b.handleImageGenerate(s, channelID, "", stripToolPrefix(target.PluginID), delegatedMsg)
				return
			case alias.TargetVideo:
				b.handleVideoGenerate(s, channelID, "", stripToolPrefix(target.PluginID), delegatedMsg)
				return
			}
		}
	}

	// Attribute the response to the coordinator's alias (or plugin ID).
	if coordinator != nil && !strings.HasPrefix(response, "[@") {
		responderName := b.aliases.FindAliasByPluginID(coordinator.PluginID)
		if responderName == "" {
			responderName = coordinator.PluginID
		}
		response = formatAttributedResponse(responderName, response)
	}

	// Send the response, splitting if necessary
	if err := b.sendResponse(s, channelID, response); err != nil {
		log.Printf("Error sending response: %v", err)
		b.emitEvent("error", fmt.Sprintf("send error: %v", err))
	} else {
		b.emitEvent("message_sent", fmt.Sprintf("response (%d chars)", len(response)))
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

// resolvedAgent holds the plugin ID and optional model for a resolved coordinator.
type resolvedAgent struct {
	PluginID string
	Model    string
}

// resolveDefaultAgent returns the configured coordinator agent.
// Returns nil if no default agent is set — callers must treat this as an error.
// The DEFAULT_AGENT config stores an alias name, so we resolve it via the alias map.
func (b *Bot) resolveDefaultAgent() *resolvedAgent {
	da := b.getDefaultAgent()
	if da == "" {
		return nil
	}
	if target := b.aliases.Resolve(da); target != nil {
		return &resolvedAgent{PluginID: target.PluginID, Model: target.Model}
	}
	return &resolvedAgent{PluginID: da}
}

// formatAttributedResponse prefixes a response with the responder's name
// so users can see who authored the message.
func formatAttributedResponse(name, response string) string {
	if name == "" {
		return response
	}
	return fmt.Sprintf("[@%s]\n%s", name, response)
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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

// stripToolPrefix removes the "tool-" prefix from a plugin ID for use as a
// provider name in image/video generation.
func stripToolPrefix(pluginID string) string {
	return strings.TrimPrefix(pluginID, "tool-")
}

// handleImageGenerate submits an image generation request and sends the result as a Discord file.
func (b *Bot) handleImageGenerate(s *discordgo.Session, channelID, username, provider, prompt string) {
	if prompt == "" {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Usage: @%s <prompt>", provider))
		return
	}

	b.emitEvent("image_request", fmt.Sprintf("from %s provider=%s prompt=%s", username, provider, truncate(prompt, 100)))
	s.ChannelMessageSend(channelID, fmt.Sprintf("Generating image with %s...\nPrompt: %s", provider, truncate(prompt, 200)))
	s.ChannelTyping(channelID)

	genResp, err := b.kernelClient.GenerateImage(provider, prompt)
	if err != nil {
		log.Printf("Image generate error: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("generate: %v", err))
		s.ChannelMessageSend(channelID, "Failed to generate image: "+err.Error())
		return
	}

	imageBytes, err := base64.StdEncoding.DecodeString(genResp.ImageData)
	if err != nil {
		log.Printf("Image base64 decode error: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("base64 decode: %v", err))
		s.ChannelMessageSend(channelID, "Failed to decode image data.")
		return
	}

	// Send as a Discord file attachment.
	_, err = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: truncate(prompt, 200),
		Files: []*discordgo.File{
			{
				Name:   "image.png",
				Reader: bytes.NewReader(imageBytes),
			},
		},
	})
	if err != nil {
		log.Printf("Error sending image: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("send: %v", err))
		s.ChannelMessageSend(channelID, "Image generated but failed to send: "+err.Error())
		return
	}

	b.emitEvent("image_complete", fmt.Sprintf("provider=%s for %s", provider, username))
}

// handleVideoGenerate submits a video generation request and polls for completion.
func (b *Bot) handleVideoGenerate(s *discordgo.Session, channelID, username, provider, prompt string) {
	if prompt == "" {
		s.ChannelMessageSend(channelID, fmt.Sprintf("Usage: @%s <prompt>", provider))
		return
	}

	b.emitEvent("video_request", fmt.Sprintf("from %s provider=%s prompt=%s", username, provider, truncate(prompt, 100)))
	s.ChannelMessageSend(channelID, fmt.Sprintf("Submitting video request to %s...\nPrompt: %s", provider, truncate(prompt, 200)))

	genResp, err := b.kernelClient.GenerateVideo(provider, prompt)
	if err != nil {
		log.Printf("Video generate error: %v", err)
		b.emitEvent("video_error", fmt.Sprintf("generate: %v", err))
		s.ChannelMessageSend(channelID, "Failed to start video generation: "+err.Error())
		return
	}

	taskID := genResp.TaskID
	b.emitEvent("video_submitted", fmt.Sprintf("task=%s", taskID))
	s.ChannelMessageSend(channelID, fmt.Sprintf("Video generation started (task: %s). I'll check progress...", taskID))

	// Poll for completion in a goroutine.
	go b.pollVideoStatus(s, channelID, username, provider, taskID)
}

// pollVideoStatus polls the video tool for task completion and sends result to channel.
func (b *Bot) pollVideoStatus(s *discordgo.Session, channelID, username, provider, taskID string) {
	const (
		initialInterval = 5 * time.Second
		laterInterval   = 10 * time.Second
		maxWait         = 5 * time.Minute
	)

	start := time.Now()
	interval := initialInterval
	notifiedProcessing := false

	for {
		if time.Since(start) > maxWait {
			s.ChannelMessageSend(channelID, fmt.Sprintf("Video generation timed out after %v (task: %s).", maxWait, taskID))
			return
		}

		time.Sleep(interval)

		status, err := b.kernelClient.CheckVideoStatus(provider, taskID)
		if err != nil {
			log.Printf("Video status check error: %v", err)
			continue
		}

		switch status.Status {
		case "completed":
			videoLink := status.VideoURI
			if videoLink == "" {
				videoLink = status.VideoURL
			}
			if videoLink == "" {
				s.ChannelMessageSend(channelID, fmt.Sprintf("Video completed but no URL returned (task: %s).", taskID))
			} else {
				elapsed := time.Since(start).Round(time.Second)
				s.ChannelMessageSend(channelID, fmt.Sprintf("Video ready! (%v)\n\n%s", elapsed, videoLink))
			}
			b.emitEvent("video_complete", fmt.Sprintf("task=%s for %s", taskID, username))
			return

		case "failed":
			errMsg := status.Error
			if errMsg == "" {
				errMsg = "unknown error"
			}
			s.ChannelMessageSend(channelID, fmt.Sprintf("Video generation failed: %s (task: %s)", errMsg, taskID))
			b.emitEvent("video_failed", fmt.Sprintf("task=%s error=%s", taskID, errMsg))
			return

		default:
			if !notifiedProcessing && time.Since(start) > 30*time.Second {
				s.ChannelMessageSend(channelID, "Still generating... video generation typically takes 30-120 seconds.")
				notifiedProcessing = true
			}
		}

		if time.Since(start) > 30*time.Second {
			interval = laterInterval
		}
	}
}

// appendAttachmentURLs extracts media URLs from Discord message attachments.
// Falls back to filename extension when ContentType is empty (e.g. in message snapshots).
func appendAttachmentURLs(urls []string, attachments []*discordgo.MessageAttachment) []string {
	for _, att := range attachments {
		if att.URL == "" {
			continue
		}
		if strings.HasPrefix(att.ContentType, "image/") ||
			strings.HasPrefix(att.ContentType, "video/") ||
			strings.HasPrefix(att.ContentType, "audio/") {
			urls = append(urls, att.URL)
			continue
		}
		// Fallback: check filename extension when ContentType is empty or unrecognised.
		if att.ContentType == "" {
			lower := strings.ToLower(att.Filename)
			for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp",
				".mp4", ".webm", ".mov", ".avi", ".mp3", ".ogg", ".wav"} {
				if strings.HasSuffix(lower, ext) {
					urls = append(urls, att.URL)
					break
				}
			}
		}
	}
	return urls
}

// appendEmbedImageURLs extracts image URLs from Discord message embeds.
func appendEmbedImageURLs(urls []string, embeds []*discordgo.MessageEmbed) []string {
	for _, embed := range embeds {
		if embed.Image != nil && embed.Image.URL != "" {
			urls = append(urls, embed.Image.URL)
		}
		if embed.Thumbnail != nil && embed.Thumbnail.URL != "" {
			urls = append(urls, embed.Thumbnail.URL)
		}
	}
	return urls
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
