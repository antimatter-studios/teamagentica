package config

import (
	"os"
	"testing"
)

func clearEnv() {
	os.Unsetenv("TEAMAGENTICA_PLUGIN_TOKEN")
	os.Unsetenv("NGROK_AUTHTOKEN")
	os.Unsetenv("TEAMAGENTICA_KERNEL_HOST")
	os.Unsetenv("TEAMAGENTICA_KERNEL_PORT")
	os.Unsetenv("TEAMAGENTICA_PLUGIN_ID")
	os.Unsetenv("NGROK_DOMAIN")
	os.Unsetenv("NGROK_TUNNEL_TARGET")
	os.Unsetenv("NGROK_HTTP_PORT")
}

func TestLoad_RequiresServiceToken(t *testing.T) {
	clearEnv()
	os.Setenv("NGROK_AUTHTOKEN", "ngrok-token")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when TEAMAGENTICA_PLUGIN_TOKEN is missing")
	}
	if err.Error() != "TEAMAGENTICA_PLUGIN_TOKEN is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_RequiresNgrokAuthToken(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	defer clearEnv()

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when NGROK_AUTHTOKEN is missing")
	}
	if err.Error() != "NGROK_AUTHTOKEN is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("NGROK_AUTHTOKEN", "ngrok-token")
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
	if cfg.PluginID != "ngrok" {
		t.Errorf("expected PluginID=ngrok, got %q", cfg.PluginID)
	}
	if cfg.HTTPPort != 9100 {
		t.Errorf("expected HTTPPort=9100, got %d", cfg.HTTPPort)
	}
	// Default tunnel target = KernelHost:KernelPort
	if cfg.TunnelTarget != "localhost:8080" {
		t.Errorf("expected TunnelTarget=localhost:8080, got %q", cfg.TunnelTarget)
	}
	if cfg.NgrokDomain != "" {
		t.Errorf("expected empty NgrokDomain, got %q", cfg.NgrokDomain)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("NGROK_AUTHTOKEN", "ngrok-token")
	os.Setenv("TEAMAGENTICA_KERNEL_HOST", "myhost")
	os.Setenv("TEAMAGENTICA_KERNEL_PORT", "9090")
	os.Setenv("TEAMAGENTICA_PLUGIN_ID", "my-ngrok")
	os.Setenv("NGROK_DOMAIN", "my-app.ngrok.io")
	os.Setenv("NGROK_TUNNEL_TARGET", "custom-host:1234")
	os.Setenv("NGROK_HTTP_PORT", "7777")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.PluginID != "my-ngrok" {
		t.Errorf("expected PluginID=my-ngrok, got %q", cfg.PluginID)
	}
	if cfg.NgrokDomain != "my-app.ngrok.io" {
		t.Errorf("expected NgrokDomain=my-app.ngrok.io, got %q", cfg.NgrokDomain)
	}
	if cfg.TunnelTarget != "custom-host:1234" {
		t.Errorf("expected TunnelTarget=custom-host:1234, got %q", cfg.TunnelTarget)
	}
	if cfg.HTTPPort != 7777 {
		t.Errorf("expected HTTPPort=7777, got %d", cfg.HTTPPort)
	}
}

func TestLoad_InvalidHTTPPort(t *testing.T) {
	clearEnv()
	os.Setenv("TEAMAGENTICA_PLUGIN_TOKEN", "svc-token")
	os.Setenv("NGROK_AUTHTOKEN", "ngrok-token")
	os.Setenv("NGROK_HTTP_PORT", "notanumber")
	defer clearEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPPort != 9100 {
		t.Errorf("expected default HTTPPort=9100 for invalid value, got %d", cfg.HTTPPort)
	}
}

func TestKernelBaseURL(t *testing.T) {
	cfg := &Config{KernelHost: "myhost", KernelPort: "9090"}
	want := "http://myhost:9090"
	got := cfg.KernelBaseURL()
	if got != want {
		t.Errorf("KernelBaseURL() = %q, want %q", got, want)
	}
}
