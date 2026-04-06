package channels

import "testing"

func TestSanitizeChannelName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase", "My Channel", "my-channel"},
		{"removes special chars", "hello@world#123!", "helloworld123"},
		{"preserves hyphens", "my-channel", "my-channel"},
		{"preserves underscores", "my_channel", "my_channel"},
		{"multiple spaces become hyphens", "a  b  c", "a--b--c"},
		{"empty string", "", ""},
		{"only special chars", "!@#$%", ""},
		{"trims spaces", "  hello  ", "hello"},
		{"max 100 chars", string(make([]byte, 200)), ""},
		{"long valid name truncated", func() string {
			s := ""
			for i := 0; i < 110; i++ {
				s += "a"
			}
			return s
		}(), func() string {
			s := ""
			for i := 0; i < 100; i++ {
				s += "a"
			}
			return s
		}()},
		{"numbers only", "123", "123"},
		{"mixed case and spaces", "Hello World Test", "hello-world-test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeChannelName(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeChannelName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
