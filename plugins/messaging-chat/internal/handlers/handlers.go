package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk/alias"
	"github.com/antimatter-studios/teamagentica/plugins/messaging-chat/internal/kernel"
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
	db           *storage.DB
	files        *storage.FileStore
	kernel       *kernel.Client
	sdk          *pluginsdk.Client
	aliases      *alias.AliasMap
	defaultAgent atomic.Pointer[string]
	debug        bool
}

func NewHandler(db *storage.DB, files *storage.FileStore, kc *kernel.Client, sdk *pluginsdk.Client, aliases *alias.AliasMap, defaultAgent string, debug bool) *Handler {
	h := &Handler{
		db:      db,
		files:   files,
		kernel:  kc,
		sdk:     sdk,
		aliases: aliases,
		debug:   debug,
	}
	h.SetDefaultAgent(defaultAgent)
	return h
}

func (h *Handler) DefaultAgent() string {
	if p := h.defaultAgent.Load(); p != nil {
		return *p
	}
	return ""
}

func (h *Handler) SetDefaultAgent(agent string) {
	h.defaultAgent.Store(&agent)
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok", "plugin": "messaging-chat"})
}

// --- Config Options ---

// ConfigOptions handles GET /config/options/:field — returns dynamic options for config fields.
func (h *Handler) ConfigOptions(c *gin.Context) {
	field := c.Param("field")
	if field == "DEFAULT_AGENT" {
		entries := h.aliases.List()
		var names []string
		for _, e := range entries {
			if e.Target.Type == alias.TargetAgent {
				names = append(names, e.Alias)
			}
		}
		c.JSON(http.StatusOK, gin.H{"options": names})
		return
	}
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
		"agents":          agents,
		"has_coordinator": h.DefaultAgent() != "",
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
	AgentAlias string `json:"agent_alias"`
	Title      string `json:"title"`
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
		UserID:       userID,
		Title:        title,
		DefaultAgent: req.AgentAlias,
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
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
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
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
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
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}

	// Collect file IDs and storage keys from messages for cleanup.
	msgs, _ := h.db.ListMessages(uint(id))
	var fileIDs []string
	var storageKeys []string
	for _, msg := range msgs {
		if msg.Attachments == "" {
			continue
		}
		var atts []storage.Attachment
		if json.Unmarshal([]byte(msg.Attachments), &atts) == nil {
			for _, a := range atts {
				if a.FileID != "" {
					fileIDs = append(fileIDs, a.FileID)
				}
				if a.StorageKey != "" {
					storageKeys = append(storageKeys, a.StorageKey)
				}
			}
		}
	}

	if err := h.db.DeleteConversation(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Clean up local files in background.
	if len(fileIDs) > 0 {
		go h.files.DeleteFiles(fileIDs)
	}

	// Clean up sss3 storage keys in background.
	if len(storageKeys) > 0 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, key := range storageKeys {
				if err := h.sdk.StorageDelete(ctx, key); err != nil {
					log.Printf("[chat] failed to delete storage key %s: %v", key, err)
				}
			}
		}()
	}

	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// --- Messages ---

type sendMessageReq struct {
	Content       string   `json:"content"`
	AgentAlias    string   `json:"agent_alias"`
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
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
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

	// --- Phase 1: @alias prefix → direct route, strip prefix ---
	messageText := req.Content
	var agentAlias string
	var systemPrompt string
	useCoordinator := false

	if strings.HasPrefix(messageText, "@") {
		parsed := h.aliases.Parse(messageText)
		if parsed.Target != nil && parsed.Target.Type == alias.TargetAgent {
			agentAlias = parsed.Alias
			messageText = parsed.Remainder
			if messageText == "" {
				messageText = req.Content // fallback: send original if no remainder
			}
		}
	}

	// --- Phase 2: No prefix, no explicit agent → coordinator ---
	if agentAlias == "" {
		agentAlias = req.AgentAlias
	}
	if agentAlias == "" {
		agentAlias = conv.DefaultAgent
	}
	if agentAlias == "" {
		coordAlias := h.DefaultAgent()
		if coordAlias != "" {
			agentAlias = coordAlias
			systemPrompt = h.aliases.SystemPromptBlock()
			useCoordinator = true
		}
	}

	// --- Phase 3: Still nothing → error ---
	if agentAlias == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no agent selected"})
		return
	}

	target := h.aliases.Resolve(agentAlias)
	if target == nil || target.Type != alias.TargetAgent {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown agent alias: %s", agentAlias)})
		return
	}

	// Build user message attachments JSON.
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

	// Store user message (always with original content).
	userMsg := &storage.Message{
		ConversationID: uint(convID),
		Role:           "user",
		Content:        req.Content,
		AgentAlias:     agentAlias,
		Attachments:    attachmentsJSON,
	}
	if err := h.db.CreateMessage(userMsg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Build conversation history for agent.
	history, err := h.db.ListMessagesForContext(uint(convID), 80)
	if err != nil {
		log.Printf("[chat] error loading history: %v", err)
	}
	convMsgs := make([]kernel.ConversationMsg, 0, len(history))
	for _, m := range history {
		if m.ID == userMsg.ID {
			continue // Skip the just-created user message (we'll send it as the current message).
		}
		convMsgs = append(convMsgs, kernel.ConversationMsg{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	// Inject agent identity for direct @alias routes (non-coordinator).
	if systemPrompt == "" && !useCoordinator {
		systemPrompt = fmt.Sprintf("You are @%s (%s). You are one of several AI agents in a collaborative platform.", agentAlias, target.PluginID)
	}

	// Send to agent via kernel.
	start := time.Now()
	agentResp, err := h.kernel.ChatWithAgent(userID, target.PluginID, target.Model, messageText, imageURLs, convMsgs, systemPrompt)
	elapsed := time.Since(start)
	if err != nil {
		log.Printf("[chat] agent error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"error":        "agent request failed: " + err.Error(),
			"user_message": userMsg,
		})
		return
	}

	// --- Coordinator delegation check ---
	if useCoordinator {
		if delegateAlias, delegateMsg, isDelegation := alias.ParseCoordinatorResponse(agentResp.Response); isDelegation {
			delegateTarget := h.aliases.Resolve(delegateAlias)
			if delegateTarget != nil {
				if delegateMsg == "" {
					delegateMsg = messageText
				}
				log.Printf("[chat] coordinator delegated to @%s", delegateAlias)
				delegateIdentity := fmt.Sprintf("You are @%s (%s). You are one of several AI agents in a collaborative platform.", delegateAlias, delegateTarget.PluginID)
				dStart := time.Now()
				delegateResp, dErr := h.kernel.ChatWithAgent(userID, delegateTarget.PluginID, delegateTarget.Model, delegateMsg, imageURLs, convMsgs, delegateIdentity)
				dElapsed := time.Since(dStart)
				if dErr == nil {
					agentResp = delegateResp
					elapsed = dElapsed
					agentAlias = delegateAlias
					target = delegateTarget
				} else {
					log.Printf("[chat] delegate @%s failed: %v, returning coordinator response", delegateAlias, dErr)
				}
			}
		}
	}

	// Store the raw LLM response exactly as received.
	responseText := agentResp.Response

	// Process structured media attachments from agent response into local files.
	var allAttachments []storage.Attachment
	for _, att := range agentResp.Attachments {
		saved, err := h.saveMediaAttachment(att.MimeType, att.ImageData)
		if err != nil {
			log.Printf("[chat] failed to save attachment: %v", err)
			continue
		}
		allAttachments = append(allAttachments, saved)
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
		Content:        responseText,
		AgentAlias:     agentAlias,
		AgentPlugin:    target.PluginID,
		Model:          agentResp.Model,
		DurationMs:     elapsed.Milliseconds(),
		Attachments:    mediaAttJSON,
	}
	if agentResp.Usage != nil {
		assistantMsg.InputTokens = agentResp.Usage.PromptTokens
		assistantMsg.OutputTokens = agentResp.Usage.CompletionTokens
	}
	if err := h.db.CreateMessage(assistantMsg); err != nil {
		log.Printf("[chat] error storing assistant message: %v", err)
	}

	// Update conversation metadata.
	if !useCoordinator {
		conv.DefaultAgent = agentAlias
	}
	conv.UpdatedAt = time.Now()
	// Auto-title on first exchange.
	if conv.Title == "New Chat" {
		title := req.Content
		if len(title) > 50 {
			title = title[:50]
		}
		conv.Title = title
	}
	h.db.UpdateConversation(conv)

	c.JSON(http.StatusOK, gin.H{
		"user_message":      userMsg,
		"assistant_message": assistantMsg,
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
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
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
