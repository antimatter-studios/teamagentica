package config

import (
	"os"
	"testing"
)

func clearEnv() {
	os.Unsetenv("TEAMAGENTICA_DISCORD_TOKEN")
	os.Unsetenv("TEAMAGENTICA_PLUGIN_ID")
}

func TestLoad_RequiresDiscordToken(t *testing.T) {
	clearEnv()
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when TEAMAGENTICA_DISCORD_TOKEN is missing")
	}
	if err.Error() != "TEAMAGENTICA_DISCORD_TOKEN is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_DISCORD_TOKEN", "test-token")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DiscordToken != "test-token" {
		t.Errorf("expected DiscordToken=test-token, got %q", cfg.DiscordToken)
	}
	if cfg.PluginID != "discord-bot" {
		t.Errorf("expected PluginID=discord-bot, got %q", cfg.PluginID)
	}
}

func TestLoad_CustomPluginID(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_DISCORD_TOKEN", "test-token")
	os.Setenv("TEAMAGENTICA_PLUGIN_ID", "my-discord")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PluginID != "my-discord" {
		t.Errorf("expected PluginID=my-discord, got %q", cfg.PluginID)
	}
}
