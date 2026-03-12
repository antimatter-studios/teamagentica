package bot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestFormatAttributedResponse(t *testing.T) {
	tests := []struct {
		name     string
		respName string
		response string
		want     string
	}{
		{"with name", "agent1", "hello world", "[@agent1]\nhello world"},
		{"empty name returns raw response", "", "hello world", "hello world"},
		{"both empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatAttributedResponse(tt.respName, tt.response)
			if got != tt.want {
				t.Errorf("formatAttributedResponse(%q, %q) = %q, want %q", tt.respName, tt.response, got, tt.want)
			}
		})
	}
}

func TestStripToolPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"tool-dalle", "dalle"},
		{"tool-", ""},
		{"dalle", "dalle"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripToolPrefix(tt.input)
			if got != tt.want {
				t.Errorf("stripToolPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAppendAttachmentURLs(t *testing.T) {
	tests := []struct {
		name        string
		attachments []*discordgo.MessageAttachment
		want        []string
	}{
		{
			"image content type",
			[]*discordgo.MessageAttachment{{URL: "http://img.png", ContentType: "image/png"}},
			[]string{"http://img.png"},
		},
		{
			"video content type",
			[]*discordgo.MessageAttachment{{URL: "http://vid.mp4", ContentType: "video/mp4"}},
			[]string{"http://vid.mp4"},
		},
		{
			"audio content type",
			[]*discordgo.MessageAttachment{{URL: "http://aud.mp3", ContentType: "audio/mpeg"}},
			[]string{"http://aud.mp3"},
		},
		{
			"text content type excluded",
			[]*discordgo.MessageAttachment{{URL: "http://doc.txt", ContentType: "text/plain"}},
			nil,
		},
		{
			"empty content type with .png fallback",
			[]*discordgo.MessageAttachment{{URL: "http://img.png", ContentType: "", Filename: "photo.png"}},
			[]string{"http://img.png"},
		},
		{
			"empty content type with .txt excluded",
			[]*discordgo.MessageAttachment{{URL: "http://doc.txt", ContentType: "", Filename: "notes.txt"}},
			nil,
		},
		{
			"empty URL skipped",
			[]*discordgo.MessageAttachment{{URL: "", ContentType: "image/png"}},
			nil,
		},
		{
			"nil attachments",
			nil,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendAttachmentURLs(nil, tt.attachments)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAppendEmbedImageURLs(t *testing.T) {
	tests := []struct {
		name   string
		embeds []*discordgo.MessageEmbed
		want   []string
	}{
		{
			"image URL",
			[]*discordgo.MessageEmbed{{Image: &discordgo.MessageEmbedImage{URL: "http://img.png"}}},
			[]string{"http://img.png"},
		},
		{
			"thumbnail URL",
			[]*discordgo.MessageEmbed{{Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "http://thumb.png"}}},
			[]string{"http://thumb.png"},
		},
		{
			"both image and thumbnail",
			[]*discordgo.MessageEmbed{{
				Image:     &discordgo.MessageEmbedImage{URL: "http://img.png"},
				Thumbnail: &discordgo.MessageEmbedThumbnail{URL: "http://thumb.png"},
			}},
			[]string{"http://img.png", "http://thumb.png"},
		},
		{
			"nil image and thumbnail",
			[]*discordgo.MessageEmbed{{}},
			nil,
		},
		{
			"empty image URL skipped",
			[]*discordgo.MessageEmbed{{Image: &discordgo.MessageEmbedImage{URL: ""}}},
			nil,
		},
		{
			"empty thumbnail URL skipped",
			[]*discordgo.MessageEmbed{{Thumbnail: &discordgo.MessageEmbedThumbnail{URL: ""}}},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendEmbedImageURLs(nil, tt.embeds)
			if !strSliceEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func strSliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
