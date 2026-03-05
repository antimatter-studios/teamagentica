package bot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/telegram/internal/kernel"
)

const maxMessageLength = 4096

// Bot manages the Telegram bot session.
type Bot struct {
	api          *tgbotapi.BotAPI
	token        string
	kernelClient *kernel.Client
	pluginID     string
	allowedUsers map[int64]bool
	pollTimeout  int
	debug        bool
	aliases      *alias.AliasMap
	defaultAgent atomic.Pointer[string] // plugin ID for coordinator brain

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu            sync.Mutex
	polling       bool
	webhookActive bool
	pollStopCh    chan struct{}
	shutdownCh    chan struct{}
	shutdownOnce  sync.Once
}

// New creates a new Bot instance and validates the token via GetMe().
func New(ctx context.Context, token string, kernelClient *kernel.Client, pluginID string, allowedUsers map[int64]bool, pollTimeout int, debug bool, aliases *alias.AliasMap, defaultAgent string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("creating telegram bot: %w", err)
	}

	log.Printf("Authorized on Telegram as @%s (ID: %d)", api.Self.UserName, api.Self.ID)

	if !aliases.IsEmpty() {
		log.Printf("Configured %d aliases", len(aliases.List()))
	}
	if defaultAgent != "" {
		log.Printf("Coordinator agent: %s", defaultAgent)
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
		ctx:          childCtx,
		cancel:       cancel,
		pollStopCh:   make(chan struct{}),
		shutdownCh:   make(chan struct{}),
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

		// 2. Cancel context — signals all goroutines.
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
func (b *Bot) handleMessage(msg *tgbotapi.Message) {
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

	// Extract image URLs from photos.
	var imageURLs []string
	if msg.Photo != nil && len(msg.Photo) > 0 {
		// Telegram sends multiple sizes; pick the highest resolution (last element).
		bestPhoto := msg.Photo[len(msg.Photo)-1]
		fileURL, err := b.api.GetFileDirectURL(bestPhoto.FileID)
		if err != nil {
			log.Printf("[message] failed to get photo URL: %v", err)
			b.emitEvent("error", fmt.Sprintf("photo URL: %v", err))
		} else {
			imageURLs = append(imageURLs, fileURL)
			if b.debug {
				log.Printf("[message] extracted photo URL: %s", fileURL)
			}
		}
	}

	// Extract message text.
	text := msg.Text
	if text == "" {
		text = msg.Caption // Support photo/document captions.
	}
	if text == "" && len(imageURLs) > 0 {
		text = "What's in this image?"
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
	var userID int64
	if msg.From != nil {
		username = msg.From.UserName
		userID = msg.From.ID
	}
	log.Printf("[message] from @%s (user=%d chat=%d): %s", username, userID, msg.Chat.ID, text)

	if b.debug {
		b.emitEvent("message_received", fmt.Sprintf("from @%s: %s", username, truncate(text, 100)))
	} else {
		b.emitEvent("message_received", fmt.Sprintf("from @%s (%d chars)", username, len(text)))
	}

	// Per-message context for cancellation.
	msgCtx, msgCancel := context.WithCancel(b.ctx)
	defer msgCancel()

	// Send typing indicator and refresh it while waiting.
	b.wg.Add(1)
	go b.sendTypingLoop(msgCtx, msg.Chat.ID)

	// Handle /help command.
	if text == "/help" || text == "/start" {
		msgCancel()
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
		b.sendResponse(msg.Chat.ID, helpMsg)
		return
	}

	// Handle /clear command to reset conversation.
	if text == "/clear" || text == "/reset" {
		msgCancel()
		b.kernelClient.ClearHistory(msg.Chat.ID)
		b.sendResponse(msg.Chat.ID, "Conversation cleared.")
		return
	}

	// Handle /aliases — list configured @mention aliases.
	if text == "/aliases" {
		msgCancel()
		b.handleAliasesCommand(msg.Chat.ID)
		return
	}

	// Check for direct @mention routing (fast path — no coordinator needed).
	if !b.aliases.IsEmpty() {
		result := b.aliases.Parse(text)
		if result.Target != nil {
			msgCancel()
			switch result.Target.Type {
			case alias.TargetAgent:
				b.handleAliasAgent(msg.Chat.ID, userID, username, result, imageURLs)
			case alias.TargetImage:
				b.handleImageGenerate(msg.Chat.ID, username, stripToolPrefix(result.Target.PluginID), result.Remainder)
			case alias.TargetVideo:
				b.handleVideoGenerate(msg.Chat.ID, username, stripToolPrefix(result.Target.PluginID), result.Remainder)
			}
			return
		}
	}

	// Route to coordinator agent (or default AI agent).
	coordinatorID := b.resolveDefaultAgent(msgCtx)
	systemPrompt := b.aliases.SystemPromptBlock()

	var response string
	var err error
	if coordinatorID != "" && systemPrompt != "" {
		// Use direct routing with alias-aware system prompt for coordinator.
		response, err = b.kernelClient.ChatWithAgentDirect(msgCtx, msg.Chat.ID, userID, coordinatorID, "", text, imageURLs, systemPrompt)
	} else if coordinatorID != "" {
		// Direct route to coordinator without system prompt (no aliases configured).
		response, err = b.kernelClient.ChatWithAgentDirect(msgCtx, msg.Chat.ID, userID, coordinatorID, "", text, imageURLs, "")
	} else {
		// Fallback: use normal agent discovery.
		response, err = b.kernelClient.ChatWithAgent(msgCtx, msg.Chat.ID, userID, text, imageURLs)
	}
	msgCancel()

	if err != nil {
		log.Printf("AI agent unavailable: %v", err)
		b.emitEvent("fallback", fmt.Sprintf("no AI agent: %v", err))
		response = fmt.Sprintf("There is no AI Agent configured (received %s)", time.Now().UTC().Format(time.RFC3339))
	} else {
		b.emitEvent("agent_response", fmt.Sprintf("response length=%d chars", len(response)))

		// Check if coordinator delegated to another alias.
		if delegatedAlias, delegatedMsg, ok := alias.ParseCoordinatorResponse(response); ok {
			if target := b.aliases.Resolve(delegatedAlias); target != nil {
				b.emitEvent("coordinator_delegate", fmt.Sprintf("@%s → %s", delegatedAlias, target.PluginID))
				// Re-route to the delegated target.
				delegCtx, delegCancel := context.WithCancel(b.ctx)
				switch target.Type {
				case alias.TargetAgent:
					delegatedResp, delegErr := b.kernelClient.ChatWithAgentDirect(
						delegCtx, msg.Chat.ID, userID, target.PluginID, target.Model, delegatedMsg, nil, "")
					if delegErr != nil {
						response = fmt.Sprintf("Failed to reach @%s: %v", delegatedAlias, delegErr)
					} else {
						response = formatAttributedResponse(delegatedAlias, delegatedResp)
					}
				case alias.TargetImage:
					delegCancel()
					b.handleImageGenerate(msg.Chat.ID, username, stripToolPrefix(target.PluginID), delegatedMsg)
					return
				case alias.TargetVideo:
					delegCancel()
					b.handleVideoGenerate(msg.Chat.ID, username, stripToolPrefix(target.PluginID), delegatedMsg)
					return
				}
				delegCancel()
			}
		}
	}

	// Attribute the response to the coordinator's alias (or plugin ID).
	if coordinatorID != "" && !strings.HasPrefix(response, "[@") {
		responderName := b.aliases.FindAliasByPluginID(coordinatorID)
		if responderName == "" {
			responderName = coordinatorID
		}
		response = formatAttributedResponse(responderName, response)
	}

	// Send the response, splitting if necessary.
	if err := b.sendResponse(msg.Chat.ID, response); err != nil {
		log.Printf("Error sending response: %v", err)
		b.emitEvent("error", fmt.Sprintf("send error: %v", err))
	} else {
		if b.debug {
			b.emitEvent("message_sent", fmt.Sprintf("to @%s: %s", username, truncate(response, 100)))
		} else {
			b.emitEvent("message_sent", fmt.Sprintf("to @%s (%d chars)", username, len(response)))
		}
	}
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
				b.sendResponse(chatID, fmt.Sprintf("Video ready! (%v)\n\n%s", elapsed, videoLink))
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

// handleAliasAgent routes a message directly to a specific agent via @mention.
func (b *Bot) handleAliasAgent(chatID int64, userID int64, username string, result alias.ParseResult, imageURLs []string) {
	target := result.Target
	message := result.Remainder
	if message == "" {
		b.sendResponse(chatID, fmt.Sprintf("Usage: @%s <message>", result.Alias))
		return
	}

	b.emitEvent("alias_route", fmt.Sprintf("@%s → %s from @%s", result.Alias, target.PluginID, username))

	reqCtx, reqCancel := context.WithCancel(b.ctx)
	defer reqCancel()

	b.wg.Add(1)
	go b.sendTypingLoop(reqCtx, chatID)

	response, err := b.kernelClient.ChatWithAgentDirect(reqCtx, chatID, userID, target.PluginID, target.Model, message, imageURLs, "")
	reqCancel()

	if err != nil {
		log.Printf("Alias agent error (@%s → %s): %v", result.Alias, target.PluginID, err)
		b.emitEvent("alias_error", fmt.Sprintf("@%s: %v", result.Alias, err))
		b.sendResponse(chatID, fmt.Sprintf("@%s is not available: %v", result.Alias, err))
		return
	}

	b.sendResponse(chatID, formatAttributedResponse(result.Alias, response))
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

	if da := b.getDefaultAgent(); da != "" {
		sb.WriteString(fmt.Sprintf("\nCoordinator: %s", da))
	}

	sb.WriteString("\n\nUsage: @nickname <message>")
	b.sendResponse(chatID, sb.String())
}

// resolveDefaultAgent returns the configured coordinator agent plugin ID,
// falling back to auto-discovery if not set.
func (b *Bot) resolveDefaultAgent(ctx context.Context) string {
	if da := b.getDefaultAgent(); da != "" {
		return da
	}
	// Fall back to first available ai:chat agent.
	agentID, err := b.kernelClient.FindAIAgent(ctx)
	if err != nil {
		return ""
	}
	return agentID
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
