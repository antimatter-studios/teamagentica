package config

import (
	"os"
	"testing"
)

func clearEnv() {
	os.Unsetenv("TEAMAGENTICA_PLUGIN_ID")
	os.Unsetenv("PLUGIN_PORT")
	os.Unsetenv("PLUGIN_DATA_PATH")
	os.Unsetenv("PLUGIN_DEBUG")
	os.Unsetenv("WHATSAPP_ACCESS_TOKEN")
	os.Unsetenv("WHATSAPP_PHONE_NUMBER_ID")
	os.Unsetenv("WHATSAPP_VERIFY_TOKEN")
	os.Unsetenv("WHATSAPP_APP_SECRET")
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv()
	defer clearEnv()

	cfg := Load()

	if cfg.PluginID != "whatsapp-bot" {
		t.Errorf("expected PluginID=whatsapp-bot, got %q", cfg.PluginID)
	}
	if cfg.Port != 8091 {
		t.Errorf("expected Port=8091, got %d", cfg.Port)
	}
	if cfg.DataPath != "/data" {
		t.Errorf("expected DataPath=/data, got %q", cfg.DataPath)
	}
	if cfg.Debug {
		t.Error("expected Debug=false")
	}
	if cfg.AccessToken != "" {
		t.Errorf("expected empty AccessToken, got %q", cfg.AccessToken)
	}
	if cfg.PhoneNumberID != "" {
		t.Errorf("expected empty PhoneNumberID, got %q", cfg.PhoneNumberID)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_ID", "my-whatsapp")
	os.Setenv("PLUGIN_PORT", "9999")
	os.Setenv("PLUGIN_DATA_PATH", "/custom/data")
	os.Setenv("PLUGIN_DEBUG", "true")
	os.Setenv("WHATSAPP_ACCESS_TOKEN", "my-token")
	os.Setenv("WHATSAPP_PHONE_NUMBER_ID", "12345")
	os.Setenv("WHATSAPP_VERIFY_TOKEN", "verify-me")
	os.Setenv("WHATSAPP_APP_SECRET", "secret123")
	defer clearEnv()

	cfg := Load()

	if cfg.PluginID != "my-whatsapp" {
		t.Errorf("expected PluginID=my-whatsapp, got %q", cfg.PluginID)
	}
	if cfg.Port != 9999 {
		t.Errorf("expected Port=9999, got %d", cfg.Port)
	}
	if cfg.DataPath != "/custom/data" {
		t.Errorf("expected DataPath=/custom/data, got %q", cfg.DataPath)
	}
	if !cfg.Debug {
		t.Error("expected Debug=true")
	}
	if cfg.AccessToken != "my-token" {
		t.Errorf("expected AccessToken=my-token, got %q", cfg.AccessToken)
	}
	if cfg.PhoneNumberID != "12345" {
		t.Errorf("expected PhoneNumberID=12345, got %q", cfg.PhoneNumberID)
	}
	if cfg.VerifyToken != "verify-me" {
		t.Errorf("expected VerifyToken=verify-me, got %q", cfg.VerifyToken)
	}
	if cfg.AppSecret != "secret123" {
		t.Errorf("expected AppSecret=secret123, got %q", cfg.AppSecret)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv()
	os.Setenv("PLUGIN_PORT", "notanumber")
	defer clearEnv()

	cfg := Load()
	if cfg.Port != 8091 {
		t.Errorf("expected default Port=8091 for invalid value, got %d", cfg.Port)
	}
}

func TestLoad_DebugFalse(t *testing.T) {
	clearEnv()
	os.Setenv("PLUGIN_DEBUG", "false")
	defer clearEnv()

	cfg := Load()
	if cfg.Debug {
		t.Error("expected Debug=false when PLUGIN_DEBUG=false")
	}
}
