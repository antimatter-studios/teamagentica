package main

import (
	"testing"
)

func TestMatchRoute_SingleMatch(t *testing.T) {
	routes := []Route{
		{PluginID: "telegram-bot", Prefix: "/telegram-bot", TargetHost: "host1", TargetPort: 8443},
	}
	matched := MatchRoute(routes, "/telegram-bot/webhook")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.PluginID != "telegram-bot" {
		t.Errorf("expected telegram-bot, got %q", matched.PluginID)
	}
}

func TestMatchRoute_LongestPrefixWins(t *testing.T) {
	routes := []Route{
		{PluginID: "short", Prefix: "/t", TargetHost: "host1", TargetPort: 8080},
		{PluginID: "long", Prefix: "/telegram-bot", TargetHost: "host2", TargetPort: 8443},
	}
	matched := MatchRoute(routes, "/telegram-bot/webhook")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.PluginID != "long" {
		t.Errorf("expected longest prefix match 'long', got %q", matched.PluginID)
	}
}

func TestMatchRoute_NoMatch(t *testing.T) {
	routes := []Route{
		{PluginID: "telegram-bot", Prefix: "/telegram-bot", TargetHost: "host1", TargetPort: 8443},
	}
	matched := MatchRoute(routes, "/discord-bot/webhook")
	if matched != nil {
		t.Errorf("expected no match, got %q", matched.PluginID)
	}
}

func TestMatchRoute_EmptyRoutes(t *testing.T) {
	matched := MatchRoute(nil, "/some/path")
	if matched != nil {
		t.Error("expected no match for empty routes")
	}
}

func TestMatchRoute_ExactPrefix(t *testing.T) {
	routes := []Route{
		{PluginID: "bot", Prefix: "/bot", TargetHost: "host1", TargetPort: 8080},
	}
	matched := MatchRoute(routes, "/bot")
	if matched == nil {
		t.Fatal("expected a match for exact prefix")
	}
	if matched.PluginID != "bot" {
		t.Errorf("expected bot, got %q", matched.PluginID)
	}
}

func TestBuildRemainingPath_Empty(t *testing.T) {
	result := BuildRemainingPath("/telegram-bot", "/telegram-bot")
	if result != "/" {
		t.Errorf("expected '/', got %q", result)
	}
}

func TestBuildRemainingPath_WithSubpath(t *testing.T) {
	result := BuildRemainingPath("/telegram-bot/webhook", "/telegram-bot")
	if result != "/webhook" {
		t.Errorf("expected '/webhook', got %q", result)
	}
}

func TestBuildRemainingPath_NoLeadingSlash(t *testing.T) {
	result := BuildRemainingPath("/prefixsuffix", "/prefix")
	if result != "/suffix" {
		t.Errorf("expected '/suffix', got %q", result)
	}
}

func TestBuildTargetURL_Simple(t *testing.T) {
	result := BuildTargetURL("localhost", 8443, "/webhook", "")
	expected := "http://localhost:8443/webhook"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildTargetURL_WithQuery(t *testing.T) {
	result := BuildTargetURL("host", 9000, "/path", "foo=bar&baz=qux")
	expected := "http://host:9000/path?foo=bar&baz=qux"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildTargetURL_Root(t *testing.T) {
	result := BuildTargetURL("host", 8080, "/", "")
	expected := "http://host:8080/"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestNormalizePrefix_AlreadyNormalized(t *testing.T) {
	result := NormalizePrefix("/telegram-bot")
	if result != "/telegram-bot" {
		t.Errorf("expected '/telegram-bot', got %q", result)
	}
}

func TestNormalizePrefix_MissingSlash(t *testing.T) {
	result := NormalizePrefix("telegram-bot")
	if result != "/telegram-bot" {
		t.Errorf("expected '/telegram-bot', got %q", result)
	}
}

func TestNormalizePrefix_Empty(t *testing.T) {
	result := NormalizePrefix("")
	if result != "/" {
		t.Errorf("expected '/', got %q", result)
	}
}
