package config

import (
	"os"
	"testing"
)

func clearEnv() {
	os.Unsetenv("TELEGRAM_BOT_TOKEN")
	os.Unsetenv("TEAMAGENTICA_PLUGIN_TOKEN")
	os.Unsetenv("TEAMAGENTICA_KERNEL_HOST")
	os.Unsetenv("TEAMAGENTICA_KERNEL_PORT")
	os.Unsetenv("TEAMAGENTICA_PLUGIN_ID")
	os.Unsetenv("TELEGRAM_ALLOWED_USERS")
	os.Unsetenv("TELEGRAM_POLL_TIMEOUT")
	os.Unsetenv("TELEGRAM_HTTP_PORT")
	os.Unsetenv("PLUGIN_DEBUG")
}

func TestLoad_RequiresTelegramToken(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "test-token")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when TELEGRAM_BOT_TOKEN is missing")
	}
	if err.Error() != "TELEGRAM_BOT_TOKEN is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_RequiresServiceToken(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when TEAMAGENTICA_PLUGIN_TOKEN is missing")
	}
	if err.Error() != "TEAMAGENTICA_PLUGIN_TOKEN is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.KernelHost != "localhost" {
		t.Errorf("expected KernelHost=localhost, got %q", cfg.KernelHost)
	}
	if cfg.KernelPort != "8080" {
		t.Errorf("expected KernelPort=8080, got %q", cfg.KernelPort)
	}
	if cfg.PluginID != "telegram-bot" {
		t.Errorf("expected PluginID=telegram-bot, got %q", cfg.PluginID)
	}
	if cfg.PollTimeout != 60 {
		t.Errorf("expected PollTimeout=60, got %d", cfg.PollTimeout)
	}
	if cfg.HTTPPort != 8443 {
		t.Errorf("expected HTTPPort=8443, got %d", cfg.HTTPPort)
	}
	if cfg.Debug {
		t.Error("expected Debug=false")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("TEAMAGENTICA_KERNEL_HOST", "mykernel")
	os.Setenv("TEAMAGENTICA_KERNEL_PORT", "9090")
	os.Setenv("TEAMAGENTICA_PLUGIN_ID", "my-telegram")
	os.Setenv("TELEGRAM_ALLOWED_USERS", "123,456")
	os.Setenv("TELEGRAM_POLL_TIMEOUT", "30")
	os.Setenv("TELEGRAM_HTTP_PORT", "7777")
	os.Setenv("PLUGIN_DEBUG", "true")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.KernelHost != "mykernel" {
		t.Errorf("expected KernelHost=mykernel, got %q", cfg.KernelHost)
	}
	if cfg.KernelPort != "9090" {
		t.Errorf("expected KernelPort=9090, got %q", cfg.KernelPort)
	}
	if cfg.PluginID != "my-telegram" {
		t.Errorf("expected PluginID=my-telegram, got %q", cfg.PluginID)
	}
	if cfg.PollTimeout != 30 {
		t.Errorf("expected PollTimeout=30, got %d", cfg.PollTimeout)
	}
	if cfg.HTTPPort != 7777 {
		t.Errorf("expected HTTPPort=7777, got %d", cfg.HTTPPort)
	}
	if !cfg.Debug {
		t.Error("expected Debug=true")
	}
}

func TestLoad_DebugFlag1(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("PLUGIN_DEBUG", "1")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Debug {
		t.Error("expected Debug=true when PLUGIN_DEBUG=1")
	}
}

func TestLoad_InvalidPollTimeout(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("TELEGRAM_POLL_TIMEOUT", "notanumber")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PollTimeout != 60 {
		t.Errorf("expected default PollTimeout=60 for invalid value, got %d", cfg.PollTimeout)
	}
}

func TestLoad_InvalidHTTPPort(t *testing.T) {
	clearEnv()
	os.Setenv("TELEGRAM_BOT_TOKEN", "bot-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("TELEGRAM_HTTP_PORT", "abc")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 8443 {
		t.Errorf("expected default HTTPPort=8443 for invalid value, got %d", cfg.HTTPPort)
	}
}

func TestKernelBaseURL(t *testing.T) {
	cfg := &Config{
		KernelHost: "myhost",
		KernelPort: "9090",
	}
	want := "http://myhost:9090"
	got := cfg.KernelBaseURL()
	if got != want {
		t.Errorf("KernelBaseURL() = %q, want %q", got, want)
	}
}

func TestParseAllowedUsers_Empty(t *testing.T) {
	cfg := &Config{AllowedUsers: ""}
	result := cfg.ParseAllowedUsers()
	if result != nil {
		t.Errorf("expected nil for empty AllowedUsers, got %v", result)
	}
}

func TestParseAllowedUsers_Valid(t *testing.T) {
	cfg := &Config{AllowedUsers: "123,456,789"}
	result := cfg.ParseAllowedUsers()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 users, got %d", len(result))
	}
	for _, id := range []int64{123, 456, 789} {
		if !result[id] {
			t.Errorf("expected user %d in allowed list", id)
		}
	}
}

func TestParseAllowedUsers_WithSpaces(t *testing.T) {
	cfg := &Config{AllowedUsers: " 123 , 456 "}
	result := cfg.ParseAllowedUsers()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 users, got %d", len(result))
	}
}

func TestParseAllowedUsers_InvalidIDs(t *testing.T) {
	cfg := &Config{AllowedUsers: "abc,def"}
	result := cfg.ParseAllowedUsers()
	if result != nil {
		t.Errorf("expected nil for all-invalid IDs, got %v", result)
	}
}

func TestParseAllowedUsers_MixedValidInvalid(t *testing.T) {
	cfg := &Config{AllowedUsers: "123,abc,456"}
	result := cfg.ParseAllowedUsers()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 valid users, got %d", len(result))
	}
}

func TestParseAllowedUsers_EmptyEntries(t *testing.T) {
	cfg := &Config{AllowedUsers: "123,,456,"}
	result := cfg.ParseAllowedUsers()
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 users, got %d", len(result))
	}
}
