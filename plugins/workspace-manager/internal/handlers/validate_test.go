package handlers

import "testing"

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"The Claude Code Testing Env", "the-claude-code-testing-env"},
		{"my-workspace", "my-workspace"},
		{"Hello World!", "hello-world"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"UPPERCASE", "uppercase"},
		{"a/b/c", "a-b-c"},
		{"123 numbers", "123-numbers"},
		{"", ""},
		{"---", ""},
		{"café project", "caf-project"},
	}

	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsValidWorkspaceKey(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"my-workspace", true},
		{"test_123", true},
		{"", false},
		{"Has Spaces", false},
		{"UPPER", false},
		{"valid-key", true},
	}

	for _, tt := range tests {
		got := isValidWorkspaceKey(tt.input)
		if got != tt.valid {
			t.Errorf("isValidWorkspaceKey(%q) = %v, want %v", tt.input, got, tt.valid)
		}
	}
}
