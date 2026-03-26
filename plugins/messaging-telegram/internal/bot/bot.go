package bot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"net/http"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/msgbuffer"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/kernel"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-telegram/internal/relay"
)

const maxMessageLength = 4096

// Bot manages the Telegram bot session.
type Bot struct {
	api          *tgbotapi.BotAPI
	token        string
	kernelClient *kernel.Client
	pluginID     string
	version      string
	allowedUsers map[int64]bool
	pollTimeout  int
	debug        bool
	aliases     *alias.AliasMap
	relayClient *relay.Client
	msgBuffer   *msgbuffer.Buffer
	dataDir     string // persistent storage directory

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu            sync.Mutex
	polling       bool
	webhookActive bool
	pollStopCh    chan struct{}
	shutdownCh    chan struct{}
	shutdownOnce  sync.Once

	knownChats map[int64]bool // tracked chat IDs (groups + DMs)
	cacheMu    sync.RWMutex
	cache      *redis.Client // optional Redis cache for throttling

	// Task group tracking for progress updates.
	tasksMu    sync.Mutex
	taskChats  map[string]taskProgress // task_group_id → progress state
}

// taskProgress tracks a pending task group for progress updates.
type taskProgress struct {
	ChatID     int64
	MessageID  int                // Telegram message ID for editMessageText
	Cancel     context.CancelFunc // cancel typing loop
	Streaming  bool               // true once we receive the first streaming event
	LastEditAt time.Time          // debounce edits during streaming
}

// New creates a new Bot instance and validates the token via GetMe().
// The default agent must be set via the plugin config UI (config:update event).
func New(ctx context.Context, token string, kernelClient *kernel.Client, pluginID string, allowedUsers map[int64]bool, pollTimeout int, debug bool, aliases *alias.AliasMap, dataDir string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	log.Printf("Authorized on Telegram as @%s (ID: %d)", api.Self.UserName, api.Self.ID)

	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}

	childCtx, cancel := context.WithCancel(ctx)

	b := &Bot{
		api:          api,
		token:        token,
		kernelClient: kernelClient,
		pluginID:     pluginID,
		allowedUsers: allowedUsers,
		pollTimeout:  pollTimeout,
		debug:        debug,
		aliases:      aliases,
		dataDir:      dataDir,
		ctx:          childCtx,
		cancel:       cancel,
		pollStopCh:   make(chan struct{}),
		shutdownCh:   make(chan struct{}),
		knownChats:  make(map[int64]bool),
		taskChats:   make(map[string]taskProgress),
	}

	b.loadKnownChats()

	b.msgBuffer = msgbuffer.New(1*time.Second, func(channelID string, text string, mediaURLs []string) {
		chatID, _ := strconv.ParseInt(channelID, 10, 64)
		b.processBuffered(chatID, text, mediaURLs)
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

// SetRelayClient attaches the relay client for routing messages.
func (b *Bot) SetRelayClient(rc *relay.Client) {
	b.relayClient = rc
}

// SetVersion sets the plugin version for startup announcements.
func (b *Bot) SetVersion(v string) {
	b.version = v
}

// SetCache sets the Redis cache client for welcome message throttling.
func (b *Bot) SetCache(c *redis.Client) {
	b.cacheMu.Lock()
	b.cache = c
	b.cacheMu.Unlock()
}

func (b *Bot) getCache() *redis.Client {
	b.cacheMu.RLock()
	defer b.cacheMu.RUnlock()
	return b.cache
}

// emitEvent sends a debug event to the kernel console.
func (b *Bot) emitEvent(eventType, detail string) {
	ctx, cancel := context.WithTimeout(b.ctx, 3*time.Second)
	defer cancel()
	b.kernelClient.ReportEvent(ctx, b.pluginID, eventType, detail)
}

// StartPolling begins the long polling loop in a goroutine.
// It first calls deleteWebhook to ensure Telegram is not sending to a stale webhook.
func (b *Bot) StartPolling() {
	b.mu.Lock()
	if b.polling {
		b.mu.Unlock()
		return
	}
	b.pollStopCh = make(chan struct{})
	b.polling = true
	b.mu.Unlock()

	// Clear any existing webhook so getUpdates works.
	if err := b.DeleteWebhook(); err != nil {
		log.Printf("Warning: deleteWebhook on poll start: %v", err)
	}

	b.registerCommands()

	b.emitEvent("poll_start", fmt.Sprintf("started polling with timeout=%ds", b.pollTimeout))
	log.Println("Telegram bot is now running (long polling)")

	b.updateBotStatus("Online and ready.")

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		offset := 0
		for {
			select {
			case <-b.ctx.Done():
				log.Println("Polling loop stopped (context cancelled)")
				return
			case <-b.pollStopCh:
				log.Println("Polling loop stopped")
				return
			default:
			}

			u := tgbotapi.NewUpdate(offset)
			u.Timeout = b.pollTimeout

			if b.debug {
				b.emitEvent("poll", fmt.Sprintf("getUpdates offset=%d timeout=%ds", offset, b.pollTimeout))
			}

			updates, err := b.api.GetUpdates(u)
			if err != nil {
				// Check if we were stopped during the long poll.
				select {
				case <-b.ctx.Done():
					return
				case <-b.pollStopCh:
					return
				default:
				}

				errStr := err.Error()
				b.emitEvent("error", fmt.Sprintf("poll error: %v", err))
				log.Printf("GetUpdates error: %v", err)

				// Conflict-specific backoff: wait up to pollTimeout before retrying.
				if strings.Contains(errStr, "Conflict") {
					log.Printf("Conflict detected — backing off %ds for old poll to expire", b.pollTimeout)
					select {
					case <-time.After(time.Duration(b.pollTimeout) * time.Second):
					case <-b.ctx.Done():
						return
					case <-b.pollStopCh:
						return
					}
				} else {
					select {
					case <-time.After(3 * time.Second):
					case <-b.ctx.Done():
						return
					case <-b.pollStopCh:
						return
					}
				}
				continue
			}

			if len(updates) > 0 {
				b.emitEvent("poll_result", fmt.Sprintf("received %d update(s)", len(updates)))
			}

			for _, update := range updates {
				offset = update.UpdateID + 1
				msg := update.Message
				if msg == nil {
					msg = update.ChannelPost
				}
				if msg != nil {
					b.handleMessage(msg)
				} else if b.debug {
					// Log what kind of update this is so we can diagnose dropped messages.
					kind := "unknown"
					switch {
					case update.EditedMessage != nil:
						kind = "edited_message"
					case update.EditedChannelPost != nil:
						kind = "edited_channel_post"
					case update.CallbackQuery != nil:
						kind = "callback_query"
					case update.InlineQuery != nil:
						kind = "inline_query"
					case update.ChosenInlineResult != nil:
						kind = "chosen_inline_result"
					case update.MyChatMember != nil:
						kind = "my_chat_member"
					case update.ChatMember != nil:
						kind = "chat_member"
					case update.ChatJoinRequest != nil:
						kind = "chat_join_request"
					}
					b.emitEvent("update_skipped", fmt.Sprintf("update_id=%d type=%s (not a message)", update.UpdateID, kind))
				}
			}
		}
	}()
}

// StopPolling stops the long polling loop without shutting down the bot.
func (b *Bot) StopPolling() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.polling {
		return
	}

	b.emitEvent("poll_stop", "stopping polling (switching to webhook)")
	log.Println("Stopping polling loop...")
	b.api.StopReceivingUpdates()
	close(b.pollStopCh)
	b.polling = false
}

// Start begins the long polling loop (convenience alias for StartPolling).
func (b *Bot) Start() {
	b.StartPolling()
}

// Stop gracefully shuts down the bot entirely.
func (b *Bot) Stop() {
	b.shutdownOnce.Do(func() {
		// 1. Emit shutdown event (before cancel, so ctx is still valid).
		b.emitEvent("shutdown", "shutting down")
		log.Println("Shutting down Telegram bot...")

		// 2. Flush pending message buffers before cancelling context.
		b.msgBuffer.Stop()

		// 3. Cancel context — signals all goroutines.
		b.cancel()

		// 3. Stop polling if active.
		b.StopPolling()

		// 4. Remove webhook if active.
		if b.IsWebhookActive() {
			if err := b.DeleteWebhook(); err != nil {
				log.Printf("Warning: deleteWebhook on shutdown: %v", err)
			}
		}

		// 5. Wait for goroutines to drain with timeout.
		done := make(chan struct{})
		go func() {
			b.wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			log.Println("All bot goroutines drained")
		case <-time.After(15 * time.Second):
			log.Println("Warning: timed out waiting for bot goroutines to drain")
		}

		// 6. Signal shutdown complete.
		close(b.shutdownCh)
	})
}

// IsWebhookActive returns whether webhook mode is currently active.
func (b *Bot) IsWebhookActive() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.webhookActive
}

// SetWebhook calls the Telegram Bot API to configure a webhook URL.
// The webhookURL should be the full public URL that Telegram will POST to.
func (b *Bot) SetWebhook(webhookURL string) error {
	fullURL := strings.TrimRight(webhookURL, "/")

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/setWebhook", b.token)
	redactedAPI := "https://api.telegram.org/bot<TOKEN_SECRET>/setWebhook"

	payload, err := json.Marshal(map[string]string{"url": fullURL})
	if err != nil {
		return fmt.Errorf("marshalling setWebhook payload: %w", err)
	}

	b.emitEvent("webhook", fmt.Sprintf("POST %s payload=%s", redactedAPI, string(payload)))

	resp, err := http.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("calling setWebhook: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading setWebhook response: %w", err)
	}

	b.emitEvent("webhook", fmt.Sprintf("response status=%d body=%s", resp.StatusCode, string(body)))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("setWebhook returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the Telegram API response to check for success.
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parsing setWebhook response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("setWebhook failed: %s", result.Description)
	}

	b.mu.Lock()
	b.webhookActive = true
	b.mu.Unlock()

	b.registerCommands()

	b.emitEvent("webhook_set", fmt.Sprintf("webhook active: %s", fullURL))
	log.Printf("Webhook set to %s", fullURL)

	return nil
}

// DeleteWebhook removes the Telegram webhook so the bot can fall back to polling.
func (b *Bot) DeleteWebhook() error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook", b.token)

	resp, err := http.Post(apiURL, "application/json", strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("calling deleteWebhook: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading deleteWebhook response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deleteWebhook returned status %d: %s", resp.StatusCode, string(body))
	}

	b.mu.Lock()
	b.webhookActive = false
	b.mu.Unlock()

	log.Println("Webhook deleted")

	return nil
}

// HandleWebhookUpdate processes a single incoming Update from a Telegram webhook POST.
// The body is a single Update JSON object (same structure as getUpdates elements).
func (b *Bot) HandleWebhookUpdate(body []byte) error {
	var update tgbotapi.Update
	if err := json.Unmarshal(body, &update); err != nil {
		log.Printf("[webhook] failed to parse update: %v", err)
		return fmt.Errorf("parsing webhook update: %w", err)
	}

	// Channel posts arrive as ChannelPost, not Message.
	msg := update.Message
	if msg == nil {
		msg = update.ChannelPost
	}

	if b.debug {
		from := "unknown"
		text := ""
		if msg != nil {
			if msg.From != nil {
				from = fmt.Sprintf("@%s (id=%d)", msg.From.UserName, msg.From.ID)
			}
			text = msg.Text
			if text == "" {
				text = msg.Caption
			}
		}
		log.Printf("[webhook] update_id=%d from=%s text=%q has_message=%v",
			update.UpdateID, from, text, msg != nil)
		b.emitEvent("webhook_update", fmt.Sprintf("update_id=%d from=%s text=%s",
			update.UpdateID, from, truncate(text, 100)))
	}

	if msg != nil {
		b.handleMessage(msg)
	} else {
		log.Printf("[webhook] update %d has no message (may be callback/edit/etc)", update.UpdateID)
	}

	return nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// registerCommands registers bot commands with Telegram so they appear in the / menu.
func (b *Bot) registerCommands() {
	commands := []tgbotapi.BotCommand{
		{Command: "clear", Description: "Clear conversation history"},
		{Command: "aliases", Description: "List configured @mention aliases"},
		{Command: "help", Description: "Show available commands"},
	}
	cfg := tgbotapi.NewSetMyCommands(commands...)
	if _, err := b.api.Request(cfg); err != nil {
		log.Printf("setMyCommands failed: %v", err)
		b.emitEvent("error", fmt.Sprintf("setMyCommands: %v", err))
	} else {
		b.emitEvent("commands", fmt.Sprintf("registered %d commands with Telegram", len(commands)))
	}
}

// handleMessage processes an incoming Telegram message.
// Commands are handled immediately; all other messages are buffered per-chat.
func (b *Bot) handleMessage(msg *tgbotapi.Message) {
	// Track chats (groups + DMs) for startup announcements.
	if msg.Chat != nil {
		b.trackChat(msg.Chat.ID)
	}

	// Ignore messages from bots.
	if msg.From != nil && msg.From.IsBot {
		if b.debug {
			log.Printf("[message] ignoring bot message from %s", msg.From.UserName)
		}
		return
	}

	// Check allowed users if configured.
	if b.allowedUsers != nil && msg.From != nil {
		if !b.allowedUsers[msg.From.ID] {
			log.Printf("[message] BLOCKED unauthorized user %d (@%s) — allowed: %v", msg.From.ID, msg.From.UserName, b.allowedUsers)
			b.emitEvent("blocked", fmt.Sprintf("user=%d @%s not in allowed list", msg.From.ID, msg.From.UserName))
			return
		}
		if b.debug {
			log.Printf("[message] user %d (@%s) authorized", msg.From.ID, msg.From.UserName)
		}
	}

	// Extract media URLs from photos, video, voice, audio, and documents.
	// Also check ReplyToMessage so users can reply to an image with a text prompt.
	var imageURLs []string
	imageURLs = b.extractMediaURLs(imageURLs, msg)
	if msg.ReplyToMessage != nil {
		imageURLs = b.extractMediaURLs(imageURLs, msg.ReplyToMessage)
	}

	if b.debug && msg.ForwardDate != 0 {
		hasPhoto := msg.Photo != nil && len(msg.Photo) > 0
		hasVideo := msg.Video != nil
		hasDoc := msg.Document != nil
		log.Printf("[message] forwarded message: photo=%v video=%v doc=%v caption=%q text=%q media_urls=%d",
			hasPhoto, hasVideo, hasDoc, msg.Caption, msg.Text, len(imageURLs))
		b.emitEvent("forward_debug", fmt.Sprintf("photo=%v video=%v doc=%v caption=%q media=%d",
			hasPhoto, hasVideo, hasDoc, truncate(msg.Caption, 50), len(imageURLs)))
	}

	// Extract message text.
	text := msg.Text
	if text == "" {
		text = msg.Caption // Support photo/document captions.
	}
	if text == "" && msg.ReplyToMessage != nil {
		// If replying to a message, use the replied-to caption as context.
		if msg.ReplyToMessage.Caption != "" {
			text = msg.ReplyToMessage.Caption
		}
	}
	if text == "" && len(imageURLs) > 0 {
		text = "What's in this media?"
	}
	if text == "" && msg.Venue != nil {
		text = fmt.Sprintf("Location: %s, %s (%f, %f)", msg.Venue.Title, msg.Venue.Address, msg.Venue.Location.Latitude, msg.Venue.Location.Longitude)
	} else if text == "" && msg.Location != nil {
		text = fmt.Sprintf("Location: %f, %f", msg.Location.Latitude, msg.Location.Longitude)
	}
	if text == "" {
		if b.debug {
			log.Printf("[message] empty text from chat %d, skipping", msg.Chat.ID)
		}
		return
	}

	username := ""
	if msg.From != nil {
		username = msg.From.UserName
	}

	if b.debug {
		b.emitEvent("message_received", fmt.Sprintf("from @%s: %s", username, truncate(text, 100)))
	} else {
		b.emitEvent("message_received", fmt.Sprintf("from @%s (%d chars)", username, len(text)))
	}

	// Commands bypass the buffer — handle immediately.
	if text == "/help" || text == "/start" || text == "/clear" || text == "/reset" || text == "/aliases" {
		b.handleCommand(msg.Chat.ID, text)
		return
	}

	// Start typing on first buffered message so user sees immediate feedback.
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		typing := tgbotapi.NewChatAction(msg.Chat.ID, tgbotapi.ChatTyping)
		b.api.Send(typing)
	}()

	var userID int64
	if msg.From != nil {
		userID = msg.From.ID
	}
	log.Printf("[message] buffering from @%s (user=%d chat=%d): %s", username, userID, msg.Chat.ID, text)

	// Buffer the message — will be flushed after debounce window.
	b.msgBuffer.Add(fmt.Sprintf("%d", msg.Chat.ID), text, imageURLs)
}

// handleCommand processes slash commands immediately without buffering.
func (b *Bot) handleCommand(chatID int64, text string) {
	switch text {
	case "/help", "/start":
		helpMsg := "Available commands:\n\n" +
			"/clear — Clear conversation history\n" +
			"/aliases — List configured @mention aliases\n" +
			"/help — Show this message\n\n"

		if !b.aliases.IsEmpty() {
			helpMsg += "Use @nickname to route messages directly to a specific agent or tool.\n"
			helpMsg += "Type /aliases to see the full list."
		} else {
			helpMsg += "Or just send any message to chat with the AI."
		}
		b.sendResponse(chatID, helpMsg)

	case "/clear", "/reset":
		b.kernelClient.ClearHistory(chatID)
		b.sendResponse(chatID, "Conversation cleared.")

	case "/aliases":
		b.handleAliasesCommand(chatID)
	}
}

// processBuffered handles the merged text and media after the debounce timer fires.
// Called from the MessageBuffer flush callback.
func (b *Bot) processBuffered(chatID int64, text string, imageURLs []string) {
	// Per-message context for cancellation.
	msgCtx, msgCancel := context.WithCancel(b.ctx)
	defer msgCancel()

	// Send typing indicator and refresh it while waiting.
	b.wg.Add(1)
	go b.sendTypingLoop(msgCtx, chatID)

	// Image/video aliases are handled locally (platform-specific output).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(text)
		if result.Target != nil {
			switch result.Target.Type {
			case alias.TargetImage:
				msgCancel()
				b.handleImageGenerate(chatID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
				return
			case alias.TargetVideo:
				msgCancel()
				b.handleVideoGenerate(chatID, "", stripToolPrefix(result.Target.PluginID), result.Remainder)
				return
			}
		}
	}

	// All text routing goes through the relay (alias, coordinator, workspace).
	if b.relayClient != nil {
		channelID := fmt.Sprintf("%d", chatID)
		accepted, err := b.relayClient.Chat(channelID, text, imageURLs)
		if err != nil {
			msgCancel()
			log.Printf("Relay error: %v", err)
			b.emitEvent("error", fmt.Sprintf("relay: %v", err))
			b.sendResponse(chatID, "Sorry, I encountered an error processing your message.")
			return
		}

		// Send initial "Thinking..." message and track it for progress updates.
		thinkMsg := tgbotapi.NewMessage(chatID, "Thinking...")
		sent, err := b.api.Send(thinkMsg)
		if err != nil {
			msgCancel()
			log.Printf("Error sending thinking message: %v", err)
			return
		}

		// Register task for progress updates — the typing loop continues
		// and the event handler will update the message.
		b.tasksMu.Lock()
		b.taskChats[accepted.TaskGroupID] = taskProgress{
			ChatID:    chatID,
			MessageID: sent.MessageID,
			Cancel:    msgCancel,
		}
		b.tasksMu.Unlock()

		b.emitEvent("task_accepted", fmt.Sprintf("task_group=%s chat=%d", accepted.TaskGroupID, chatID))
		// Don't cancel msgCtx here — the typing loop continues until
		// the progress event handler calls cancel on completion.
		return
	}

	// No relay configured — cannot route.
	msgCancel()
	log.Printf("No relay client configured — cannot route message")
	b.emitEvent("error", "no relay client configured")
	b.sendResponse(chatID, "Message routing is not available. The agent relay is not configured.")
}

// handleImageGenerate submits an image generation request to a specific provider.
// Image generation is synchronous — the plugin returns base64 image data directly.
func (b *Bot) handleImageGenerate(chatID int64, username, provider, prompt string) {
	if prompt == "" {
		b.sendResponse(chatID, fmt.Sprintf("Usage: /%s <prompt>\n\nExample: /%s a beautiful sunset over the ocean", provider, provider))
		return
	}

	b.emitEvent("image_request", fmt.Sprintf("from @%s provider=%s prompt=%s", username, provider, truncate(prompt, 100)))
	b.sendResponse(chatID, fmt.Sprintf("Generating image with %s...\nPrompt: %s", provider, truncate(prompt, 200)))

	// Per-request context.
	reqCtx, reqCancel := context.WithCancel(b.ctx)
	defer reqCancel()

	// Send typing indicator while generating.
	b.wg.Add(1)
	go b.sendTypingLoop(reqCtx, chatID)

	genResp, err := b.kernelClient.GenerateImage(reqCtx, provider, prompt)
	reqCancel()

	if err != nil {
		log.Printf("Image generate error: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("generate: %v", err))
		b.sendResponse(chatID, "Failed to generate image: "+err.Error())
		return
	}

	// Decode base64 image data.
	imageBytes, err := base64.StdEncoding.DecodeString(genResp.ImageData)
	if err != nil {
		log.Printf("Image base64 decode error: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("base64 decode: %v", err))
		b.sendResponse(chatID, "Failed to decode image data.")
		return
	}

	// Send as a Telegram photo.
	photoFile := tgbotapi.FileBytes{
		Name:  "image.png",
		Bytes: imageBytes,
	}
	photo := tgbotapi.NewPhoto(chatID, photoFile)
	photo.Caption = truncate(prompt, 200)

	if _, err := b.api.Send(photo); err != nil {
		log.Printf("Error sending photo: %v", err)
		b.emitEvent("image_error", fmt.Sprintf("send photo: %v", err))
		b.sendResponse(chatID, "Image generated but failed to send: "+err.Error())
		return
	}

	b.emitEvent("image_complete", fmt.Sprintf("provider=%s for @%s", provider, username))
}

// handleVideoGenerate submits a video generation request to a specific provider.
func (b *Bot) handleVideoGenerate(chatID int64, username, provider, prompt string) {
	if prompt == "" {
		b.sendResponse(chatID, fmt.Sprintf("Usage: /%s <prompt>\n\nExample: /%s a sunset over mountains", provider, provider))
		return
	}

	b.emitEvent("video_request", fmt.Sprintf("from @%s provider=%s prompt=%s", username, provider, truncate(prompt, 100)))
	b.sendResponse(chatID, fmt.Sprintf("Submitting video request to %s...\nPrompt: %s", provider, truncate(prompt, 200)))

	// Submit generation request.
	genResp, err := b.kernelClient.GenerateVideo(b.ctx, provider, prompt)
	if err != nil {
		log.Printf("Video generate error: %v", err)
		b.emitEvent("video_error", fmt.Sprintf("generate: %v", err))
		b.sendResponse(chatID, "Failed to start video generation: "+err.Error())
		return
	}

	taskID := genResp.TaskID
	b.emitEvent("video_submitted", fmt.Sprintf("task=%s", taskID))
	b.sendResponse(chatID, fmt.Sprintf("Video generation started (task: %s). I'll check progress...", taskID))

	// Poll for completion in a goroutine.
	b.wg.Add(1)
	go b.pollVideoStatus(b.ctx, chatID, username, provider, taskID)
}

// pollVideoStatus polls the video tool for task completion and sends result to chat.
func (b *Bot) pollVideoStatus(ctx context.Context, chatID int64, username, provider, taskID string) {
	defer b.wg.Done()

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
			b.sendResponse(chatID, fmt.Sprintf("Video generation timed out after %v (task: %s). The video may still be processing — try /videostatus %s later.", maxWait, taskID, taskID))
			return
		}

		select {
		case <-ctx.Done():
			log.Printf("pollVideoStatus cancelled for task %s", taskID)
			return
		case <-time.After(interval):
		}

		status, err := b.kernelClient.CheckVideoStatus(ctx, provider, taskID)
		if err != nil {
			log.Printf("Video status check error: %v", err)
			// Keep polling on transient errors.
			continue
		}

		switch status.Status {
		case "completed":
			videoLink := status.VideoURI
			if videoLink == "" {
				videoLink = status.VideoURL
			}
			if videoLink == "" {
				b.sendResponse(chatID, fmt.Sprintf("Video completed but no URL returned (task: %s).", taskID))
			} else {
				elapsed := time.Since(start).Round(time.Second)
				// Try sending as a native Telegram video.
				video := tgbotapi.NewVideo(chatID, tgbotapi.FileURL(videoLink))
				video.Caption = truncate(fmt.Sprintf("Video ready! (%v)", elapsed), 200)
				if _, err := b.api.Send(video); err != nil {
					log.Printf("[video] native send failed, falling back to link: %v", err)
					b.sendResponse(chatID, fmt.Sprintf("Video ready! (%v)\n\n%s", elapsed, videoLink))
				}
			}
			b.emitEvent("video_complete", fmt.Sprintf("task=%s for @%s", taskID, username))
			return

		case "failed":
			errMsg := status.Error
			if errMsg == "" {
				errMsg = "unknown error"
			}
			b.sendResponse(chatID, fmt.Sprintf("Video generation failed: %s (task: %s)", errMsg, taskID))
			b.emitEvent("video_failed", fmt.Sprintf("task=%s error=%s", taskID, errMsg))
			return

		default:
			// Still processing — send one progress update.
			if !notifiedProcessing && time.Since(start) > 30*time.Second {
				b.sendResponse(chatID, "Still generating... video generation typically takes 30-120 seconds.")
				notifiedProcessing = true
			}
		}

		// Slow down after first 30 seconds.
		if time.Since(start) > 30*time.Second {
			interval = laterInterval
		}
	}
}

// HandleRelayProgress processes a relay:progress event for task group updates.
func (b *Bot) HandleRelayProgress(detail string) {
	var ev struct {
		TaskGroupID string `json:"task_group_id"`
		ChannelID   string `json:"channel_id"`
		Status      string `json:"status"`
		Message     string `json:"message"`
		Response    string `json:"response,omitempty"`
		Responder   string `json:"responder,omitempty"`
	}
	if err := json.Unmarshal([]byte(detail), &ev); err != nil {
		log.Printf("[progress] failed to parse relay:progress: %v", err)
		return
	}

	b.tasksMu.Lock()
	tp, ok := b.taskChats[ev.TaskGroupID]
	b.tasksMu.Unlock()

	if !ok {
		// Not a task we're tracking — might be for a different messaging client.
		return
	}

	switch ev.Status {
	case "completed":
		// Cancel typing loop.
		tp.Cancel()

		// Delete the progress message and send the final response.
		del := tgbotapi.NewDeleteMessage(tp.ChatID, tp.MessageID)
		b.api.Send(del)

		response := ev.Response
		if ev.Responder != "" {
			response = formatAttributedResponse(ev.Responder, response)
		}
		if err := b.sendResponse(tp.ChatID, response); err != nil {
			log.Printf("[progress] error sending final response: %v", err)
		}

		// Clean up.
		b.tasksMu.Lock()
		delete(b.taskChats, ev.TaskGroupID)
		b.tasksMu.Unlock()

		b.emitEvent("task_complete", fmt.Sprintf("task_group=%s chat=%d", ev.TaskGroupID, tp.ChatID))

	case "streaming":
		// Streaming token update — edit the progress message with accumulated text.
		// Debounce to avoid Telegram rate limits (~1 edit/sec).
		if time.Since(tp.LastEditAt) < 800*time.Millisecond {
			return
		}

		msg := ev.Message
		if msg == "" {
			return
		}

		// Truncate to Telegram's message limit (4096 chars).
		if len(msg) > 4000 {
			msg = msg[len(msg)-4000:]
		}

		if !tp.Streaming {
			// First streaming event — cancel typing indicator.
			tp.Cancel()
			tp.Streaming = true
		}

		edit := tgbotapi.NewEditMessageText(tp.ChatID, tp.MessageID, msg)
		if _, err := b.api.Send(edit); err != nil {
			log.Printf("[progress] streaming edit error: %v", err)
		}

		tp.LastEditAt = time.Now()

		// Write back updated state.
		b.tasksMu.Lock()
		b.taskChats[ev.TaskGroupID] = tp
		b.tasksMu.Unlock()

	case "failed":
		tp.Cancel()

		// Update the progress message with the error.
		edit := tgbotapi.NewEditMessageText(tp.ChatID, tp.MessageID, "Error: "+ev.Message)
		b.api.Send(edit)

		b.tasksMu.Lock()
		delete(b.taskChats, ev.TaskGroupID)
		b.tasksMu.Unlock()

		b.emitEvent("task_failed", fmt.Sprintf("task_group=%s error=%s", ev.TaskGroupID, ev.Message))

	default:
		// Progress update — edit the thinking message.
		msg := ev.Message
		if msg == "" {
			msg = ev.Status + "..."
		}
		edit := tgbotapi.NewEditMessageText(tp.ChatID, tp.MessageID, msg)
		if _, err := b.api.Send(edit); err != nil {
			// Telegram may reject edits if content hasn't changed — ignore.
			log.Printf("[progress] edit message error (may be duplicate): %v", err)
		}
	}
}

// sendTypingLoop sends ChatTyping action every 4 seconds until ctx is cancelled.
// Caller must call b.wg.Add(1) before spawning this goroutine.
func (b *Bot) sendTypingLoop(ctx context.Context, chatID int64) {
	defer b.wg.Done()

	typing := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	b.api.Send(typing)

	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.api.Send(tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping))
		}
	}
}

// formatAttributedResponse prefixes a response with the responder's name
// so users can see who authored the message.
func formatAttributedResponse(name, response string) string {
	if name == "" {
		return response
	}
	return fmt.Sprintf("[@%s]\n%s", name, response)
}

// handleAliasesCommand lists all configured @mention aliases.
func (b *Bot) handleAliasesCommand(chatID int64) {
	if b.aliases.IsEmpty() {
		b.sendResponse(chatID, "No aliases configured.\n\nSet the ALIASES environment variable to enable @mention routing.\nExample: ALIASES=codex=agent-openai,claude=agent-claude")
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
	b.sendResponse(chatID, sb.String())
}

// extractMediaURLs extracts photo, video, voice, audio, and document media
// URLs from a Telegram message and appends them to the provided slice.
func (b *Bot) extractMediaURLs(urls []string, msg *tgbotapi.Message) []string {
	if msg.Photo != nil && len(msg.Photo) > 0 {
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		if fileURL, err := b.api.GetFileDirectURL(bestPhoto.FileID); err != nil {
			log.Printf("[message] failed to get photo URL: %v", err)
			b.emitEvent("error", fmt.Sprintf("photo URL: %v", err))
		} else {
			urls = append(urls, fileURL)
			if b.debug {
				log.Printf("[message] extracted photo URL: %s", fileURL)
			}
		}
	}
	if msg.Video != nil {
		if fileURL, err := b.api.GetFileDirectURL(msg.Video.FileID); err != nil {
			log.Printf("[message] failed to get video URL: %v", err)
		} else {
			urls = append(urls, fileURL)
		}
	}
	if msg.Voice != nil {
		if fileURL, err := b.api.GetFileDirectURL(msg.Voice.FileID); err != nil {
			log.Printf("[message] failed to get voice URL: %v", err)
		} else {
			urls = append(urls, fileURL)
		}
	}
	if msg.Audio != nil {
		if fileURL, err := b.api.GetFileDirectURL(msg.Audio.FileID); err != nil {
			log.Printf("[message] failed to get audio URL: %v", err)
		} else {
			urls = append(urls, fileURL)
		}
	}
	if msg.Document != nil {
		mime := msg.Document.MimeType
		if strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "audio/") {
			if fileURL, err := b.api.GetFileDirectURL(msg.Document.FileID); err != nil {
				log.Printf("[message] failed to get document URL: %v", err)
			} else {
				urls = append(urls, fileURL)
			}
		}
	}
	// Stickers contain an image file.
	if msg.Sticker != nil {
		if fileURL, err := b.api.GetFileDirectURL(msg.Sticker.FileID); err != nil {
			log.Printf("[message] failed to get sticker URL: %v", err)
		} else {
			urls = append(urls, fileURL)
		}
	}
	return urls
}

// stripToolPrefix removes the "tool-" prefix from a plugin ID for use as a
// provider name in image/video generation commands.
func stripToolPrefix(pluginID string) string {
	return strings.TrimPrefix(pluginID, "tool-")
}

// sendResponse sends a message, splitting into chunks if over 4096 chars.
func (b *Bot) sendResponse(chatID int64, response string) error {
	if len(response) == 0 {
		response = "(empty response)"
	}

	chunks := splitMessage(response, maxMessageLength)
	for _, chunk := range chunks {
		msg := tgbotapi.NewMessage(chatID, chunk)
		if _, err := b.api.Send(msg); err != nil {
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

		// Try to find a good break point.
		chunk := text[:maxLen]
		breakIdx := -1

		// Prefer breaking at newline.
		if idx := strings.LastIndex(chunk, "\n"); idx > 0 {
			breakIdx = idx
		} else if idx := strings.LastIndex(chunk, " "); idx > 0 {
			// Fall back to space.
			breakIdx = idx
		}

		if breakIdx > 0 {
			chunks = append(chunks, text[:breakIdx])
			text = text[breakIdx+1:]
		} else {
			// No good break point, hard cut.
			chunks = append(chunks, chunk)
			text = text[maxLen:]
		}
	}

	return chunks
}

// --- Known chats tracking & startup announcements ---

const knownChatsFile = "known_chats.json"

// knownChatsPath returns the path to the known chats JSON file.
func (b *Bot) knownChatsPath() string {
	return filepath.Join(b.dataDir, knownChatsFile)
}

// loadKnownChats reads tracked chat IDs from disk.
func (b *Bot) loadKnownChats() {
	data, err := os.ReadFile(b.knownChatsPath())
	if err != nil {
		return // file doesn't exist yet
	}
	var ids []int64
	if err := json.Unmarshal(data, &ids); err != nil {
		log.Printf("[chats] failed to parse %s: %v", knownChatsFile, err)
		return
	}
	for _, id := range ids {
		b.knownChats[id] = true
	}
	log.Printf("Loaded %d known chat(s)", len(b.knownChats))
}

// saveKnownChats writes tracked chat IDs to disk.
func (b *Bot) saveKnownChats() {
	var ids []int64
	for id := range b.knownChats {
		ids = append(ids, id)
	}
	data, _ := json.Marshal(ids)
	if err := os.WriteFile(b.knownChatsPath(), data, 0644); err != nil {
		log.Printf("[chats] failed to save %s: %v", knownChatsFile, err)
	}
}

// trackChat records a chat ID (group or DM). Persists to disk if new.
func (b *Bot) trackChat(chatID int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.knownChats[chatID] {
		return
	}
	b.knownChats[chatID] = true
	log.Printf("[chats] tracking new chat %d (total: %d)", chatID, len(b.knownChats))
	b.saveKnownChats()
}

// updateBotStatus sets the bot's profile description and short description
// instead of sending messages to chats. Includes a timestamp as a heartbeat
// so users can see when the bot last signalled it was alive.
func (b *Bot) updateBotStatus(status string) {
	ts := time.Now().Format("Jan 2 15:04 MST")
	text := fmt.Sprintf("%s · since %s", status, ts)
	if b.version != "" {
		text = fmt.Sprintf("%s (v%s) · since %s", status, b.version, ts)
	}

	// Build a longer description with aliases and commands.
	var descLines []string
	descLines = append(descLines, text)
	if !b.aliases.IsEmpty() {
		var aliasNames []string
		for _, entry := range b.aliases.List() {
			aliasNames = append(aliasNames, "@"+entry.Alias)
		}
		descLines = append(descLines, fmt.Sprintf("Aliases: %s", strings.Join(aliasNames, ", ")))
	}
	descLines = append(descLines, "Commands: /help, /clear, /aliases")
	desc := strings.Join(descLines, "\n")

	// setMyDescription — shown when user opens bot for the first time.
	descParams := tgbotapi.Params{"description": desc}
	if _, err := b.api.MakeRequest("setMyDescription", descParams); err != nil {
		log.Printf("setMyDescription failed: %v", err)
	}

	// setMyShortDescription — shown in bot profile card.
	shortParams := tgbotapi.Params{"short_description": text}
	if _, err := b.api.MakeRequest("setMyShortDescription", shortParams); err != nil {
		log.Printf("setMyShortDescription failed: %v", err)
	}

	log.Printf("Bot status updated: %s", text)
}

// sendStartupAnnouncement sends a status message to all known chats.
// Throttled to at most once per hour via Redis cache if available.
func (b *Bot) sendStartupAnnouncement(status string) {
	const throttleKey = "telegram:welcome:last"
	const throttleTTL = 1 * time.Hour

	// Throttle: skip if we sent a welcome message less than 1 hour ago.
	if c := b.getCache(); c != nil {
		if _, err := c.Get(context.Background(), throttleKey).Result(); err == nil {
			log.Println("[announce] throttled (sent within last hour)")
			return
		}
	}

	b.mu.Lock()
	chats := make([]int64, 0, len(b.knownChats))
	for id := range b.knownChats {
		chats = append(chats, id)
	}
	b.mu.Unlock()

	if len(chats) == 0 {
		log.Println("[announce] no known chats to announce to")
		return
	}

	msg := b.buildStartupMessage(status)
	sent := 0
	for _, chatID := range chats {
		tgMsg := tgbotapi.NewMessage(chatID, msg)
		tgMsg.ParseMode = tgbotapi.ModeMarkdown
		if _, err := b.api.Send(tgMsg); err != nil {
			log.Printf("[announce] chat %d: %v", chatID, err)
			// If the bot was removed from the chat, untrack it.
			if strings.Contains(err.Error(), "Forbidden") || strings.Contains(err.Error(), "chat not found") {
				b.mu.Lock()
				delete(b.knownChats, chatID)
				b.saveKnownChats()
				b.mu.Unlock()
				log.Printf("[announce] removed stale chat %d", chatID)
			}
		} else {
			sent++
		}
	}
	log.Printf("[announce] sent to %d/%d chat(s)", sent, len(chats))

	// Mark as sent in cache so subsequent restarts within 1h are throttled.
	if c := b.getCache(); c != nil && sent > 0 {
		c.Set(context.Background(), throttleKey, time.Now().Unix(), throttleTTL)
	}
}

// buildStartupMessage constructs the announcement text.
func (b *Bot) buildStartupMessage(status string) string {
	var lines []string
	if b.version != "" {
		lines = append(lines, fmt.Sprintf("*%s* (v%s)", status, b.version))
	} else {
		lines = append(lines, fmt.Sprintf("*%s*", status))
	}

	if !b.aliases.IsEmpty() {
		var aliasNames []string
		for _, entry := range b.aliases.List() {
			aliasNames = append(aliasNames, "@"+entry.Alias)
		}
		lines = append(lines, fmt.Sprintf("Aliases: %s", strings.Join(aliasNames, ", ")))
	}

	lines = append(lines, "Commands: /help, /clear, /aliases")
	lines = append(lines, "Send me a message to get started.")
	return strings.Join(lines, "\n")
}
