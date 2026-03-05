package alias

import (
	"strings"
	"sync"
	"testing"
)

func TestNewAliasMap(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
		{Name: "claude", Target: "agent-claude:claude-3-opus", Capabilities: []string{"ai:chat"}},
		{Name: "veo", Target: "tool-veo", Capabilities: []string{"tool:video"}},
		{Name: "banana", Target: "tool-nanobanana", Capabilities: []string{"tool:image"}},
		{Name: "", Target: "ignored"},     // empty name
		{Name: "noTarget", Target: ""},    // empty target
	})

	if m.IsEmpty() {
		t.Fatal("expected non-empty alias map")
	}

	entries := m.List()
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	// Alphabetical: banana, claude, codex, veo
	if entries[0].Alias != "banana" || entries[0].Target.Type != TargetImage {
		t.Errorf("entry 0: got %+v", entries[0])
	}
	if entries[1].Alias != "claude" || entries[1].Target.Model != "claude-3-opus" {
		t.Errorf("entry 1: got %+v", entries[1])
	}
	if entries[2].Alias != "codex" || entries[2].Target.PluginID != "agent-openai" {
		t.Errorf("entry 2: got %+v", entries[2])
	}
	if entries[3].Alias != "veo" || entries[3].Target.Type != TargetVideo {
		t.Errorf("entry 3: got %+v", entries[3])
	}
}

func TestParse(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"ai:chat"}},
		{Name: "veo", Target: "tool-veo", Capabilities: []string{"tool:video"}},
	})

	tests := []struct {
		input     string
		wantAlias string
		wantRem   string
		wantMatch bool
	}{
		{"@codex hello world", "codex", "hello world", true},
		{"@CODEX hello", "codex", "hello", true},      // case insensitive
		{"@claude", "claude", "", true},                 // no remainder
		{"@veo make a video", "veo", "make a video", true},
		{"hello @codex", "", "hello @codex", false},     // not at start
		{"@unknown test", "", "@unknown test", false},   // unknown alias
		{"no mention", "", "no mention", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		result := m.Parse(tt.input)
		if tt.wantMatch {
			if result.Target == nil {
				t.Errorf("Parse(%q): expected match, got nil target", tt.input)
				continue
			}
			if result.Alias != tt.wantAlias {
				t.Errorf("Parse(%q): alias=%q, want %q", tt.input, result.Alias, tt.wantAlias)
			}
			if result.Remainder != tt.wantRem {
				t.Errorf("Parse(%q): remainder=%q, want %q", tt.input, result.Remainder, tt.wantRem)
			}
		} else {
			if result.Target != nil {
				t.Errorf("Parse(%q): expected no match, got %+v", tt.input, result.Target)
			}
			if result.Remainder != tt.wantRem {
				t.Errorf("Parse(%q): remainder=%q, want %q", tt.input, result.Remainder, tt.wantRem)
			}
		}
	}
}

func TestResolve(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
	})

	if target := m.Resolve("codex"); target == nil || target.PluginID != "agent-openai" {
		t.Errorf("Resolve(codex) = %+v", target)
	}
	if target := m.Resolve("@codex"); target == nil || target.PluginID != "agent-openai" {
		t.Errorf("Resolve(@codex) = %+v", target)
	}
	if target := m.Resolve("@CODEX"); target == nil || target.PluginID != "agent-openai" {
		t.Errorf("Resolve(@CODEX) = %+v", target)
	}
	if target := m.Resolve("unknown"); target != nil {
		t.Errorf("Resolve(unknown) = %+v, want nil", target)
	}
}

func TestNilAliasMap(t *testing.T) {
	var m *AliasMap

	if !m.IsEmpty() {
		t.Error("nil map should be empty")
	}
	if entries := m.List(); len(entries) != 0 {
		t.Error("nil map List should be empty")
	}
	result := m.Parse("@codex test")
	if result.Target != nil {
		t.Error("nil map Parse should not match")
	}
	if target := m.Resolve("codex"); target != nil {
		t.Error("nil map Resolve should return nil")
	}
	if prompt := m.SystemPromptBlock(); prompt != "" {
		t.Error("nil map SystemPromptBlock should be empty")
	}
}

func TestSystemPromptBlock(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
		{Name: "claude", Target: "agent-claude:claude-3-opus", Capabilities: []string{"ai:chat"}},
		{Name: "veo", Target: "tool-veo", Capabilities: []string{"tool:video"}},
		{Name: "banana", Target: "tool-nanobanana", Capabilities: []string{"tool:image"}},
	})

	block := m.SystemPromptBlock()
	if block == "" {
		t.Fatal("expected non-empty system prompt block")
	}

	// Should contain all aliases.
	for _, name := range []string{"@codex", "@claude", "@veo", "@banana"} {
		if !strings.Contains(block, name) {
			t.Errorf("system prompt missing %s", name)
		}
	}

	if !strings.Contains(block, "ROUTE:@alias") {
		t.Error("system prompt missing ROUTE instruction")
	}
}

func TestParseCoordinatorResponse(t *testing.T) {
	tests := []struct {
		input     string
		wantAlias string
		wantMsg   string
		wantOK    bool
	}{
		{"ROUTE:@claude\nhello from coordinator", "claude", "hello from coordinator", true},
		{"ROUTE:@veo\nmake a video of cats", "veo", "make a video of cats", true},
		{"ROUTE:@codex", "codex", "", true}, // no message body
		{"Just a normal response", "", "", false},
		{"ROUTE:missing-at", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		alias, msg, ok := ParseCoordinatorResponse(tt.input)
		if ok != tt.wantOK || alias != tt.wantAlias || msg != tt.wantMsg {
			t.Errorf("ParseCoordinatorResponse(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, alias, msg, ok, tt.wantAlias, tt.wantMsg, tt.wantOK)
		}
	}
}

func TestTargetFromCapabilities(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "a", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
		{Name: "b", Target: "agent-claude:sonnet", Capabilities: []string{"ai:chat"}},
		{Name: "c", Target: "tool-stability", Capabilities: []string{"tool:image:generate"}},
		{Name: "d", Target: "tool-veo", Capabilities: []string{"tool:video:generate"}},
		{Name: "e", Target: "custom-plugin", Capabilities: nil}, // no caps → defaults to agent
	})

	tests := []struct {
		alias     string
		wantType  TargetType
		wantID    string
		wantModel string
	}{
		{"a", TargetAgent, "agent-openai", ""},
		{"b", TargetAgent, "agent-claude", "sonnet"},
		{"c", TargetImage, "tool-stability", ""},
		{"d", TargetVideo, "tool-veo", ""},
		{"e", TargetAgent, "custom-plugin", ""},
	}

	for _, tt := range tests {
		target := m.Resolve(tt.alias)
		if target == nil {
			t.Errorf("Resolve(%q) = nil", tt.alias)
			continue
		}
		if target.Type != tt.wantType {
			t.Errorf("Resolve(%q).Type = %d, want %d", tt.alias, target.Type, tt.wantType)
		}
		if target.PluginID != tt.wantID {
			t.Errorf("Resolve(%q).PluginID = %q, want %q", tt.alias, target.PluginID, tt.wantID)
		}
		if target.Model != tt.wantModel {
			t.Errorf("Resolve(%q).Model = %q, want %q", tt.alias, target.Model, tt.wantModel)
		}
	}
}

func TestReplace(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
	})

	if target := m.Resolve("codex"); target == nil {
		t.Fatal("expected codex before replace")
	}

	// Replace with completely different aliases.
	m.Replace([]AliasInfo{
		{Name: "claude", Target: "agent-claude", Capabilities: []string{"ai:chat"}},
		{Name: "veo", Target: "tool-veo", Capabilities: []string{"tool:video"}},
	})

	if target := m.Resolve("codex"); target != nil {
		t.Error("codex should be gone after replace")
	}
	if target := m.Resolve("claude"); target == nil || target.PluginID != "agent-claude" {
		t.Errorf("Resolve(claude) after replace = %+v", target)
	}
	if target := m.Resolve("veo"); target == nil || target.Type != TargetVideo {
		t.Errorf("Resolve(veo) after replace = %+v", target)
	}

	entries := m.List()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after replace, got %d", len(entries))
	}
}

func TestReplaceEmpty(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai"},
	})
	m.Replace(nil)

	if !m.IsEmpty() {
		t.Error("expected empty after Replace(nil)")
	}
}

func TestReplaceConcurrent(t *testing.T) {
	m := NewAliasMap([]AliasInfo{
		{Name: "codex", Target: "agent-openai", Capabilities: []string{"ai:chat"}},
	})

	var wg sync.WaitGroup
	// Concurrent readers.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = m.Parse("@codex hello")
				_ = m.Resolve("codex")
				_ = m.List()
				_ = m.IsEmpty()
			}
		}()
	}
	// Concurrent writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Replace([]AliasInfo{{Name: "claude", Target: "agent-claude"}})
				m.Replace([]AliasInfo{{Name: "codex", Target: "agent-openai"}})
			}
		}()
	}
	wg.Wait()
	// If we get here without a race detector panic, the test passes.
}
