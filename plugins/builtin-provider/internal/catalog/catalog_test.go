package catalog

import (
	"testing"
)

func TestSearch_EmptyQuery(t *testing.T) {
	results := Search("")
	if len(results) != len(OfficialCatalog) {
		t.Errorf("expected all %d entries, got %d", len(OfficialCatalog), len(results))
	}
}

func TestSearch_ByPluginID(t *testing.T) {
	results := Search("agent-openai")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'agent-openai'")
	}
	found := false
	for _, r := range results {
		if r.PluginID == "agent-openai" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected agent-openai in results")
	}
}

func TestSearch_ByName(t *testing.T) {
	results := Search("Telegram")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'Telegram'")
	}
	found := false
	for _, r := range results {
		if r.PluginID == "messaging-telegram" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected messaging-telegram in results")
	}
}

func TestSearch_ByTag(t *testing.T) {
	results := Search("discord")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'discord' tag")
	}
	found := false
	for _, r := range results {
		if r.PluginID == "messaging-discord" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected messaging-discord in results")
	}
}

func TestSearch_ByDescription(t *testing.T) {
	results := Search("tunnel")
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for 'tunnel'")
	}
	found := false
	for _, r := range results {
		if r.PluginID == "network-ngrok" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ngrok in results")
	}
}

func TestSearch_NoMatch(t *testing.T) {
	results := Search("zzzznonexistent")
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent query, got %d", len(results))
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	results := Search("TELEGRAM")
	if len(results) == 0 {
		t.Fatal("expected results for case-insensitive 'TELEGRAM'")
	}
}

func TestSearch_AITag(t *testing.T) {
	results := Search("ai")
	// Multiple plugins have the "ai" tag.
	if len(results) < 3 {
		t.Errorf("expected at least 3 AI-related results, got %d", len(results))
	}
}

func TestSearch_VideoTag(t *testing.T) {
	results := Search("video")
	if len(results) < 2 {
		t.Errorf("expected at least 2 video-related results, got %d", len(results))
	}
}

func TestSearch_ImageTag(t *testing.T) {
	results := Search("image")
	if len(results) < 2 {
		t.Errorf("expected at least 2 image-related results, got %d", len(results))
	}
}

func TestMatches_PluginID(t *testing.T) {
	e := Entry{PluginID: "agent-openai", Name: "Test", Description: "Test"}
	if !matches(e, "openai") {
		t.Error("expected match on pluginID")
	}
}

func TestMatches_Name(t *testing.T) {
	e := Entry{PluginID: "test", Name: "OpenAI Agent", Description: "Test"}
	if !matches(e, "openai") {
		t.Error("expected match on name")
	}
}

func TestMatches_Description(t *testing.T) {
	e := Entry{PluginID: "test", Name: "Test", Description: "AI agent for openai"}
	if !matches(e, "openai") {
		t.Error("expected match on description")
	}
}

func TestMatches_Tag(t *testing.T) {
	e := Entry{PluginID: "test", Name: "Test", Description: "Test", Tags: []string{"chat", "ai"}}
	if !matches(e, "chat") {
		t.Error("expected match on tag")
	}
}

func TestMatches_NoMatch(t *testing.T) {
	e := Entry{PluginID: "test", Name: "Test", Description: "Test", Tags: []string{"tag1"}}
	if matches(e, "zzzzz") {
		t.Error("expected no match")
	}
}

func TestOfficialCatalog_HasExpectedPlugins(t *testing.T) {
	expectedIDs := []string{
		"agent-openai", "messaging-discord", "messaging-telegram", "messaging-whatsapp",
		"network-ngrok", "network-webhook-ingress", "agent-gemini",
	}
	for _, id := range expectedIDs {
		found := false
		for _, e := range OfficialCatalog {
			if e.PluginID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected plugin %q in OfficialCatalog", id)
		}
	}
}

func TestOfficialCatalog_EntriesHaveRequiredFields(t *testing.T) {
	for _, e := range OfficialCatalog {
		if e.PluginID == "" {
			t.Error("entry has empty PluginID")
		}
		if e.Name == "" {
			t.Errorf("entry %s has empty Name", e.PluginID)
		}
		if e.Description == "" {
			t.Errorf("entry %s has empty Description", e.PluginID)
		}
		if e.Version == "" {
			t.Errorf("entry %s has empty Version", e.PluginID)
		}
		if e.Image == "" {
			t.Errorf("entry %s has empty Image", e.PluginID)
		}
		if e.Author == "" {
			t.Errorf("entry %s has empty Author", e.PluginID)
		}
		if len(e.Tags) == 0 {
			t.Errorf("entry %s has no Tags", e.PluginID)
		}
	}
}
