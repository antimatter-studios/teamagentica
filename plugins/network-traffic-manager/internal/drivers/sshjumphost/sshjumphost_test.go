package sshjumphost

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

// Note: only validation tests live here. The network/SSH-server logic
// (reverse tunnel handshake, embedded SSH server, agent forwarding,
// unix-socket proxy) requires a running bastion + SSH client and is
// covered by integration tests, not unit tests.

func TestRegistered(t *testing.T) {
	found := false
	for _, id := range drivers.Available() {
		if id == ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ssh-jumphost not registered; available: %v", drivers.Available())
	}
}

func minimalCfg(t *testing.T) map[string]string {
	t.Helper()
	priv, pub := generateTestKeyPair(t)
	return map[string]string{
		"bastion_host":        "bastion.example.com",
		"bastion_user":        "tunnel",
		"bastion_private_key": priv,
		"username":            "agent",
		"authorized_keys":     pub,
		"agent_socket_path":   "/tmp/sshjumphost-test.sock",
	}
}

func TestNew_RequiresBastionHost(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "bastion_host")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when bastion_host missing")
	}
}

func TestNew_RequiresBastionUser(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "bastion_user")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when bastion_user missing")
	}
}

func TestNew_RequiresBastionPrivateKey(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "bastion_private_key")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when bastion_private_key missing")
	}
}

func TestNew_RequiresUsername(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "username")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when username missing")
	}
}

func TestNew_RequiresAuthorizedKeys(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "authorized_keys")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when authorized_keys missing")
	}
}

func TestNew_RequiresAgentSocketPath(t *testing.T) {
	cfg := minimalCfg(t)
	delete(cfg, "agent_socket_path")
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected error when agent_socket_path missing")
	}
}

func TestNew_TargetIsIgnored(t *testing.T) {
	cfg := minimalCfg(t)
	// non-empty target should still succeed (target is documented as unused)
	d, err := New("test", "ignored-target", cfg)
	if err != nil {
		t.Fatalf("unexpected error with target set: %v", err)
	}
	if d == nil {
		t.Fatal("nil driver")
	}
}

func TestNew_Succeeds(t *testing.T) {
	d, err := New("test", "", minimalCfg(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st := d.Status(); st.State != drivers.StateStopped {
		t.Errorf("initial state=%q, want stopped", st.State)
	}
}

func TestNew_InvalidPort(t *testing.T) {
	cfg := minimalCfg(t)
	cfg["bastion_port"] = "99999"
	if _, err := New("test", "", cfg); err == nil {
		t.Fatal("expected port range error")
	}
}

func TestNew_HostKeyProvided(t *testing.T) {
	cfg := minimalCfg(t)
	hostKeyPEM, _ := generateTestKeyPair(t)
	cfg["host_key"] = hostKeyPEM
	if _, err := New("test", "", cfg); err != nil {
		t.Fatalf("unexpected error with host_key: %v", err)
	}
}

func TestNew_BastionKnownHostsParsed(t *testing.T) {
	cfg := minimalCfg(t)
	_, pub := generateTestKeyPair(t)
	cfg["bastion_known_hosts"] = pub
	if _, err := New("test", "", cfg); err != nil {
		t.Fatalf("unexpected error with bastion_known_hosts: %v", err)
	}
}

func TestStop_Idempotent(t *testing.T) {
	d, err := New("test", "", minimalCfg(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Errorf("Stop never-started: %v", err)
	}
	// second Stop should also be a no-op
	if err := d.Stop(context.Background()); err != nil {
		t.Errorf("Stop second call: %v", err)
	}
}

func TestParsePort(t *testing.T) {
	cases := []struct {
		in      string
		def     int
		want    int
		wantErr bool
	}{
		{"", 22, 22, false},
		{"2222", 22, 2222, false},
		{"-1", 22, 0, true},
		{"70000", 22, 0, true},
		{"abc", 22, 0, true},
	}
	for _, c := range cases {
		got, err := parsePort(c.in, c.def)
		if (err != nil) != c.wantErr {
			t.Errorf("parsePort(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if err == nil && got != c.want {
			t.Errorf("parsePort(%q)=%d want %d", c.in, got, c.want)
		}
	}
}

func generateTestKeyPair(t *testing.T) (privPEM, pubAuthorized string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal priv: %v", err)
	}
	privPEM = string(pem.EncodeToMemory(privBytes))

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh pub: %v", err)
	}
	pubAuthorized = string(ssh.MarshalAuthorizedKey(sshPub))
	return
}
