package config

import (
	"os"
	"testing"
)

func clearEnv() {
	os.Unsetenv("TEAMAGENTICA_PLUGIN_TOKEN")
	os.Unsetenv("TEAMAGENTICA_KERNEL_HOST")
	os.Unsetenv("TEAMAGENTICA_KERNEL_PORT")
	os.Unsetenv("TEAMAGENTICA_PLUGIN_ID")
	os.Unsetenv("WEBHOOK_INGRESS_PORT")
}

func TestLoad_RequiresServiceToken(t *testing.T) {
	clearEnv()
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
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PluginID != "webhook-ingress" {
		t.Errorf("expected PluginID=webhook-ingress, got %q", cfg.PluginID)
	}
	if cfg.HTTPPort != 9000 {
		t.Errorf("expected HTTPPort=9000, got %d", cfg.HTTPPort)
	}
	if cfg.KernelHost != "localhost" {
		t.Errorf("expected KernelHost=localhost, got %q", cfg.KernelHost)
	}
	if cfg.KernelPort != "8080" {
		t.Errorf("expected KernelPort=8080, got %q", cfg.KernelPort)
	}
	if cfg.ServiceToken != "svc-token" {
		t.Errorf("expected ServiceToken=svc-token, got %q", cfg.ServiceToken)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("TEAMAGENTICA_KERNEL_HOST", "mykernel")
	os.Setenv("TEAMAGENTICA_KERNEL_PORT", "9090")
	os.Setenv("TEAMAGENTICA_PLUGIN_ID", "my-ingress")
	os.Setenv("WEBHOOK_INGRESS_PORT", "7777")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PluginID != "my-ingress" {
		t.Errorf("expected PluginID=my-ingress, got %q", cfg.PluginID)
	}
	if cfg.HTTPPort != 7777 {
		t.Errorf("expected HTTPPort=7777, got %d", cfg.HTTPPort)
	}
	if cfg.KernelHost != "mykernel" {
		t.Errorf("expected KernelHost=mykernel, got %q", cfg.KernelHost)
	}
	if cfg.KernelPort != "9090" {
		t.Errorf("expected KernelPort=9090, got %q", cfg.KernelPort)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("WEBHOOK_INGRESS_PORT", "notanumber")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 9000 {
		t.Errorf("expected default HTTPPort=9000 for invalid value, got %d", cfg.HTTPPort)
	}
}
