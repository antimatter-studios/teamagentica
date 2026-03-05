package bot

import (
	"testing"

	waClient "github.com/antimatter-studios/teamagentica/plugins/whatsapp/internal/whatsapp"
)

func TestExtractText_TextMessage(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type: "text",
		Text: &waClient.TextBody{Body: "Hello world"},
	}
	got := b.extractText(msg)
	if got != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", got)
	}
}

func TestExtractText_TextNilBody(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{Type: "text", Text: nil}
	got := b.extractText(msg)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractText_ImageWithCaption(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type:  "image",
		Image: &waClient.Media{Caption: "Look at this"},
	}
	got := b.extractText(msg)
	if got != "Look at this" {
		t.Errorf("expected 'Look at this', got %q", got)
	}
}

func TestExtractText_ImageNoCaption(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type:  "image",
		Image: &waClient.Media{},
	}
	got := b.extractText(msg)
	if got != "What's in this image?" {
		t.Errorf("expected default image text, got %q", got)
	}
}

func TestExtractText_LocationWithName(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type: "location",
		Location: &waClient.Location{
			Name:      "Central Park",
			Address:   "NYC",
			Latitude:  40.785091,
			Longitude: -73.968285,
		},
	}
	got := b.extractText(msg)
	if got == "" {
		t.Error("expected non-empty location text")
	}
	if !contains(got, "Central Park") {
		t.Errorf("expected text to contain 'Central Park', got %q", got)
	}
}

func TestExtractText_LocationNoName(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type: "location",
		Location: &waClient.Location{
			Latitude:  40.785091,
			Longitude: -73.968285,
		},
	}
	got := b.extractText(msg)
	if got == "" {
		t.Error("expected non-empty location text")
	}
}

func TestExtractText_Contact(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type: "contacts",
		Contacts: []waClient.VCard{
			{
				Name:   waClient.VCardName{FormattedName: "John Doe"},
				Phones: []waClient.VCardPhone{{Phone: "+1234567890"}},
			},
		},
	}
	got := b.extractText(msg)
	if !contains(got, "John Doe") {
		t.Errorf("expected contact name in text, got %q", got)
	}
}

func TestExtractText_Audio(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type:  "audio",
		Audio: &waClient.Media{ID: "abc123"},
	}
	got := b.extractText(msg)
	if got != "[Voice message received]" {
		t.Errorf("expected voice message text, got %q", got)
	}
}

func TestExtractText_Video(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type:  "video",
		Video: &waClient.Media{ID: "vid123"},
	}
	got := b.extractText(msg)
	if got != "[Video received]" {
		t.Errorf("expected video text, got %q", got)
	}
}

func TestExtractText_Document(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{
		Type:     "document",
		Document: &waClient.Media{Filename: "report.pdf"},
	}
	got := b.extractText(msg)
	if !contains(got, "report.pdf") {
		t.Errorf("expected document filename in text, got %q", got)
	}
}

func TestExtractText_UnknownType(t *testing.T) {
	b := &Bot{}
	msg := waClient.Message{Type: "sticker"}
	got := b.extractText(msg)
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
