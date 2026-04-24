package main

import (
	"encoding/base64"
	"mime"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// Markers produced by infra-mcp-server when image/video generation tools are
// invoked:
//   {{media:storage/key}}       — sss3 storage reference (for image results)
//   {{media_url:url-or-dataurl}} — external URL or `data:` URL (for video results or inline b64)
//
// The web chat plugin (messaging-chat) used to be the only place that resolved
// these; relay now resolves them centrally so every messaging channel
// (Discord, Telegram, WhatsApp, chat) gets actual attachments instead of
// literal `{{media:...}}` text.

var (
	mediaRefRe    = regexp.MustCompile(`\{\{media:(.+?)\}\}`)
	mediaURLRefRe = regexp.MustCompile(`\{\{media_url:(.+?)\}\}`)
)

// resolveMediaMarkers strips complete {{media:...}} / {{media_url:...}} markers
// from `text`, returning the cleaned text and any resulting attachments.
// Incomplete markers (e.g. an unterminated `{{` at the end of a streamed chunk)
// are left in place so the caller can hold them back until the rest arrives.
func resolveMediaMarkers(text string) (string, []pluginsdk.AgentAttachment) {
	var atts []pluginsdk.AgentAttachment

	text = mediaRefRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := mediaRefRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		key := strings.TrimSpace(parts[1])
		mimeType := mime.TypeByExtension(filepath.Ext(key))
		atts = append(atts, pluginsdk.AgentAttachment{
			Type:     attachmentTypeFromMime(mimeType),
			URL:      "storage://" + key,
			MimeType: mimeType,
			Filename: filepath.Base(key),
		})
		return ""
	})

	text = mediaURLRefRe.ReplaceAllStringFunc(text, func(match string) string {
		parts := mediaURLRefRe.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		raw := strings.TrimSpace(parts[1])

		if strings.HasPrefix(raw, "data:") {
			rest := strings.TrimPrefix(raw, "data:")
			idx := strings.Index(rest, ";base64,")
			if idx < 0 {
				return ""
			}
			mimeType := rest[:idx]
			b64 := rest[idx+len(";base64,"):]
			if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
				return ""
			}
			atts = append(atts, pluginsdk.AgentAttachment{
				Type:      attachmentTypeFromMime(mimeType),
				ImageData: b64,
				MimeType:  mimeType,
			})
			return ""
		}

		urlPath := raw
		if parsed, err := url.Parse(raw); err == nil {
			urlPath = parsed.Path
		}
		mimeType := mime.TypeByExtension(filepath.Ext(urlPath))
		atts = append(atts, pluginsdk.AgentAttachment{
			Type:     attachmentTypeFromMime(mimeType),
			URL:      raw,
			MimeType: mimeType,
			Filename: filepath.Base(urlPath),
		})
		return ""
	})

	return text, atts
}

// splitAtPartialMarker handles streaming chunks: if the tail of `text` contains
// an unterminated `{{` (i.e. a marker that hasn't arrived in full yet), split
// so the stable prefix is emitted and the partial tail is retained for the
// next chunk.
func splitAtPartialMarker(text string) (safe, held string) {
	idx := strings.LastIndex(text, "{{")
	if idx < 0 {
		return text, ""
	}
	if strings.Contains(text[idx:], "}}") {
		return text, ""
	}
	return text[:idx], text[idx:]
}

func attachmentTypeFromMime(mimeType string) string {
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	default:
		return "file"
	}
}
