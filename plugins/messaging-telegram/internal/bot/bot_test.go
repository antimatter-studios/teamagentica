package bot

import (
	"strings"
	"testing"
)

func TestTruncate_Short(t *testing.T) {
	result := truncate("hello", 10)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncate_ExactLength(t *testing.T) {
	result := truncate("hello", 5)
	if result != "hello" {
		t.Errorf("expected 'hello', got %q", result)
	}
}

func TestTruncate_Long(t *testing.T) {
	result := truncate("hello world", 5)
	if result != "hello..." {
		t.Errorf("expected 'hello...', got %q", result)
	}
}

func TestSplitMessage_Short(t *testing.T) {
	chunks := splitMessage("hello", 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "hello" {
		t.Errorf("expected 'hello', got %q", chunks[0])
	}
}

func TestSplitMessage_ExactMaxLen(t *testing.T) {
	msg := strings.Repeat("a", 4096)
	chunks := splitMessage(msg, 4096)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestSplitMessage_SplitAtNewline(t *testing.T) {
	// Build a message just over the limit with a newline as a good break point.
	part1 := strings.Repeat("a", 50)
	part2 := strings.Repeat("b", 60)
	msg := part1 + "\n" + part2
	chunks := splitMessage(msg, 80)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0] != part1 {
		t.Errorf("first chunk should be %q, got %q", part1, chunks[0])
	}
}

func TestSplitMessage_SplitAtSpace(t *testing.T) {
	part1 := strings.Repeat("a", 50)
	part2 := strings.Repeat("b", 60)
	msg := part1 + " " + part2
	chunks := splitMessage(msg, 80)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0] != part1 {
		t.Errorf("first chunk should be %q, got %q", part1, chunks[0])
	}
}

func TestSplitMessage_HardCut(t *testing.T) {
	// No newlines or spaces — force hard cut.
	msg := strings.Repeat("a", 200)
	chunks := splitMessage(msg, 100)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 {
		t.Errorf("expected first chunk length=100, got %d", len(chunks[0]))
	}
	if len(chunks[1]) != 100 {
		t.Errorf("expected second chunk length=100, got %d", len(chunks[1]))
	}
}

func TestSplitMessage_MultipleChunks(t *testing.T) {
	msg := strings.Repeat("a", 300)
	chunks := splitMessage(msg, 100)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestSplitMessage_EmptyString(t *testing.T) {
	chunks := splitMessage("", 100)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != "" {
		t.Errorf("expected empty string, got %q", chunks[0])
	}
}
