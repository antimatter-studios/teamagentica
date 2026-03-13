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
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/msgbuffer"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/channels"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/relay"
)

const maxMessageLength = 2000

// Bot manages the Discord bot session.
type Bot struct {
	session      *discordgo.Session
	kernelClient *kernel.Client // used for image/video tool calls only
	relayClient  *relay.Client
	botUserID    string
	guildID      string
	aliases      *alias.AliasMap
	debug        atomic.Bool
	sdk          *pluginsdk.Client
	msgBuffer    *msgbuffer.Buffer
	callbacks    *channels.CallbackStore
	cmdOwners    map[string]commandOwner // slash command name → owning plugin
}

// New creates a new Bot instance. It does not open the connection yet.
func New(token string, kernelClient *kernel.Client, aliases *alias.AliasMap) (*Bot, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("creating discord session: %w", err)
	}

	session.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuilds

	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}

	b := &Bot{
		session:      session,
		kernelClient: kernelClient,
		aliases:      aliases,
	}

	b.msgBuffer = msgbuffer.New(1*time.Second, func(channelID string, text string, mediaURLs []string) {
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
	log.Printf("Message buffer duration: %dms", ms)
}

// SetSDK attaches the plugin SDK client for event reporting.
func (b *Bot) SetSDK(sdk *pluginsdk.Client) {
	b.sdk = sdk
}

// RegisteredCommand is a serializable view of a registered slash command route.
type RegisteredCommand struct {
	Key      string `json:"key"`      // e.g. "workspace/list"
	PluginID string `json:"plugin_id"` // owning plugin
	Endpoint string `json:"endpoint"` // HTTP endpoint on the plugin
}

// ListRegisteredCommands returns the currently registered slash command routes.
func (b *Bot) ListRegisteredCommands() []RegisteredCommand {
	var out []RegisteredCommand
	for key, owner := range b.cmdOwners {
		out = append(out, RegisteredCommand{Key: key, PluginID: owner.pluginID, Endpoint: owner.endpoint})
	}
	return out
}

// RefreshCommands re-discovers and re-registers slash commands from all plugins.
// Safe to call multiple times; replaces the owner map atomically.
func (b *Bot) RefreshCommands() {
	if b.botUserID == "" {
		return // not connected yet
	}
	owners := b.discoverAndRegisterCommands(b.botUserID)
	if len(owners) > 0 {
		b.cmdOwners = owners
	}
}

// SetRelayClient attaches the relay client for routing messages.
func (b *Bot) SetRelayClient(rc *relay.Client) {
	b.relayClient = rc
}

// SetGuildID sets the guild ID for channel management.
func (b *Bot) SetGuildID(id string) {
	b.guildID = id
}

// GuildID returns the configured guild ID.
func (b *Bot) GuildID() string {
	return b.guildID
}

// Session returns the underlying discordgo session.
func (b *Bot) Session() *discordgo.Session {
	return b.session
}

// SetCallbackStore attaches the callback store for interactive menu handling.
func (b *Bot) SetCallbackStore(cs *channels.CallbackStore) {
	b.callbacks = cs
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


// Start opens the Discord connection and begins listening for messages.
func (b *Bot) Start() error {
	b.session.AddHandler(b.onReady)
	b.session.AddHandler(b.onMessageCreate)
	b.session.AddHandler(b.onInteraction)

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
	// Discover slash commands in a background goroutine with retries — other plugins
	// may not have registered with the kernel yet when onReady fires.
	go b.discoverCommandsWithRetry(r.User.ID)
}

// discoverCommandsWithRetry attempts command discovery up to 5 times with increasing
// delays, stopping as soon as at least one command owner is registered.
func (b *Bot) discoverCommandsWithRetry(appID string) {
	delays := []time.Duration{0, 3 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second}
	for i, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		owners := b.discoverAndRegisterCommands(appID)
		if len(owners) > 0 {
			b.cmdOwners = owners
			return
		}
		log.Printf("Slash command discovery attempt %d/%d: no commands found", i+1, len(delays))
	}
	log.Printf("Slash command discovery gave up after %d attempts", len(delays))
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

	// Image/video aliases are handled locally (platform-specific output).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(text)
		if result.Target != nil {
			switch result.Target.Type {
			case alias.TargetImage:
				b.handleImageGenerate(s, channelID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
				return
			case alias.TargetVideo:
				b.handleVideoGenerate(s, channelID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
				return
			}
		}
	}

	// All text routing goes through the relay (alias, coordinator, workspace).
	if b.relayClient != nil {
		resp, err := b.relayClient.Chat(channelID, text, mediaURLs)
		if err != nil {
			log.Printf("Relay error: %v", err)
			b.emitEvent("error", fmt.Sprintf("relay: %v", err))
			s.ChannelMessageSend(channelID, "Sorry, I encountered an error processing your message.")
			return
		}

		if b.debug.Load() {
			b.emitEvent("agent_response", fmt.Sprintf("from @%s: %s", resp.Responder, truncate(resp.Response, 200)))
		} else {
			b.emitEvent("agent_response", fmt.Sprintf("from @%s (%d chars)", resp.Responder, len(resp.Response)))
		}

		response := formatAttributedResponse(resp.Responder, resp.Response)
		if err := b.sendResponse(s, channelID, response); err != nil {
			log.Printf("Error sending response: %v", err)
			b.emitEvent("error", fmt.Sprintf("send error: %v", err))
		} else {
			b.emitEvent("message_sent", fmt.Sprintf("response (%d chars)", len(resp.Response)))
		}
		return
	}

	// No relay configured — cannot route.
	log.Printf("No relay client configured — cannot route message")
	b.emitEvent("error", "no relay client configured")
	s.ChannelMessageSend(channelID, "Message routing is not available. The agent relay is not configured.")
}

// onInteraction handles Discord interactions: slash commands and message component clicks.
func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionApplicationCommand {
		b.handleSlashCommand(s, i, b.cmdOwners)
		return
	}

	if i.Type != discordgo.InteractionMessageComponent {
		return
	}

	data := i.MessageComponentData()
	if b.callbacks == nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "Interactive menus are not configured.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// For select menus, the selected value is in data.Values[0].
	// For buttons, the callback ID is in data.CustomID.
	customID := data.CustomID
	if len(data.Values) > 0 {
		customID = data.Values[0]
	}

	callbackMsg, ok := b.callbacks.Lookup(customID)
	if !ok {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "This menu has expired. Please request a new one.", Flags: discordgo.MessageFlagsEphemeral},
		})
		return
	}

	// Acknowledge immediately with a "thinking" indicator — the relay call may take a while.
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	channelID := i.ChannelID
	log.Printf("Menu interaction from %s: %s", i.Member.User.Username, callbackMsg)
	b.emitEvent("menu_interaction", fmt.Sprintf("callback: %s", truncate(callbackMsg, 200)))

	// Route through relay as a new message.
	if b.relayClient == nil {
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "Message routing is not available.",
		})
		return
	}

	resp, err := b.relayClient.Chat(channelID, callbackMsg, nil)
	if err != nil {
		log.Printf("Menu relay error: %v", err)
		b.emitEvent("error", fmt.Sprintf("menu relay: %v", err))
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "Sorry, I encountered an error processing your selection.",
		})
		return
	}

	response := formatAttributedResponse(resp.Responder, resp.Response)
	// Split long responses into chunks — followup messages also have the 2000 char limit.
	chunks := splitMessage(response, maxMessageLength)
	for j, chunk := range chunks {
		if j == 0 {
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: chunk})
		} else {
			s.ChannelMessageSend(channelID, chunk)
		}
	}

	b.emitEvent("menu_response", fmt.Sprintf("from @%s (%d chars)", resp.Responder, len(resp.Response)))
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
// Retries once on failure — the relay round-trip can take long enough for idle connections
// to be reset by network proxies, but a fresh connection succeeds immediately.
func (b *Bot) sendResponse(s *discordgo.Session, channelID, response string) error {
	if len(response) == 0 {
		response = "(empty response)"
	}

	chunks := splitMessage(response, maxMessageLength)
	for _, chunk := range chunks {
		if _, err := s.ChannelMessageSend(channelID, chunk); err != nil {
			// Retry once — idle connection may have been reset during the relay call.
			time.Sleep(500 * time.Millisecond)
			if _, err2 := s.ChannelMessageSend(channelID, chunk); err2 != nil {
				return fmt.Errorf("sending message chunk: %w (retry: %v)", err, err2)
			}
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
