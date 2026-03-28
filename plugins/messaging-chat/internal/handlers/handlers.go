package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/relay"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/storage"
)

const maxUploadSize = 10 << 20 // 10 MB

var allowedMimeTypes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/gif":       true,
	"image/webp":      true,
	"video/mp4":       true,
	"video/webm":      true,
	"video/quicktime": true,
	"audio/mpeg":      true,
	"audio/ogg":       true,
	"audio/wav":       true,
	"audio/webm":      true,
	"audio/mp4":       true,
}

type Handler struct {
	db      *storage.DB
	files   *storage.FileStore
	relay   *relay.Client
	sdk     *pluginsdk.Client
	aliases *alias.AliasMap
	debug   bool
}

func NewHandler(db *storage.DB, files *storage.FileStore, rc *relay.Client, sdk *pluginsdk.Client, aliases *alias.AliasMap, debug bool) *Handler {
	return &Handler{
		db:      db,
		files:   files,
		relay:   rc,
		sdk:     sdk,
		aliases: aliases,
		debug:   debug,
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "plugin": "messaging-chat"})
}

// --- Config Options ---

// ConfigOptions handles GET /config/options/:field — returns dynamic options for config fields.
func (h *Handler) ConfigOptions(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"options": []string{}})
}

// --- Agents ---

func (h *Handler) ListAgents(c *gin.Context) {
	entries := h.aliases.List()
	agents := make([]gin.H, 0, len(entries))
	for _, e := range entries {
		if e.Target.Type != alias.TargetAgent {
			continue
		}
		agents = append(agents, gin.H{
			"alias":     e.Alias,
			"plugin_id": e.Target.PluginID,
			"model":     e.Target.Model,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"agents": agents,
	})
}

// --- Conversations ---

func (h *Handler) ListConversations(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	convos, err := h.db.ListConversations(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversations": convos})
}

type createConversationReq struct {
	Title string `json:"title"`
}

func (h *Handler) CreateConversation(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	var req createConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	title := req.Title
	if title == "" {
		title = "New Chat"
	}
	conv := &storage.Conversation{
		UserID: userID,
		Title:  title,
	}
	if err := h.db.CreateConversation(conv); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, conv)
}

func (h *Handler) GetConversation(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	conv, err := h.db.GetConversation(uint(id))
	if err != nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("conversation %d not found", id)})
		return
	}
	msgs, err := h.db.ListMessages(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"conversation": conv, "messages": msgs})
}

type updateConversationReq struct {
	Title string `json:"title"`
}

func (h *Handler) UpdateConversation(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	conv, err := h.db.GetConversation(uint(id))
	if err != nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("conversation %d not found", id)})
		return
	}
	var req updateConversationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	conv.Title = req.Title
	if err := h.db.UpdateConversation(conv); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, conv)
}

func (h *Handler) DeleteConversation(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	conv, err := h.db.GetConversation(uint(id))
	if err != nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("conversation %d not found", id)})
		return
	}

	// Soft-delete conversation and its messages. Files are preserved.
	if err := h.db.DeleteConversation(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// --- Mark Read ---

func (h *Handler) MarkRead(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	conv, err := h.db.GetConversation(uint(id))
	if err != nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("conversation %d not found", id)})
		return
	}
	if err := h.db.MarkRead(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// --- Messages ---

type sendMessageReq struct {
	Content       string   `json:"content"`
	AttachmentIDs []string `json:"attachment_ids"`
}

func (h *Handler) SendMessage(c *gin.Context) {
	userID, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	convID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	conv, err := h.db.GetConversation(uint(convID))
	if err != nil || conv.UserID != userID {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("conversation %d not found", convID)})
		return
	}

	var req sendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Content == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "content required"})
		return
	}

	// Build the message to send to the relay.
	// If user specified @alias prefix, pass it through — the relay handles routing.
	messageText := req.Content

	// Build user message attachments JSON and image URLs for the relay.
	var attachmentsJSON string
	var imageURLs []string
	if len(req.AttachmentIDs) > 0 {
		var atts []storage.Attachment
		for _, fid := range req.AttachmentIDs {
			path, err := h.files.LoadFile(fid)
			if err != nil {
				continue
			}
			mimeType := mime.TypeByExtension(filepath.Ext(path))
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}
			attType := attachmentTypeFromMime(mimeType)
			atts = append(atts, storage.Attachment{
				Type:     attType,
				Filename: filepath.Base(path),
				FileID:   fid,
				MimeType: mimeType,
			})

			// Read file and encode as base64 data URL for the agent.
			data, err := readFileAsBase64(path, mimeType)
			if err == nil {
				imageURLs = append(imageURLs, data)
			}
		}
		if len(atts) > 0 {
			b, _ := json.Marshal(atts)
			attachmentsJSON = string(b)
		}
	}

	// Channel ID for relay routing: use conversation ID as the channel.
	channelID := fmt.Sprintf("chat:%d:%d", userID, convID)

	// Store user message (always with original content).
	userMsg := &storage.Message{
		ConversationID: uint(convID),
		Role:           "user",
		Content:        req.Content,
		Attachments:    attachmentsJSON,
	}
	if err := h.db.CreateMessage(userMsg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Send to relay — returns task_group_id immediately.
	// The actual response arrives via relay:progress events.
	accepted, err := h.relay.Chat(channelID, messageText, imageURLs)
	if err != nil {
		var ue *relay.UserError
		if errors.As(err, &ue) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":        ue.Message,
				"user_message": userMsg,
			})
		} else {
			log.Printf("[chat] relay error: %v", err)
			c.JSON(http.StatusBadGateway, gin.H{
				"error":        "agent request failed: " + err.Error(),
				"user_message": userMsg,
			})
		}
		return
	}

	// Auto-title on first exchange.
	if conv.Title == "New Chat" {
		title := req.Content
		if len(title) > 50 {
			title = title[:50]
		}
		conv.Title = title
		conv.UpdatedAt = time.Now()
		h.db.UpdateConversation(conv)
	}

	c.JSON(http.StatusAccepted, gin.H{
		"user_message":   userMsg,
		"task_group_id":  accepted.TaskGroupID,
	})
}

// --- Media Reference Resolution ---

// markdownDataImageRe matches ![alt](data:mime;base64,...) in response text.
var markdownDataImageRe = regexp.MustCompile(`!\[[^\]]*\]\(data:([^;]+);base64,([A-Za-z0-9+/=\s]+)\)`)

var (
	mediaRefRe    = regexp.MustCompile(`\{\{media:(.+?)\}\}`)
	// Use greedy match for media_url to handle base64 data that may contain '}'.
	mediaURLRefRe = regexp.MustCompile(`\{\{media_url:(.+)\}\}`)
)

// resolveMediaReferences scans agent response text for {{media:key}} and
// {{media_url:url}} markers. For stored media, it fetches from sss3-storage
// and saves locally. Returns cleaned text and any generated attachments.
func (h *Handler) resolveMediaReferences(text string) (string, []storage.Attachment) {
	var atts []storage.Attachment

	// Handle {{media:storage/key}} — reference sss3 key directly (no copy needed).
	text = mediaRefRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := mediaRefRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		key := parts[1]

		mimeType := mime.TypeByExtension(filepath.Ext(key))
		atts = append(atts, storage.Attachment{
			Type:       attachmentTypeFromMime(mimeType),
			Filename:   filepath.Base(key),
			StorageKey: key,
			MimeType:   mimeType,
		})
		return "" // strip marker from display text
	})

	// Handle {{media_url:...}} — external URL or data URL.
	text = mediaURLRefRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := mediaURLRefRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		rawURL := parts[1]

		// Handle data URLs: parse, decode base64 and save as a local file.
		if strings.HasPrefix(rawURL, "data:") {
			rest := strings.TrimPrefix(rawURL, "data:")
			semicolonIdx := strings.Index(rest, ";base64,")
			if semicolonIdx < 0 {
				log.Printf("[chat] invalid data URL format in media_url marker")
				return ""
			}
			mimeType := rest[:semicolonIdx]
			b64Data := rest[semicolonIdx+len(";base64,"):]
			att, err := h.saveMediaAttachment(mimeType, b64Data)
			if err != nil {
				log.Printf("[chat] failed to save data URL media: %v", err)
				return ""
			}
			atts = append(atts, att)
			return ""
		}

		// Parse URL to extract clean path (strip query params for extension detection).
		urlPath := rawURL
		if parsed, err := url.Parse(rawURL); err == nil {
			urlPath = parsed.Path
		}

		mimeType := mime.TypeByExtension(filepath.Ext(urlPath))

		atts = append(atts, storage.Attachment{
			Type:     "url",
			Filename: filepath.Base(urlPath),
			FileID:   "",
			MimeType: mimeType,
			URL:      rawURL,
		})
		return "" // strip marker from display text
	})

	// Clean up extra whitespace from stripped markers.
	text = strings.TrimSpace(text)

	return text, atts
}

// saveMediaAttachment decodes base64 image data and saves it to sss3 storage.
func (h *Handler) saveMediaAttachment(mimeType, b64Data string) (storage.Attachment, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return storage.Attachment{}, fmt.Errorf("decode base64: %w", err)
	}

	ext := extFromMime(mimeType)
	storageKey := "chat_" + uuid.New().String() + ext

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := h.sdk.StorageWrite(ctx, storageKey, bytes.NewReader(decoded), mimeType); err != nil {
		return storage.Attachment{}, fmt.Errorf("storage write: %w", err)
	}

	return storage.Attachment{
		Type:       attachmentTypeFromMime(mimeType),
		Filename:   "generated" + ext,
		StorageKey: storageKey,
		MimeType:   mimeType,
	}, nil
}

// attachmentTypeFromMime returns "image", "video", or "audio" based on MIME prefix.
func attachmentTypeFromMime(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "image"
	}
}

func extFromMime(mimeType string) string {
	switch mimeType {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	case "audio/webm":
		return ".weba"
	case "audio/mp4":
		return ".m4a"
	default:
		return ".bin"
	}
}

// --- Relay Progress ---

// RelayProgressEvent is the payload from a relay:progress addressed event.
type RelayProgressEvent struct {
	TaskGroupID string `json:"task_group_id"`
	ChannelID   string `json:"channel_id"`
	Status      string `json:"status"` // thinking, planning, running, synthesizing, completed, failed
	Message     string `json:"message"`
	// Fields present on completed:
	Response    string             `json:"response,omitempty"`
	Responder   string             `json:"responder,omitempty"`
	Model       string             `json:"model,omitempty"`
	Backend     string             `json:"backend,omitempty"`
	CostUSD     float64            `json:"cost_usd,omitempty"`
	DurationMs  int64              `json:"duration_ms,omitempty"`
	Usage       *relay.Usage       `json:"usage,omitempty"`
	Attachments []relay.Attachment `json:"attachments,omitempty"`
}

// HandleRelayProgress processes a relay:progress event.
func (h *Handler) HandleRelayProgress(detail string) {
	var ev RelayProgressEvent
	if err := json.Unmarshal([]byte(detail), &ev); err != nil {
		log.Printf("[progress] failed to parse relay:progress: %v", err)
		return
	}

	// Parse conversation ID from channel format "chat:<userID>:<convID>".
	parts := strings.SplitN(ev.ChannelID, ":", 3)
	if len(parts) != 3 {
		log.Printf("[progress] unexpected channel format: %s", ev.ChannelID)
		return
	}
	convID, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		log.Printf("[progress] invalid conv ID in channel %s: %v", ev.ChannelID, err)
		return
	}

	switch ev.Status {
	case "completed":
		// Delete progress markers.
		h.db.DeleteProgressMessages(uint(convID))

		// Process attachments.
		var allAttachments []storage.Attachment
		for _, att := range ev.Attachments {
			if att.ImageData != "" {
				saved, err := h.saveMediaAttachment(att.MimeType, att.ImageData)
				if err != nil {
					log.Printf("[progress] failed to save attachment: %v", err)
					continue
				}
				allAttachments = append(allAttachments, saved)
			} else if att.URL != "" {
				allAttachments = append(allAttachments, storage.Attachment{
					Type:     att.Type,
					MimeType: att.MimeType,
					URL:      att.URL,
					Filename: att.Filename,
				})
			}
		}

		var mediaAttJSON string
		if len(allAttachments) > 0 {
			b, _ := json.Marshal(allAttachments)
			mediaAttJSON = string(b)
		}

		// Store assistant message.
		assistantMsg := &storage.Message{
			ConversationID: uint(convID),
			Role:           "assistant",
			Content:        ev.Response,
			AgentAlias:     ev.Responder,
			Model:          ev.Model,
			CostUSD:        ev.CostUSD,
			DurationMs:     ev.DurationMs,
			Attachments:    mediaAttJSON,
		}
		if ev.Usage != nil {
			assistantMsg.InputTokens = ev.Usage.PromptTokens
			assistantMsg.OutputTokens = ev.Usage.CompletionTokens
			assistantMsg.CachedTokens = ev.Usage.CachedTokens
		}
		if err := h.db.CreateMessage(assistantMsg); err != nil {
			log.Printf("[progress] error storing assistant message: %v", err)
		}

		// Update conversation timestamp.
		if conv, err := h.db.GetConversation(uint(convID)); err == nil {
			conv.UpdatedAt = time.Now()
			h.db.UpdateConversation(conv)
		}

		log.Printf("[progress] stored assistant message for conv %d (tg=%s)", convID, ev.TaskGroupID)

	case "failed":
		h.db.DeleteProgressMessages(uint(convID))

		// Store error as assistant message so the user sees it.
		errMsg := &storage.Message{
			ConversationID: uint(convID),
			Role:           "assistant",
			Content:        ev.Message,
		}
		h.db.CreateMessage(errMsg)
		log.Printf("[progress] stored error message for conv %d: %s", convID, ev.Message)

	default:
		// Progress update — upsert the progress marker.
		msg := ev.Message
		if msg == "" {
			msg = ev.Status + "..."
		}
		if _, err := h.db.UpsertProgressMessage(uint(convID), msg); err != nil {
			log.Printf("[progress] failed to upsert progress: %v", err)
		}
		log.Printf("[progress] conv %d: %s", convID, msg)
	}
}

// --- File Upload ---

func (h *Handler) Upload(c *gin.Context) {
	_, err := parseUserID(c.GetHeader("Authorization"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file required"})
		return
	}
	defer file.Close()

	mimeType := header.Header.Get("Content-Type")
	if !allowedMimeTypes[mimeType] {
		// Try to detect from extension.
		mimeType = mime.TypeByExtension(filepath.Ext(header.Filename))
		if !allowedMimeTypes[mimeType] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type"})
			return
		}
	}

	fileID := uuid.New().String()
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		exts, _ := mime.ExtensionsByType(mimeType)
		if len(exts) > 0 {
			ext = exts[0]
		}
	}

	if _, err := h.files.SaveUpload(fileID, ext, file); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"file_id":  fileID,
		"filename": header.Filename,
	})
}

func (h *Handler) ServeFile(c *gin.Context) {
	key := strings.TrimPrefix(c.Param("filepath"), "/")
	if key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file id required"})
		return
	}

	// Try local disk first (backwards compat for legacy file_id attachments).
	if path, err := h.files.LoadFile(key); err == nil {
		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if mimeType != "" {
			c.Header("Content-Type", mimeType)
		}
		c.File(path)
		return
	}

	// Try sss3 storage — key may be flat (chat_uuid.png) or nested (plugin/uuid.png).
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	reader, contentType, err := h.sdk.StorageRead(ctx, key)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("file %q not found", key)})
		return
	}
	defer reader.Close()

	if contentType == "" {
		contentType = mime.TypeByExtension(filepath.Ext(key))
	}
	if contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.Status(http.StatusOK)
	io.Copy(c.Writer, reader)
}

// --- Helpers ---

func readFileAsBase64(path, mimeType string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil
}
