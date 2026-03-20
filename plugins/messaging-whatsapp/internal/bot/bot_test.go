package bot

import (
	"strings"
	"testing"

	waClient "github.com/antimatter-studios/teamagentica/plugins/messaging-whatsapp/internal/whatsapp"
)

func testBot() *Bot {
	return &Bot{wa: waClient.NewClient("", "", false)}
}

func TestExtractContent_TextMessage(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type: "text",
		Text: &waClient.TextBody{Body: "Hello world"},
	}
	got, _ := b.extractContent(msg)
	if got != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", got)
	}
}

func TestExtractContent_TextNilBody(t *testing.T) {
	b := testBot()
	msg := waClient.Message{Type: "text", Text: nil}
	got, _ := b.extractContent(msg)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractContent_ImageWithCaption(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type:  "image",
		Image: &waClient.Media{Caption: "Look at this"},
	}
	got, _ := b.extractContent(msg)
	if got != "Look at this" {
		t.Errorf("expected 'Look at this', got %q", got)
	}
}

func TestExtractContent_ImageNoCaption(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type:  "image",
		Image: &waClient.Media{},
	}
	got, _ := b.extractContent(msg)
	if got != "What's in this image?" {
		t.Errorf("expected default image text, got %q", got)
	}
}

func TestExtractContent_LocationWithName(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type: "location",
		Location: &waClient.Location{
			Name:      "Central Park",
			Address:   "NYC",
			Latitude:  40.785091,
			Longitude: -73.968285,
		},
	}
	got, _ := b.extractContent(msg)
	if got == "" {
		t.Error("expected non-empty location text")
	}
	if !strings.Contains(got, "Central Park") {
		t.Errorf("expected text to contain 'Central Park', got %q", got)
	}
}

func TestExtractContent_LocationNoName(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type: "location",
		Location: &waClient.Location{
			Latitude:  40.785091,
			Longitude: -73.968285,
		},
	}
	got, _ := b.extractContent(msg)
	if got == "" {
		t.Error("expected non-empty location text")
	}
}

func TestExtractContent_Contact(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type: "contacts",
		Contacts: []waClient.VCard{
			{
				Name:   waClient.VCardName{FormattedName: "John Doe"},
				Phones: []waClient.VCardPhone{{Phone: "+1234567890"}},
			},
		},
	}
	got, _ := b.extractContent(msg)
	if !strings.Contains(got, "John Doe") {
		t.Errorf("expected contact name in text, got %q", got)
	}
}

func TestExtractContent_Audio(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type:  "audio",
		Audio: &waClient.Media{ID: "abc123"},
	}
	got, _ := b.extractContent(msg)
	if got != "I sent you a voice message." {
		t.Errorf("expected voice message text, got %q", got)
	}
}

func TestExtractContent_Video(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type:  "video",
		Video: &waClient.Media{ID: "vid123"},
	}
	got, _ := b.extractContent(msg)
	if got != "I sent you a video." {
		t.Errorf("expected video text, got %q", got)
	}
}

func TestExtractContent_Document(t *testing.T) {
	b := testBot()
	msg := waClient.Message{
		Type:     "document",
		Document: &waClient.Media{Filename: "report.pdf"},
	}
	got, _ := b.extractContent(msg)
	if !strings.Contains(got, "report.pdf") {
		t.Errorf("expected document filename in text, got %q", got)
	}
}

func TestExtractContent_UnknownType(t *testing.T) {
	b := testBot()
	msg := waClient.Message{Type: "sticker"}
	got, _ := b.extractContent(msg)
	if got != "" {
		t.Errorf("expected empty for unknown type, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
